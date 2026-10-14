package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/LackOfMorals/graingraph/internal/server/store"
)

// errorBody is one entry of a client-facing error document.
type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// errorDoc is the client-facing error document:
// {"errors": [{"code": ..., "message": ...}]}.
type errorDoc struct {
	Errors []errorBody `json:"errors"`
}

func docOf(ae *apiError) errorDoc {
	return errorDoc{Errors: []errorBody{{Code: ae.Code, Message: ae.Message}}}
}

// writeError responds with status and a Neo4j-style error document.
func writeError(w http.ResponseWriter, status int, ae *apiError) {
	writeErrorAs(w, "application/json", status, ae)
}

// writeErrorAs is writeError with an explicit content type: Neo4j
// answers failures in the negotiated Typed JSON type.
func writeErrorAs(w http.ResponseWriter, contentType string, status int, ae *apiError) {
	noteError(w, ae)
	if isTOON(contentType) {
		writeToon(w, status, toonErrors(docOf(ae).Errors))
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(docOf(ae))
}

// writeFailure answers a failed request with the error's own status.
func writeFailure(w http.ResponseWriter, ae *apiError) {
	writeError(w, ae.Status, ae)
}

// writeAccepted responds 202 with a JSON body in the negotiated content
// type.
func writeAccepted(w http.ResponseWriter, contentType string, body any) {
	if isTOON(contentType) {
		if res, ok := toonOf(body); ok {
			writeToon(w, http.StatusAccepted, res.String())
			return
		}
	}
	// Marshal first: a value json cannot encode (NaN, Inf) must not leave
	// the client with a 202 and a truncated body.
	data, err := json.Marshal(body)
	if err != nil {
		writeError(w, http.StatusInternalServerError,
			serverError(CodeGeneralError, "encode response: %v", err))
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write(append(data, '\n'))
}

// writeRunFailure reports a failed statement. A failure during execution
// answers 202 with the error next to an empty result, as Neo4j does;
// every other failure answers the error's own status.
func writeRunFailure(w http.ResponseWriter, contentType string, ae *apiError) {
	// TOON is for agents that read the status line first: every failure
	// is an error status there, even one raised while producing rows.
	if !ae.Runtime || isTOON(contentType) {
		writeErrorAs(w, contentType, ae.Status, ae)
		return
	}
	noteError(w, ae)
	writeAccepted(w, contentType, map[string]any{
		"data":   &dataBlock{Fields: nonNil(ae.Fields), Values: []any{}},
		"errors": docOf(ae).Errors,
	})
}

// dataBlock is the "data" object of a successful result.
type dataBlock struct {
	Fields []string `json:"fields"`
	Values []any    `json:"values"`
}

// txInfo is the "transaction" object of a transaction-open response.
type txInfo struct {
	ID      string `json:"id"`
	Expires string `json:"expires,omitempty"`
}

// resultResponse is the 202 response body of a query, transaction-open
// or transaction-run request.
type resultResponse struct {
	Data                 *dataBlock     `json:"data,omitempty"`
	Counters             *countersBlock `json:"counters,omitempty"`
	Bookmarks            []string       `json:"bookmarks,omitempty"`
	QueryType            string         `json:"queryType,omitempty"`
	ResultAvailableAfter float64        `json:"resultAvailableAfter,omitempty"`
	ResultConsumedAfter  float64        `json:"resultConsumedAfter,omitempty"`
	Transaction          *txInfo        `json:"transaction,omitempty"`
}

// countersBlock mirrors the graingraph mutation counters; system counters
// are always zero because graingraph is a single embedded database.
type countersBlock struct {
	ContainsUpdates bool `json:"containsUpdates"`

	NodesCreated         int `json:"nodesCreated"`
	NodesDeleted         int `json:"nodesDeleted"`
	PropertiesSet        int `json:"propertiesSet"`
	RelationshipsCreated int `json:"relationshipsCreated"`
	RelationshipsDeleted int `json:"relationshipsDeleted"`
	LabelsAdded          int `json:"labelsAdded"`
	LabelsRemoved        int `json:"labelsRemoved"`
	IndexesAdded         int `json:"indexesAdded"`
	IndexesRemoved       int `json:"indexesRemoved"`
	ConstraintsAdded     int `json:"constraintsAdded"`
	ConstraintsRemoved   int `json:"constraintsRemoved"`

	ContainsSystemUpdates bool `json:"containsSystemUpdates"`
	SystemUpdates         int  `json:"systemUpdates"`
}

func countersBlockOf(c *store.Counters) *countersBlock {
	if c == nil {
		return nil
	}
	return &countersBlock{
		ContainsUpdates:      c.ContainsUpdates,
		NodesCreated:         c.NodesCreated,
		NodesDeleted:         c.NodesDeleted,
		PropertiesSet:        c.PropertiesSet,
		RelationshipsCreated: c.RelationshipsCreated,
		RelationshipsDeleted: c.RelationshipsDeleted,
		LabelsAdded:          c.LabelsAdded,
		LabelsRemoved:        c.LabelsRemoved,
		IndexesAdded:         c.IndexesAdded,
		IndexesRemoved:       c.IndexesRemoved,
		ConstraintsAdded:     c.ConstraintsAdded,
		ConstraintsRemoved:   c.ConstraintsRemoved,
	}
}

// encodeValues converts raw graingraph values into the response representation.
func encodeValues(vals [][]any, rt responseType) ([]any, error) {
	out := make([]any, len(vals))
	for i, row := range vals {
		enc, err := encodeRow(row, rt)
		if err != nil {
			return nil, err
		}
		out[i] = enc
	}
	return out, nil
}

func encodeRow(row []any, rt responseType) (any, error) {
	enc := make([]any, len(row))
	for i, v := range row {
		var (
			x   any
			err error
		)
		switch rt.format {
		case formatTyped:
			x, err = encodeTyped(v, rt.typedVer)
		case formatTOON:
			x, err = encodeTOONValue(v)
		default:
			x, err = encodePlain(v)
		}
		if err != nil {
			return nil, &encodeError{err: fmt.Errorf("encode result value: %w", err)}
		}
		enc[i] = x
	}
	return enc, nil
}
