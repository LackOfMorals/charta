package api

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/LackOfMorals/graingraph/internal/server/store"
)

// query implements POST /db/{databaseName}/query/v2.
//
// Per the Query API contract the response is 202 regardless of whether
// the statement executed; a failed statement carries its error in the
// response body (or as a terminal Error event when streaming).
func (h *Handler) query(w http.ResponseWriter, r *http.Request) {
	rt, q, ok := h.parseStatementRequest(w, r)
	if !ok {
		return
	}

	qt := queryTypeOf(q.Statement)
	noteStatement(w, q.Statement, qt)
	t0 := time.Now()
	var (
		outcome *store.Outcome
		runErr  error
	)
	if strings.EqualFold(r.PathValue("databaseName"), systemDatabase) {
		qt = "s"
		var ae *apiError
		if outcome, ae = h.systemOutcome(q.Statement, r.Host, basicUser(r)); ae != nil {
			h.reject(w, r, ae)
			return
		}
	} else {
		outcome, runErr = h.store.Run(r.Context(), q.Statement, q.Parameters)
	}
	availableAfter := elapsedMs(t0)
	if runErr != nil {
		h.writeRunError(w, rt, failure(q.Statement, runErr))
		return
	}
	h.writeResult(w, rt, q, resultMeta{
		queryType:      qt,
		bookmark:       h.store.NewBookmark(),
		availableAfter: availableAfter,
		consumedAfter:  elapsedMs(t0),
	}, outcome)
}

// txOpen implements POST /db/{databaseName}/query/v2/tx.
func (h *Handler) txOpen(w http.ResponseWriter, r *http.Request) {
	if h.isSystem(r) {
		h.systemTx(w, r, "open")
		return
	}
	rt, ok := h.negotiate(w, r)
	if !ok {
		return
	}
	q, ok := h.decodeBody(w, r)
	if !ok {
		return
	}

	id, err := store.NewTxID()
	if err != nil {
		writeFailure(w, serverError(CodeGeneralError, "%v", err))
		return
	}
	timeout := h.txTimeout
	if q.MaxExecutionTime != nil {
		timeout = *q.MaxExecutionTime
	}
	tx, err := h.store.Begin(r.Context(), id, timeout, q.TxMetadata)
	if err != nil {
		writeFailure(w, mapGraingraphError(err))
		return
	}
	note(w, slog.String("tx_id", id))
	info := txInfoOf(tx)

	if q.Statement == "" {
		// An open without a statement: just the transaction info.
		if rt.jsonl {
			sw, err := newStreamWriter(w, rt, nil)
			if err != nil {
				return
			}
			defer sw.close()
			_ = sw.summary(&summaryBody{Transaction: info})
			return
		}
		writeAccepted(w, rt.contentType(), &resultResponse{Transaction: info})
		return
	}

	qt := queryTypeOf(q.Statement)
	noteStatement(w, q.Statement, qt)
	t0 := time.Now()
	outcome, runErr := tx.Run(r.Context(), q.Statement, q.Parameters)
	availableAfter := elapsedMs(t0)
	if runErr != nil {
		h.failInTx(w, rt, id, failure(q.Statement, runErr))
		return
	}
	h.writeResult(w, rt, q, resultMeta{
		queryType:      qt,
		availableAfter: availableAfter,
		consumedAfter:  elapsedMs(t0),
		tx:             info,
	}, outcome)
}

// txRun implements POST /db/{databaseName}/query/v2/tx/{transactionID}.
func (h *Handler) txRun(w http.ResponseWriter, r *http.Request) {
	if h.isSystem(r) {
		h.systemTx(w, r, "run")
		return
	}
	id := r.PathValue("transactionID")
	note(w, slog.String("tx_id", id))
	tx, ok := h.txOrNotFound(w, r, id)
	if !ok {
		return
	}
	rt, q, ok := h.parseStatementRequest(w, r)
	if !ok {
		return
	}

	qt := queryTypeOf(q.Statement)
	noteStatement(w, q.Statement, qt)
	t0 := time.Now()
	outcome, runErr := tx.Run(r.Context(), q.Statement, q.Parameters)
	availableAfter := elapsedMs(t0)
	if runErr != nil {
		h.failInTx(w, rt, id, failure(q.Statement, runErr))
		return
	}
	h.writeResult(w, rt, q, resultMeta{
		queryType:      qt,
		tx:             txInfoOf(tx),
		availableAfter: availableAfter,
		consumedAfter:  elapsedMs(t0),
	}, outcome)
}

// txCommit implements POST /db/{databaseName}/query/v2/tx/{id}/commit.
// The body may carry a final statement that runs before committing.
func (h *Handler) txCommit(w http.ResponseWriter, r *http.Request) {
	if h.isSystem(r) {
		h.systemTx(w, r, "commit")
		return
	}
	id := r.PathValue("transactionID")
	note(w, slog.String("tx_id", id))
	tx, ok := h.txOrNotFound(w, r, id)
	if !ok {
		return
	}
	rt, ok := h.negotiate(w, r)
	if !ok {
		return
	}
	q, ok := h.decodeBody(w, r)
	if !ok {
		return
	}

	var (
		outcome *store.Outcome
		qt      string
		t0      = time.Now()
		avail   float64
	)
	if q.Statement != "" {
		qt = queryTypeOf(q.Statement)
		noteStatement(w, q.Statement, qt)
		var runErr error
		outcome, runErr = tx.Run(r.Context(), q.Statement, q.Parameters)
		avail = elapsedMs(t0)
		if runErr != nil {
			h.failInTx(w, rt, id, failure(q.Statement, runErr))
			return
		}
	}
	bookmark, err := h.store.Commit(id)
	if err != nil {
		h.writeRunError(w, rt, mapGraingraphError(err))
		return
	}

	if outcome != nil {
		// The final statement's result is returned with the bookmark.
		h.writeResult(w, rt, q, resultMeta{
			queryType: qt, bookmark: bookmark,
			availableAfter: avail, consumedAfter: elapsedMs(t0),
		}, outcome)
		return
	}
	if rt.jsonl {
		sw, err := newStreamWriter(w, rt, nil)
		if err != nil {
			return
		}
		defer sw.close()
		_ = sw.summary(&summaryBody{Bookmarks: []string{bookmark}})
		return
	}
	writeAccepted(w, rt.contentType(), map[string]any{"bookmarks": []string{bookmark}})
}

// txRollback implements DELETE /db/{databaseName}/query/v2/tx/{id}.
func (h *Handler) txRollback(w http.ResponseWriter, r *http.Request) {
	if h.isSystem(r) {
		h.systemTx(w, r, "rollback")
		return
	}
	id := r.PathValue("transactionID")
	note(w, slog.String("tx_id", id))
	if _, ok := h.txOrNotFound(w, r, id); !ok {
		return
	}
	if err := h.store.Rollback(id); err != nil {
		h.reject(w, r, mapGraingraphError(err))
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

// parseStatementRequest runs the steps shared by every endpoint that
// executes a statement: content negotiation, body decoding and the
// "statement is required" check. On failure it has answered the request.
func (h *Handler) parseStatementRequest(w http.ResponseWriter, r *http.Request) (responseType, *QueryRequest, bool) {
	rt, ok := h.negotiate(w, r)
	if !ok {
		return rt, nil, false
	}
	q, ok := h.decodeBody(w, r)
	if !ok {
		return rt, nil, false
	}
	if q.Statement == "" {
		h.reject(w, r, clientError(CodeRequestInvalid, "statement cannot be null or empty"))
		return rt, nil, false
	}
	return rt, q, true
}

// resultMeta is the per-request metadata reported alongside a result.
type resultMeta struct {
	queryType      string
	bookmark       string // empty inside a transaction: bookmarks arrive on commit
	availableAfter float64
	consumedAfter  float64
	tx             *txInfo // nil outside transaction-open responses
}

// writeResult writes a successful statement result, as one JSON document
// or as a JSONL stream depending on the negotiated type.
func (h *Handler) writeResult(w http.ResponseWriter, rt responseType, q *QueryRequest, m resultMeta, outcome *store.Outcome) {
	note(w, slog.Int("rows", len(outcome.Values)))
	if rt.jsonl {
		sw, err := newStreamWriter(w, rt, outcome.Fields)
		if err != nil {
			return
		}
		defer sw.close()
		for _, row := range outcome.Values {
			if err := sw.record(row); err != nil {
				return
			}
		}
		summary := &summaryBody{
			Bookmarks:            bookmarksOf(m.bookmark),
			QueryType:            m.queryType,
			ResultAvailableAfter: m.availableAfter,
			ResultConsumedAfter:  m.consumedAfter,
			Transaction:          m.tx,
		}
		if q.IncludeCounters {
			summary.Counters = countersBlockOf(outcome.Counters)
		}
		_ = sw.summary(summary)
		return
	}

	values, err := encodeValues(outcome.Values, rt)
	if err != nil {
		writeFailure(w, serverError(CodeGeneralError, "encode result: %v", err))
		return
	}
	resp := &resultResponse{
		Data:                 &dataBlock{Fields: nonNil(outcome.Fields), Values: values},
		Bookmarks:            bookmarksOf(m.bookmark),
		QueryType:            m.queryType,
		ResultAvailableAfter: m.availableAfter,
		ResultConsumedAfter:  m.consumedAfter,
		Transaction:          m.tx,
	}
	if q.IncludeCounters || rt.format == formatTOON {
		resp.Counters = countersBlockOf(outcome.Counters)
	}
	writeAccepted(w, rt.contentType(), resp)
}

// writeRunError reports a failed statement in the negotiated format.
func (h *Handler) writeRunError(w http.ResponseWriter, rt responseType, ae *apiError) {
	if !rt.jsonl {
		writeRunFailure(w, rt.contentType(), ae)
		return
	}
	if ae.Runtime {
		// Mirror the JSON form: 202, a header with the fields, then the error.
		h.streamFailure(w, rt, ae)
		return
	}
	streamErrorOnly(w, rt, ae)
}

// failInTx reports a failed statement inside a transaction. As in Neo4j,
// a failed statement destroys the transaction.
func (h *Handler) failInTx(w http.ResponseWriter, rt responseType, id string, ae *apiError) {
	_ = h.store.Rollback(id)
	h.writeRunError(w, rt, ae)
}

// streamFailure starts a JSONL stream and terminates it with the Error
// event, for failures that happen while producing a result.
func (h *Handler) streamFailure(w http.ResponseWriter, rt responseType, ae *apiError) {
	sw, err := newStreamWriter(w, rt, ae.Fields)
	if err != nil {
		return
	}
	defer sw.close()
	_ = sw.error(ae)
}

// nonNil guarantees JSON emits [] instead of null for empty fields.
func nonNil(fields []string) []string {
	if fields == nil {
		return []string{}
	}
	return fields
}

// bookmarksOf wraps a bookmark for a response, or returns nil for none.
func bookmarksOf(b string) []string {
	if b == "" {
		return nil
	}
	return []string{b}
}

// txInfoOf describes a transaction for responses: its ID and expiry.
func txInfoOf(tx *store.OpenTx) *txInfo {
	info := &txInfo{ID: tx.ID}
	if exp := tx.ExpiresAt(); !exp.IsZero() {
		info.Expires = exp.UTC().Format(time.RFC3339)
	}
	return info
}

// failure maps a failed run to a client error. graingraph executes a
// statement eagerly, so a runtime failure leaves no result columns; they
// are recovered from the statement, as Neo4j reports them even then.
func failure(statement string, err error) *apiError {
	ae := mapGraingraphError(err)
	if ae.Runtime {
		ae.Fields = columnsOf(statement)
	}
	return ae
}
