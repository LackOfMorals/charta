package syntax

import "testing"

func TestParseNeverPanicsOnSchemaListComprehension(t *testing.T) {
	for _, q := range []string{
		`CREATE FULLTEXT INDEX i FOR (n:A) ON EACH [x IN [1] | x]`,
		`CREATE LOOKUP INDEX i FOR (n) ON EACH [(a)-->(b) | a]`,
	} {
		if _, err := Parse(q); err == nil {
			t.Errorf("%s: expected a syntax error", q)
		}
	}
}
