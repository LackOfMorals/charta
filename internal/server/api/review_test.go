package api

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LackOfMorals/graingraph/internal/server/auth"
	"github.com/LackOfMorals/graingraph/internal/server/store"
)

func TestUnsupportedContentTypeIs415(t *testing.T) {
	srv, _ := newTestServer(t)
	code, resp, _ := do(t, "POST", srv.URL+"/db/neo4j/query/v2", "", "text/plain",
		`{"statement":"RETURN 1"}`, testUser, testPass)
	if code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415; body=%v", code, resp)
	}
	if errCode(resp) != CodeRequestInvalid {
		t.Fatalf("code = %v", errCode(resp))
	}
}

func TestDiscoveryPublicURL(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h := NewHandler(st, auth.New(testUser, testPass), "neo4j", time.Minute)
	h.SetPublicURL("https://graph.example.com/")

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "evil.example.net"
	req.Header.Set("X-Forwarded-Proto", "http")
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := "https://graph.example.com/db/{databaseName}/query/v2"
	if body["query"] != want {
		t.Fatalf("query = %v, want %s", body["query"], want)
	}
}

func TestPanicIsRecoveredAs500(t *testing.T) {
	h := observe(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}), true)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Fatalf("panic value leaked to the client: %s", rec.Body.String())
	}
}

func TestUnclassifiedErrorDetailIsNotLeaked(t *testing.T) {
	ae := mapGraingraphError(errors.New("open /var/lib/secret/graingraph.db: permission denied"))
	if ae.Code != CodeGeneralError {
		t.Fatalf("code = %s", ae.Code)
	}
	if strings.Contains(ae.Message, "/var/lib") {
		t.Fatalf("internal detail leaked: %q", ae.Message)
	}
}

func TestEncodeTypedSlicesAndErrors(t *testing.T) {
	got, err := encodeTyped([]int64{1, 2}, 2)
	if err != nil {
		t.Fatalf("[]int64: %v", err)
	}
	list, ok := got.(typedValue)
	if !ok || list.Type != "List" {
		t.Fatalf("[]int64 encoded as %#v, want a List", got)
	}
	items, _ := list.Value.([]any)
	if len(items) != 2 || items[0].(typedValue).Type != "Integer" {
		t.Fatalf("[]int64 items = %#v", items)
	}
	for _, v := range []any{[]int{1}, []float64{1.5}, []bool{true}, []string{"a"}} {
		g, err := encodeTyped(v, 2)
		if err != nil || g.(typedValue).Type != "List" {
			t.Fatalf("encodeTyped(%T) = %#v, %v; want a List", v, g, err)
		}
	}

	if _, err := encodeTyped(uint64(math.MaxInt64)+1, 2); err == nil {
		t.Fatal("uint64 above MaxInt64 encoded, want error (matches Plain JSON)")
	}
	if _, err := encodeTyped(map[string]any{"n": uint64(math.MaxUint64)}, 2); err == nil {
		t.Fatal("error inside a map was swallowed")
	}
}

func TestTypedParameterErrorsWrap(t *testing.T) {
	_, err := typedParam{Type: "Boolean", Value: json.RawMessage(`"x"`)}.convert(2)
	var ute *json.UnmarshalTypeError
	if !errors.As(err, &ute) {
		t.Fatalf("err = %v, want it to wrap *json.UnmarshalTypeError", err)
	}
}

func TestAcceptLists(t *testing.T) {
	for _, tc := range []struct {
		accept string
		typed  bool
		jsonl  bool
		ok     bool
	}{
		{"", false, false, true},
		{"*/*", false, false, true},
		{"application/json, text/plain", false, false, true},
		{"text/html, application/vnd.neo4j.query", true, false, true},
		{"application/json;q=0.5, application/vnd.neo4j.query.v1.2;q=0.9", true, false, true},
		{"application/vnd.neo4j.query.v1.2+jsonl, application/json", true, true, true},
		{"application/vnd.neo4j.query+jsonl, application/vnd.neo4j.query", true, false, true},
		{"application/vnd.neo4j.query+jsonl;q=0.9, application/vnd.neo4j.query;q=0.5", true, true, true},
		{"application/jsonl;q=0", false, false, false},
		{"text/html", false, false, false},
	} {
		rt, err := parseAccept(tc.accept)
		if (err == nil) != tc.ok {
			t.Errorf("%q: err = %v, want ok=%v", tc.accept, err, tc.ok)
			continue
		}
		if tc.ok && ((rt.format == formatTyped) != tc.typed || rt.jsonl != tc.jsonl) {
			t.Errorf("%q: got %+v, want typed=%v jsonl=%v", tc.accept, rt, tc.typed, tc.jsonl)
		}
	}
}

func TestHealth(t *testing.T) {
	srv, _ := newTestServer(t)
	code, body, _ := do(t, "GET", srv.URL+"/health", "", "", "", "", "")
	if code != http.StatusOK || body["status"] != "ok" {
		t.Fatalf("health = %d %v", code, body)
	}
}

func TestAutoCommitFailsFastWhileTransactionOpen(t *testing.T) {
	srv, _ := newTestServer(t)
	_, resp, _ := do(t, "POST", srv.URL+"/db/neo4j/query/v2/tx", "", "application/json", "", testUser, testPass)
	id := resp["transaction"].(map[string]any)["id"].(string)

	done := make(chan map[string]any, 1)
	go func() {
		_, body, _ := do(t, "POST", srv.URL+"/db/neo4j/query/v2", "", "application/json", `{"statement":"RETURN 1"}`, testUser, testPass)
		done <- body
	}()
	select {
	case body := <-done:
		msg := errMsg(body)
		if errCode(body) != CodeDBUnavailable || !strings.Contains(msg, id) {
			t.Fatalf("body = %v, want %s naming transaction %s", body, CodeDBUnavailable, id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("auto-commit query blocked behind an open transaction")
	}

	// A second transaction cannot open either.
	_, body, _ := do(t, "POST", srv.URL+"/db/neo4j/query/v2/tx", "", "application/json", "", testUser, testPass)
	if errCode(body) != CodeDBUnavailable {
		t.Fatalf("second open = %v", body)
	}

	// Once the transaction ends, auto-commit queries work again.
	do(t, "DELETE", srv.URL+"/db/neo4j/query/v2/tx/"+id, "", "", "", testUser, testPass)
	_, body, _ = do(t, "POST", srv.URL+"/db/neo4j/query/v2", "", "application/json", `{"statement":"RETURN 1"}`, testUser, testPass)
	if body["data"] == nil {
		t.Fatalf("query after rollback = %v", body)
	}
}

func TestCORSPreflight(t *testing.T) {
	srv, _ := newTestServer(t)
	req, _ := http.NewRequest("OPTIONS", srv.URL+"/db/neo4j/query/v2", nil)
	req.Header.Set("Origin", "https://app.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
	resp, err := http.DefaultClient.Do(req) // no credentials: preflights are unauthenticated
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	h := resp.Header
	if resp.StatusCode != http.StatusNoContent || h.Get("Access-Control-Allow-Origin") != "*" ||
		!strings.Contains(h.Get("Access-Control-Allow-Methods"), "POST") ||
		h.Get("Access-Control-Allow-Headers") != "authorization, content-type" {
		t.Fatalf("preflight = %d %v", resp.StatusCode, h)
	}

	// Actual requests carry the allow-origin header too.
	req, _ = http.NewRequest("GET", srv.URL+"/", nil)
	req.Header.Set("Origin", "https://app.example")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("simple request headers = %v", resp.Header)
	}
}

func TestCORSOriginList(t *testing.T) {
	h := withCORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}), []string{"https://ok.example"})
	for origin, want := range map[string]string{"https://ok.example": "https://ok.example", "https://evil.example": ""} {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != want {
			t.Errorf("origin %s: allow-origin = %q, want %q", origin, got, want)
		}
	}
	// Disabled: OPTIONS still answers, with no CORS headers.
	h = withCORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}), nil)
	req := httptest.NewRequest("OPTIONS", "/db/x", nil)
	req.Header.Set("Origin", "https://ok.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "" || rec.Header().Get("Allow") == "" {
		t.Fatalf("disabled OPTIONS = %d %v", rec.Code, rec.Header())
	}
}

// With a wait configured, a request that meets a transaction that ends soon
// waits for it instead of failing: Neo4j Bloom opens several at once.
func TestRequestsWaitForShortTransactions(t *testing.T) {
	st, err := store.New(":memory:", store.WithTxWait(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := httptest.NewServer(NewHandler(st, auth.New(testUser, testPass), "neo4j", 5*time.Minute).Routes())
	t.Cleanup(srv.Close)

	_, resp, _ := do(t, "POST", srv.URL+"/db/neo4j/query/v2/tx", "", "application/json", "", testUser, testPass)
	id := resp["transaction"].(map[string]any)["id"].(string)

	opened := make(chan map[string]any, 1)
	go func() {
		_, body, _ := do(t, "POST", srv.URL+"/db/neo4j/query/v2/tx", "", "application/json", `{"statement":"RETURN 2 AS x"}`, testUser, testPass)
		opened <- body
	}()
	time.Sleep(100 * time.Millisecond)
	select {
	case body := <-opened:
		t.Fatalf("second transaction did not wait: %v", body)
	default:
	}
	do(t, "POST", srv.URL+"/db/neo4j/query/v2/tx/"+id+"/commit", "", "application/json", `{}`, testUser, testPass)
	select {
	case body := <-opened:
		if body["transaction"] == nil || body["data"] == nil {
			t.Fatalf("second transaction = %v", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second transaction never opened")
	}
}
