// Package analyze performs semantic analysis of a parsed Cypher statement: the
// checks that need more than the grammar, such as undefined or conflicting
// variables, aggregation rules and static type errors. It reports the same
// error classes and detail codes as the openCypher TCK.
//
// It depends only on cypher/syntax.
package analyze

import (
	"errors"
	"fmt"

	"github.com/LackOfMorals/graphlite/v2/cypher/syntax"
)

// Error classes, as used by the openCypher TCK.
const (
	ClassSyntax    = "SyntaxError"
	ClassType      = "TypeError"
	ClassProcedure = "ProcedureError"
	ClassParameter = "ParameterMissing"
)

// Error detail codes (openCypher TCK names) reported by Check.
const (
	CodeUndefinedVariable               = "UndefinedVariable"
	CodeVariableAlreadyBound            = "VariableAlreadyBound"
	CodeVariableTypeConflict            = "VariableTypeConflict"
	CodeColumnNameConflict              = "ColumnNameConflict"
	CodeNoExpressionAlias               = "NoExpressionAlias"
	CodeNoVariablesInScope              = "NoVariablesInScope"
	CodeInvalidAggregation              = "InvalidAggregation"
	CodeAmbiguousAggregationExpression  = "AmbiguousAggregationExpression"
	CodeNestedAggregation               = "NestedAggregation"
	CodeNonConstantExpression           = "NonConstantExpression"
	CodeNegativeIntegerArgument         = "NegativeIntegerArgument"
	CodeInvalidArgumentType             = "InvalidArgumentType"
	CodeMissingParameter                = "MissingParameter"
	CodeInvalidNumberOfArguments        = "InvalidNumberOfArguments"
	CodeInvalidArgumentPassingMode      = "InvalidArgumentPassingMode"
	CodeUnknownFunction                 = "UnknownFunction"
	CodeInvalidParameterUse             = "InvalidParameterUse"
	CodeNoSingleRelationshipType        = "NoSingleRelationshipType"
	CodeRequiresDirectedRelationship    = "RequiresDirectedRelationship"
	CodeCreatingVarLength               = "CreatingVarLength"
	CodeRelationshipUniquenessViolation = "RelationshipUniquenessViolation"
	CodeInvalidDelete                   = "InvalidDelete"
	CodeInvalidClauseComposition        = "InvalidClauseComposition"
	CodeDifferentColumnsInUnion         = "DifferentColumnsInUnion"
	CodeProcedureNotFound               = "ProcedureNotFound"
	CodeUnexpectedSyntax                = syntax.CodeUnexpectedSyntax
)

// Error is a compile-time semantic error.
type Error struct {
	Class string
	Code  string
	Pos   syntax.Pos
	Msg   string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s (%s) at line %d, column %d: %s", e.Class, e.Code, e.Pos.Line, e.Pos.Col, e.Msg)
}

// Describe returns the TCK class and detail code of a compile-time error: an
// *Error from Check, or a *syntax.SyntaxError from the parser. ok is false for
// any other error.
func Describe(err error) (class, code string, ok bool) {
	var ae *Error
	if errors.As(err, &ae) {
		return ae.Class, ae.Code, true
	}
	var se *syntax.SyntaxError
	if errors.As(err, &se) {
		return ClassSyntax, se.ErrorCode(), true
	}
	return "", "", false
}
