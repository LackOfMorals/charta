package syntax

import (
	"strconv"
	"strings"
)

// SyntaxError is a positioned lexing or parsing error.
type SyntaxError struct {
	// Pos is where the error was detected.
	Pos Pos
	// Found describes the offending input (e.g. a token's text); may be empty.
	Found string
	// Expected lists what would have been accepted; may be empty.
	Expected []string
	// Msg is the primary description of the problem.
	Msg string
}

// Error formats the error as
// `syntax error at line L, column C: msg (found "X", expected A, B)`.
func (e *SyntaxError) Error() string {
	var b strings.Builder
	b.WriteString("syntax error at line ")
	b.WriteString(strconv.Itoa(e.Pos.Line))
	b.WriteString(", column ")
	b.WriteString(strconv.Itoa(e.Pos.Col))
	b.WriteString(": ")
	b.WriteString(e.Msg)
	if e.Found != "" || len(e.Expected) > 0 {
		b.WriteString(" (")
		if e.Found != "" {
			b.WriteString("found ")
			b.WriteString(strconv.Quote(e.Found))
			if len(e.Expected) > 0 {
				b.WriteString(", ")
			}
		}
		if len(e.Expected) > 0 {
			b.WriteString("expected ")
			b.WriteString(strings.Join(e.Expected, ", "))
		}
		b.WriteString(")")
	}
	return b.String()
}
