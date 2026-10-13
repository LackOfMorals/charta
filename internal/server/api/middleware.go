package api

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
	"unicode/utf8"
)

// maxLoggedStatement bounds how much of a Cypher statement is logged.
const maxLoggedStatement = 2000

// requestIDHeader carries the request ID on both request and response.
const requestIDHeader = "X-Request-Id"

// statusRecorder captures the response status and size for logging, and
// the per-request attributes handlers add with note. It also records
// whether the header has been sent, so panic recovery knows if it can
// still answer 500.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
	bytes  int64
	attrs  []slog.Attr
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wrote {
		s.status = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wrote {
		s.status = http.StatusOK
		s.wrote = true
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += int64(n)
	return n, err
}

// Flush forwards to the underlying writer; JSONL streaming needs it.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// note adds attributes to the request's log line. It is a no-op when w
// is not the recorder installed by observe (as in handler unit tests).
func note(w http.ResponseWriter, attrs ...slog.Attr) {
	if rec, ok := w.(*statusRecorder); ok {
		rec.attrs = append(rec.attrs, attrs...)
	}
}

// noteStatement records the statement text and its query type.
func noteStatement(w http.ResponseWriter, statement, queryType string) {
	note(w, slog.String("statement", truncate(statement, maxLoggedStatement)),
		slog.String("query_type", queryType))
}

// noteError records the Neo4j error code of a failed request.
func noteError(w http.ResponseWriter, ae *apiError) {
	if rec, ok := w.(*statusRecorder); ok {
		for _, a := range rec.attrs {
			if a.Key == "error_code" {
				return // already recorded by an earlier layer
			}
		}
	}
	note(w, slog.String("error_code", ae.Code), slog.String("error", ae.Message))
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max] + "…"
}

func newRequestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// observe wraps next with request IDs, panic recovery and one structured
// log line per request. The line never carries credentials or query
// parameters. Statement text is logged only when logStatements is set.
func observe(next http.Handler, logStatements bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := newRequestID()
		w.Header().Set(requestIDHeader, id)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if p := recover(); p != nil {
				if p == http.ErrAbortHandler {
					panic(p)
				}
				slog.Error("panic serving request",
					"request_id", id, "method", r.Method, "path", r.URL.Path,
					"panic", p, "stack", string(debug.Stack()))
				if !rec.wrote {
					ae := serverError(CodeGeneralError, "An internal error occurred.")
					noteError(rec, ae)
					writeError(rec, http.StatusInternalServerError, ae)
				}
			}
			attrs := []slog.Attr{
				slog.String("request_id", id),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Duration("duration", time.Since(start)),
				slog.Int64("bytes", rec.bytes),
				slog.String("remote", r.RemoteAddr),
				slog.String("user_agent", r.UserAgent()),
			}
			if user, _, ok := r.BasicAuth(); ok {
				attrs = append(attrs, slog.String("user", user))
			}
			for _, a := range rec.attrs {
				if !logStatements && a.Key == "statement" {
					continue
				}
				attrs = append(attrs, a)
			}
			slog.LogAttrs(r.Context(), levelFor(rec), "request", attrs...)
		}()
		next.ServeHTTP(rec, r)
	})
}

// levelFor picks the log level: failures are visible without raising
// the noise of routine requests. A failed statement answers 202, so it
// is recognised by its recorded error code, not its status.
func levelFor(rec *statusRecorder) slog.Level {
	switch {
	case rec.status >= 500:
		return slog.LevelError
	case rec.status >= 400:
		return slog.LevelWarn
	}
	for _, a := range rec.attrs {
		if a.Key == "error_code" {
			if strings.HasPrefix(a.Value.String(), "Neo.DatabaseError") {
				return slog.LevelError
			}
			return slog.LevelWarn
		}
	}
	return slog.LevelInfo
}
