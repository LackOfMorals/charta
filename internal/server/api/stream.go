package api

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// streamEvent is one line of a JSONL streaming response.
type streamEvent struct {
	Event string `json:"$event"`
	Body  any    `json:"_body"`
}

// streamWriter emits JSONL events to an HTTP response, flushing after
// each event so clients see them as they are produced.
type streamWriter struct {
	rw     http.ResponseWriter // for request-log annotations
	w      *bufio.Writer
	fl     http.Flusher
	rt     responseType
	fields []string
}

// newStreamWriter sets up the response for JSONL streaming and writes
// the initial Header event. fields may be nil (transaction open without
// a statement).
func newStreamWriter(w http.ResponseWriter, rt responseType, fields []string) (*streamWriter, error) {
	fl, ok := w.(http.Flusher)
	if !ok {
		err := fmt.Errorf("streaming requires a flushable response writer")
		writeFailure(w, serverError(CodeGeneralError, "%v", err))
		return nil, err
	}
	w.Header().Set("Content-Type", rt.contentType())
	w.WriteHeader(http.StatusAccepted)

	buf := bufio.NewWriterSize(w, 64*1024)
	sw := &streamWriter{
		rw:     w,
		w:      buf,
		fl:     fl,
		rt:     rt,
		fields: fields,
	}
	header := map[string]any{}
	if fields != nil {
		header["fields"] = fields
	}
	return sw, sw.event("Header", header)
}

// record writes one Record event carrying a single row of values.
// A row that cannot be encoded ends the stream with an Error event, since
// the 202 status has already been sent.
func (sw *streamWriter) record(row []any) error {
	enc, err := encodeRow(row, sw.rt)
	if err == nil {
		err = sw.event("Record", enc)
	}
	var ee *encodeError
	if errors.As(err, &ee) {
		_ = sw.error(serverError(CodeGeneralError, "encode result: %v", err))
	}
	return err
}

// encodeError marks a failure to marshal an event, as opposed to a
// failure to write it to the client.
type encodeError struct{ err error }

func (e *encodeError) Error() string { return e.err.Error() }
func (e *encodeError) Unwrap() error { return e.err }

// summary writes the final Summary event.
func (sw *streamWriter) summary(body any) error {
	return sw.event("Summary", body)
}

// error writes a terminal Error event: [{code, message}].
func (sw *streamWriter) error(ae *apiError) error {
	noteError(sw.rw, ae)
	return sw.event("Error", docOf(ae).Errors)
}

// streamErrorOnly answers a statement that failed before producing any
// result: the error's own status and a single Error event.
func streamErrorOnly(w http.ResponseWriter, rt responseType, ae *apiError) {
	noteError(w, ae)
	w.Header().Set("Content-Type", rt.contentType())
	w.WriteHeader(ae.Status)
	data, _ := json.Marshal(streamEvent{Event: "Error", Body: docOf(ae).Errors})
	_, _ = w.Write(append(data, '\n'))
}

// close flushes any buffered bytes.
func (sw *streamWriter) close() {
	_ = sw.w.Flush()
	sw.fl.Flush()
}

func (sw *streamWriter) event(name string, body any) error {
	data, err := json.Marshal(streamEvent{Event: name, Body: body})
	if err != nil {
		return &encodeError{err: err}
	}
	if _, err := sw.w.Write(append(data, '\n')); err != nil {
		return err
	}
	sw.fl.Flush()
	return nil
}

// summaryBody is the _body of a Summary event.
type summaryBody struct {
	Bookmarks            []string `json:"bookmarks,omitempty"`
	QueryType            string   `json:"queryType,omitempty"`
	ResultAvailableAfter float64  `json:"resultAvailableAfter,omitempty"`
	ResultConsumedAfter  float64  `json:"resultConsumedAfter,omitempty"`
	Counters             any      `json:"counters,omitempty"`
	Transaction          *txInfo  `json:"transaction,omitempty"`
}

// resultAvailableAfterMs renders elapsed time as whole milliseconds,
// the unit used by the Query API.
func elapsedMs(t0 time.Time) float64 {
	ms := float64(time.Since(t0).Nanoseconds()) / 1e6
	if ms < 1 {
		ms = 1
	}
	return float64(int64(ms))
}
