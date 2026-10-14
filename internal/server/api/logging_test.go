package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/graingraph/graingraph/internal/server/auth"
	"github.com/graingraph/graingraph/internal/server/store"
)

// captureLogs routes slog output to a JSON buffer for the test's duration
// and returns a function that decodes the request lines logged so far.
func captureLogs(t *testing.T) func() []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return func() []map[string]any {
		var out []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			if line == "" {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				t.Fatalf("log line is not JSON: %q", line)
			}
			if m["msg"] == "request" {
				out = append(out, m)
			}
		}
		return out
	}
}

func newLoggingServer(t *testing.T, logStatements bool) *httptest.Server {
	t.Helper()
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h := NewHandler(st, auth.New(testUser, testPass), "neo4j", time.Minute)
	h.SetLogStatements(logStatements)
	srv := httptest.NewServer(h.Routes())
	t.Cleanup(srv.Close)
	return srv
}

func TestRequestLogFields(t *testing.T) {
	lines := captureLogs(t)
	srv := newLoggingServer(t, true)

	_, _, hdr := do(t, "POST", srv.URL+"/db/neo4j/query/v2", "", "application/json",
		`{"statement":"UNWIND [1,2,3] AS x RETURN x","parameters":{"secret":"hunter2"}}`, testUser, testPass)

	got := lines()
	if len(got) != 1 {
		t.Fatalf("got %d request log lines, want 1: %v", len(got), got)
	}
	l := got[0]
	if l["level"] != "INFO" || l["status"] != float64(202) {
		t.Errorf("level/status = %v/%v", l["level"], l["status"])
	}
	if l["user"] != testUser || l["method"] != "POST" || l["rows"] != float64(3) {
		t.Errorf("user/method/rows = %v/%v/%v", l["user"], l["method"], l["rows"])
	}
	if l["statement"] != "UNWIND [1,2,3] AS x RETURN x" || l["query_type"] != "r" {
		t.Errorf("statement/query_type = %v/%v", l["statement"], l["query_type"])
	}
	if id, _ := l["request_id"].(string); id == "" || id != hdr.Get("X-Request-Id") {
		t.Errorf("request_id %v does not match response header %q", l["request_id"], hdr.Get("X-Request-Id"))
	}
	if l["bytes"].(float64) <= 0 {
		t.Errorf("bytes = %v", l["bytes"])
	}
	if strings.Contains(mustJSON(t, l), "hunter2") || strings.Contains(mustJSON(t, l), testPass) {
		t.Errorf("log line leaks a parameter or password: %v", l)
	}
}

func TestFailedStatementIsLoggedAsWarning(t *testing.T) {
	lines := captureLogs(t)
	srv := newLoggingServer(t, true)

	code, _, _ := do(t, "POST", srv.URL+"/db/neo4j/query/v2", "", "application/json",
		`{"statement":"RETURN RETURN"}`, testUser, testPass)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	l := lines()[0]
	if l["level"] != "WARN" || l["error_code"] == nil {
		t.Errorf("level/error_code = %v/%v, want WARN with an error code", l["level"], l["error_code"])
	}
}

func TestFailedAuthLogsAttemptedUserNotPassword(t *testing.T) {
	lines := captureLogs(t)
	srv := newLoggingServer(t, true)

	do(t, "POST", srv.URL+"/db/neo4j/query/v2", "", "application/json", `{"statement":"RETURN 1"}`, "mallory", "guess")
	l := lines()[0]
	if l["level"] != "WARN" || l["status"] != float64(401) || l["user"] != "mallory" {
		t.Errorf("level/status/user = %v/%v/%v", l["level"], l["status"], l["user"])
	}
	if strings.Contains(mustJSON(t, l), "guess") {
		t.Errorf("log line leaks the password: %v", l)
	}
}

func TestStatementLoggingCanBeDisabled(t *testing.T) {
	lines := captureLogs(t)
	srv := newLoggingServer(t, false)

	do(t, "POST", srv.URL+"/db/neo4j/query/v2", "", "application/json", `{"statement":"RETURN 1"}`, testUser, testPass)
	if _, ok := lines()[0]["statement"]; ok {
		t.Error("statement logged although logging of statements is disabled")
	}
}

func TestLongStatementIsTruncated(t *testing.T) {
	lines := captureLogs(t)
	srv := newLoggingServer(t, true)

	long := "RETURN '" + strings.Repeat("é", 5000) + "'"
	body, _ := json.Marshal(map[string]string{"statement": long})
	do(t, "POST", srv.URL+"/db/neo4j/query/v2", "", "application/json", string(body), testUser, testPass)
	stmt, _ := lines()[0]["statement"].(string)
	if len(stmt) > maxLoggedStatement+len("…") || !strings.HasSuffix(stmt, "…") {
		t.Errorf("statement length %d not truncated", len(stmt))
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
