package syntax

import (
	"errors"
	"testing"
)

// TestParse_ErrorCodes checks the TCK error detail code carried by syntax errors.
func TestParse_ErrorCodes(t *testing.T) {
	tests := []struct{ src, code string }{
		{"RETURN 1 +", CodeUnexpectedSyntax},
		{"RETURN [, ]", CodeUnexpectedSyntax},
		{"RETURN {1}", CodeUnexpectedSyntax},
		{"RETURN {1B2c3e67: 1}", CodeUnexpectedSyntax}, // a map key is not a number
		{"RETURN 9223372#54775808", CodeUnexpectedSyntax},
		{"RETURN 9223372036854775808", CodeIntegerOverflow},
		{"RETURN -9223372036854775809", CodeIntegerOverflow},
		{"RETURN 0x8000000000000000", CodeIntegerOverflow},
		{"RETURN -0x8000000000000001", CodeIntegerOverflow},
		{"RETURN 0o1000000000000000000000", CodeIntegerOverflow},
		{"RETURN 1.34E999", CodeFloatingPointOverflow},
		{"RETURN 9223372h54775808", CodeInvalidNumberLiteral},
		{"RETURN 0x", CodeInvalidNumberLiteral},
		{"RETURN 0x1A2b3j4D5E6f7", CodeInvalidNumberLiteral},
		{"RETURN 0x1A2b3c4Z5E6f7", CodeInvalidNumberLiteral},
		{"RETURN 09", CodeInvalidNumberLiteral},
		{"RETURN 1e", CodeInvalidNumberLiteral},
		{"RETURN '\\uH'", CodeInvalidUnicodeLiteral},
		{"RETURN '\\uD83D'", CodeInvalidUnicodeLiteral},
		{"RETURN 42 — 41", CodeInvalidUnicodeCharacter},
		{"MATCH (a)-[:LIKES..]->(c) RETURN c", CodeInvalidRelationshipPattern},
		{"MATCH (a)-[:LIKES*-2]->(c) RETURN c", CodeInvalidRelationshipPattern},
		{"RETURN 1 AS a UNION RETURN 2 AS a UNION ALL RETURN 3 AS a", CodeInvalidClauseComposition},
		{"CREATE (a) MATCH (b) RETURN b", CodeInvalidClauseComposition},
	}
	for _, tc := range tests {
		_, err := Parse(tc.src)
		var se *SyntaxError
		if !errors.As(err, &se) {
			t.Errorf("Parse(%q) error = %v, want *SyntaxError", tc.src, err)
			continue
		}
		if got := se.ErrorCode(); got != tc.code {
			t.Errorf("Parse(%q): code %q, want %q (%v)", tc.src, got, tc.code, err)
		}
	}
}

// A number directly followed by letters is one invalid literal; separated by
// whitespace it is an ordinary syntax error (an alias needs AS).
func TestParse_NumberFollowedByIdentifier(t *testing.T) {
	for src, want := range map[string]string{
		"RETURN 12abc":  CodeInvalidNumberLiteral,
		"RETURN 12 abc": CodeUnexpectedSyntax,
	} {
		_, err := Parse(src)
		var se *SyntaxError
		if !errors.As(err, &se) || se.ErrorCode() != want {
			t.Errorf("Parse(%q) = %v, want code %s", src, err, want)
		}
	}
}

func TestSyntaxError_ErrorCodeDefault(t *testing.T) {
	if got := (&SyntaxError{}).ErrorCode(); got != CodeUnexpectedSyntax {
		t.Errorf("default code = %q", got)
	}
}
