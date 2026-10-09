package interp

import "fmt"

// Error is a run-time error with its openCypher TCK class and detail code.
type Error struct {
	Class string // SyntaxError, TypeError, ArgumentError, ConstraintVerificationFailed, EntityNotFound, ...
	Code  string
	Msg   string

	// Schema identifies the constraint or index a ConstraintValidationFailed
	// error came from; nil for every other error.
	Schema *SchemaRef
}

// SchemaRef names the schema object behind a constraint violation.
type SchemaRef struct {
	Name       string
	Kind       string   // UNIQUENESS, KEY, EXISTENCE, TYPE or VECTOR
	Entity     string   // NODE or RELATIONSHIP
	Target     string   // the label or relationship type
	Properties []string // the constrained properties
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s (%s): %s", e.Class, e.Code, e.Msg)
}

func errorf(class, code, format string, args ...any) *Error {
	return &Error{Class: class, Code: code, Msg: fmt.Sprintf(format, args...)}
}

// typeErr is a TypeError / InvalidArgumentType.
func typeErr(format string, args ...any) *Error {
	return errorf("TypeError", "InvalidArgumentType", format, args...)
}

// argErr is an ArgumentError / InvalidArgumentValue.
func argErr(format string, args ...any) *Error {
	return errorf("ArgumentError", "InvalidArgumentValue", format, args...)
}

// unsupported marks a construct the interpreter does not implement.
func unsupported(format string, args ...any) *Error {
	return errorf("SyntaxError", "Unsupported", format, args...)
}
