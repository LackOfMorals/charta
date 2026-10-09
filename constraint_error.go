package graphlite

import (
	"fmt"
	"strings"
)

// ErrConstraintViolation is returned when a write breaks a schema constraint
// (uniqueness, node key, property existence, property type) or the dimension of
// a vector index. The statement is rolled back. Use errors.As to inspect it:
//
//	var cv *graphlite.ErrConstraintViolation
//	if errors.As(err, &cv) && cv.Kind == "UNIQUENESS" {
//	    // e.g. report that the email is taken
//	}
type ErrConstraintViolation struct {
	// Name is the constraint (or vector index) that was violated.
	Name string
	// Kind is UNIQUENESS, KEY, EXISTENCE, TYPE for a constraint, or VECTOR for
	// a vector index.
	Kind string
	// EntityType is "NODE" or "RELATIONSHIP".
	EntityType string
	// Label is the node label or relationship type the constraint covers.
	Label string
	// Properties are the constrained properties.
	Properties []string
	// Message describes the violation.
	Message string

	cause error
}

// Error implements the error interface.
func (e *ErrConstraintViolation) Error() string {
	return fmt.Sprintf("graphlite: constraint `%s` (%s on %s %s(%s)) violated: %s",
		e.Name, e.Kind, strings.ToLower(e.EntityType), e.Label, strings.Join(e.Properties, ", "), e.Message)
}

// Unwrap returns the underlying execution error.
func (e *ErrConstraintViolation) Unwrap() error { return e.cause }
