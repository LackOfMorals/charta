package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/LackOfMorals/graingraph"
	"github.com/LackOfMorals/graingraph/cypher/analyze"
	"github.com/LackOfMorals/graingraph/cypher/interp"
	"github.com/LackOfMorals/graingraph/cypher/syntax"

	"github.com/LackOfMorals/graingraph/internal/server/store"
)

// Neo4j error codes used by the server. These match the codes a Neo4j
// server returns for the same conditions.
const (
	CodeRequestInvalid   = "Neo.ClientError.Request.Invalid"
	CodeUnauthorized     = "Neo.ClientError.Security.Unauthorized"
	CodeForbidden        = "Neo.ClientError.General.ForbiddenOnReadOnlyDatabase"
	CodeSyntaxError      = "Neo.ClientError.Statement.SyntaxError"
	CodeParameterMissing = "Neo.ClientError.Statement.ParameterMissing"
	CodeConstraint       = "Neo.ClientError.Schema.ConstraintValidationFailed"
	CodeDatabaseNotFound = "Neo.ClientError.Database.DatabaseNotFound"
	CodeTxTimedOut       = "Neo.ClientError.Transaction.TransactionTimedOut"
	CodeDBUnavailable    = "Neo.TransientError.General.DatabaseUnavailable"
	CodeGeneralError     = "Neo.DatabaseError.General.UnknownError"
)

// statementClasses are the openCypher error classes Neo4j reports under
// Neo.ClientError.Statement.
var statementClasses = map[string]bool{
	"SyntaxError": true, "SemanticError": true, "TypeError": true,
	"ArgumentError": true, "ArithmeticError": true, "EntityNotFound": true,
	"ParameterMissing": true, "ParameterNotFound": true,
}

// runtimeClasses are errors raised while a result is being produced. As
// with Neo4j, they answer 202 and carry the error in the body next to the
// (empty) data; every other statement failure answers 400.
var runtimeClasses = map[string]bool{
	"TypeError": true, "ArgumentError": true, "ArithmeticError": true,
	"EntityNotFound": true, "ProcedureError": true,
}

// apiError is an error destined for a client response body:
// {"errors": [{"code": ..., "message": ...}]}.
type apiError struct {
	Code    string
	Message string
	// Status is the HTTP status that answers the failure.
	Status int
	// Runtime marks a failure during execution, answered 202 with the
	// error in the body instead of Status.
	Runtime bool
	// Fields are the result columns, when known, for a Runtime failure.
	Fields []string
}

func (e *apiError) Error() string { return e.Message }

// clientError is a 400 caused by the request or statement.
func clientError(code, format string, args ...any) *apiError {
	return &apiError{Code: code, Message: fmt.Sprintf(format, args...), Status: http.StatusBadRequest}
}

// serverError is a 500 caused by the server.
func serverError(code, format string, args ...any) *apiError {
	return &apiError{Code: code, Message: fmt.Sprintf(format, args...), Status: http.StatusInternalServerError}
}

// withStatus returns the error with its HTTP status replaced.
func (e *apiError) withStatus(status int) *apiError {
	e.Status = status
	return e
}

// interpCode maps an openCypher error class to a Neo4j error code.
func interpCode(class string) string {
	switch {
	case class == "ConstraintVerificationFailed" || class == "ConstraintValidationFailed":
		return CodeConstraint
	case class == "ProcedureError":
		return "Neo.ClientError.Procedure.ProcedureCallFailed"
	case statementClasses[class]:
		return "Neo.ClientError.Statement." + class
	}
	return "Neo.ClientError.Statement.ExternalResourceFailed"
}

// mapGraingraphError converts a graingraph (or store) execution error into a
// client-facing Neo4j-style error.
func mapGraingraphError(err error) *apiError {
	var txOpen *store.TransactionOpenError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return clientError(CodeTxTimedOut, "The query or transaction exceeded its time limit.")
	case errors.Is(err, context.Canceled):
		return serverError(CodeGeneralError, "The request was canceled before it completed.")
	case errors.Is(err, graingraph.ErrReadOnly):
		return clientError(CodeForbidden, "The database is read-only: write queries are not allowed.")
	case errors.As(err, &txOpen):
		return clientError(CodeDBUnavailable, "Transaction %s is open and holds the database; commit or roll it back (DELETE .../tx/%s) before running other queries.", txOpen.ID, txOpen.ID).
			withStatus(http.StatusServiceUnavailable)
	case errors.Is(err, store.ErrTransactionNotFound), errors.Is(err, store.ErrTransactionClosed):
		return clientError(CodeRequestInvalid, "The transaction does not exist or has already finished.").
			withStatus(http.StatusNotFound)
	}

	var cv *graingraph.ErrConstraintViolation
	if errors.As(err, &cv) {
		return clientError(CodeConstraint, "%s", cv.Message)
	}

	var sp *syntax.SyntaxError
	if errors.As(err, &sp) {
		return clientError(CodeSyntaxError, "%s", sp.Error())
	}

	var ae *analyze.Error
	if errors.As(err, &ae) {
		return clientError(interpCode(ae.Class), "%s", ae.Msg)
	}

	var ie *interp.Error
	if errors.As(err, &ie) {
		out := clientError(interpCode(ie.Class), "%s", ie.Msg)
		out.Runtime = runtimeClasses[ie.Class]
		return out
	}

	var missing *graingraph.ErrMissingParameter
	if errors.As(err, &missing) {
		return clientError(CodeParameterMissing, "Expected parameter(s): %s", missing.Name)
	}

	var unsupported *graingraph.ErrUnsupportedCypher
	if errors.As(err, &unsupported) {
		return clientError(CodeSyntaxError, "Unsupported Cypher construct %q: %s", unsupported.Clause, unsupported.Detail)
	}

	// Anything unclassified may carry internal detail (file paths, driver
	// messages); log it and give the client a generic message.
	slog.Error("unclassified error running statement", "error", err)
	return serverError(CodeGeneralError, "An internal error occurred while running the statement; see the server log for details.")
}
