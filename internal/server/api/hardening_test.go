package api

import (
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOversizedBodyRejected(t *testing.T) {
	body := `{"statement":"` + strings.Repeat("x", maxBodyBytes) + `"}`

	// Through the handler: 413, not a truncated-JSON 400.
	h := &Handler{}
	req := httptest.NewRequest("POST", "/db/neo4j/query/v2", strings.NewReader(body))
	rec := httptest.NewRecorder()
	if _, ok := h.decodeBody(rec, req); ok {
		t.Fatal("decodeBody accepted an oversized body")
	}
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}

	// Directly, without MaxBytesReader in front.
	if _, err := decodeQueryRequest(strings.NewReader(body), "application/json"); !errors.Is(err, errBodyTooLarge) {
		t.Fatalf("decodeQueryRequest err = %v, want errBodyTooLarge", err)
	}
}

func TestMaxExecutionTime(t *testing.T) {
	for _, tc := range []struct {
		body string
		want time.Duration // 0: expect nil (server default)
	}{
		{`{"maxExecutionTime":0}`, 0},
		{`{}`, 0},
		{`{"maxExecutionTime":30}`, 30 * time.Second},
		{`{"maxExecutionTime":0.5}`, 500 * time.Millisecond},
		{`{"maxExecutionTime":86400}`, maxClientTxTimeout},
		{`{"maxExecutionTime":1e300}`, maxClientTxTimeout},
	} {
		q, err := decodeQueryRequest(strings.NewReader(tc.body), "application/json")
		if err != nil {
			t.Fatalf("%s: %v", tc.body, err)
		}
		switch {
		case tc.want == 0 && q.MaxExecutionTime != nil:
			t.Fatalf("%s: MaxExecutionTime = %v, want nil", tc.body, *q.MaxExecutionTime)
		case tc.want != 0 && (q.MaxExecutionTime == nil || *q.MaxExecutionTime != tc.want):
			t.Fatalf("%s: MaxExecutionTime = %v, want %v", tc.body, q.MaxExecutionTime, tc.want)
		}
	}
}

func TestWriteAcceptedUnencodableValue(t *testing.T) {
	rec := httptest.NewRecorder()
	writeAccepted(rec, "application/json", map[string]any{"x": math.NaN()})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, CodeGeneralError) || !strings.Contains(body, "encode response") {
		t.Fatalf("body = %q, want an encode error document", body)
	}
}

func TestStreamRecordEncodeFailureEmitsErrorEvent(t *testing.T) {
	rec := httptest.NewRecorder()
	sw, err := newStreamWriter(rec, responseType{}, []string{"x"})
	if err != nil {
		t.Fatalf("newStreamWriter: %v", err)
	}
	if err := sw.record([]any{int64(1)}); err != nil {
		t.Fatalf("record(1): %v", err)
	}
	if err := sw.record([]any{math.NaN()}); err == nil {
		t.Fatal("record(NaN) succeeded, want error")
	}
	sw.close()

	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d events, want Header, Record, Error:\n%s", len(lines), rec.Body.String())
	}
	if !strings.Contains(lines[2], `"$event":"Error"`) {
		t.Fatalf("last event = %s, want an Error event", lines[2])
	}
}

// nonFlusher is a ResponseWriter that cannot stream.
type nonFlusher struct {
	http.ResponseWriter
}

func TestStreamWithoutFlusherAnswersWithError(t *testing.T) {
	rec := httptest.NewRecorder()
	if _, err := newStreamWriter(nonFlusher{rec}, responseType{jsonl: true}, nil); err == nil {
		t.Fatal("newStreamWriter succeeded without a Flusher")
	}
	if rec.Body.Len() == 0 {
		t.Fatal("no error body was written")
	}
}
