package api

import (
	"net/http/httptest"
	"time"

	"encoding/json"
	"github.com/graingraph/graingraph/internal/server/auth"
	"github.com/graingraph/graingraph/internal/server/store"
	"net/http"
	"runtime"
	"strings"
	"testing"
)

func nestedTyped(depth int, leaf string) string {
	s := `{"$type":"String","_value":"` + leaf + `"}`
	for i := 0; i < depth; i++ {
		s = `{"$type":"List","_value":[` + s + `]}`
	}
	return s
}

func TestTypedParametersNestingBounded(t *testing.T) {
	leaf := strings.Repeat("A", 1<<20)
	raw := json.RawMessage(`{"p":` + nestedTyped(5000, leaf) + `}`)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if _, err := decodeParameters(raw, formatTyped, 2); err == nil {
		t.Fatal("expected a nesting error")
	}
	runtime.ReadMemStats(&after)
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 512<<20 {
		t.Fatalf("allocated %d MiB for a 1 MiB body", grew>>20)
	}
	ok := json.RawMessage(`{"p":` + nestedTyped(10, "x") + `}`)
	if _, err := decodeParameters(ok, formatTyped, 2); err != nil {
		t.Fatalf("shallow nesting must work: %v", err)
	}
}

func TestNonFiniteTypedValuesRejected(t *testing.T) {
	for _, body := range []string{
		`{"p":{"$type":"Point","_value":"POINT (NaN 1)"}}`,
		`{"p":{"$type":"Vector","_value":{"coordinatesType":"FLOAT64","coordinates":["NaN"]}}}`,
		`{"p":{"$type":"Vector","_value":{"coordinatesType":"FLOAT64","coordinates":["Inf"]}}}`,
	} {
		if _, err := decodeParameters(json.RawMessage(body), formatTyped, 2); err == nil {
			t.Errorf("%s: expected an error", body)
		}
	}
}

func TestCrossOriginWritesRefused(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h := NewHandler(st, auth.New(testUser, testPass), "neo4j", 5*time.Minute)
	h.SetCORSOrigins([]string{"none"})
	srv := httptest.NewServer(h.Routes())
	t.Cleanup(srv.Close)
	post := func(origin, ctype string) int {
		req, _ := http.NewRequest("POST", srv.URL+"/db/neo4j/query/v2", strings.NewReader(`{"statement":"RETURN 1"}`))
		req.SetBasicAuth(testUser, testPass)
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := post("", "application/json"); got != http.StatusAccepted {
		t.Errorf("no Origin: %d", got)
	}
	if got := post("", ""); got != http.StatusUnsupportedMediaType {
		t.Errorf("no Content-Type: %d", got)
	}
	if got := post("http://evil.example", "application/json"); got != http.StatusForbidden {
		t.Errorf("foreign Origin: %d", got)
	}
	if got := post(srv.URL, "application/json"); got != http.StatusAccepted {
		t.Errorf("same Origin: %d", got)
	}
}
