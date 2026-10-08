package syntax

import (
	"errors"
	"strings"
	"testing"
)

func sxInTx(t *InTransactions) string {
	if t == nil {
		return ""
	}
	s := " IN"
	if t.Concurrent {
		s += " " + opt("", t.Concurrency) + " CONCURRENT"
	}
	s += " TRANSACTIONS"
	s += opt(" OF ", t.BatchSize)
	if t.Disjoint != "" {
		s += " DISJOINT " + t.Disjoint
	}
	if t.ReportAs != "" {
		s += " REPORT " + t.ReportAs
	}
	if t.OnError != "" {
		s += " ON ERROR " + t.OnError
		s += opt(" FOR ", t.RetryFor)
		if t.RetryThen != "" {
			s += " THEN " + t.RetryThen
		}
	}
	return s
}

// sxSubquery renders subquery-related nodes for tests.
func sxSubquery(n Node) (string, bool) {
	switch n := n.(type) {
	case *SubqueryExpr:
		if n.Query != nil {
			return "(" + string(n.Kind) + " {" + sx(n.Query) + "})", true
		}
		return "(" + string(n.Kind) + " " + sxParts(n.Patterns) + opt(" WHERE ", n.Where) + ")", true
	case *Conditional:
		var b strings.Builder
		for i, w := range n.Branches {
			if i > 0 {
				b.WriteString(" ")
			}
			b.WriteString("WHEN " + sx(w.Cond) + " THEN {" + sx(w.Body) + "}")
		}
		if n.Else != nil {
			b.WriteString(" ELSE {" + sx(n.Else) + "}")
		}
		return b.String(), true
	case *CallSubquery:
		s := "CALL"
		if n.Optional {
			s = "OPTIONAL CALL"
		}
		switch {
		case n.ImportAll:
			s += " (*)"
		case n.Scoped:
			s += " (" + strings.Join(n.Imports, ", ") + ")"
		}
		return s + " {" + sx(n.Body) + "}" + sxInTx(n.InTx), true
	}
	return "", false
}

func TestParse_Subqueries(t *testing.T) {
	tests := []struct{ src, want string }{
		// EXISTS / COUNT pattern shorthand
		{"MATCH (n) WHERE EXISTS { (n)-->() } RETURN n", "MATCH (n) WHERE (EXISTS (n)-->()) RETURN n"},
		{"MATCH (n) WHERE EXISTS { (n)-->(m) WHERE m.x > 1 } RETURN n", "MATCH (n) WHERE (EXISTS (n)-->(m) WHERE (chain (. m x) > 1)) RETURN n"},
		{"MATCH (n) WHERE EXISTS { p = (n)-->(m), (m)--(o) } RETURN n", "MATCH (n) WHERE (EXISTS p=(n)-->(m), (m)--(o)) RETURN n"},
		{"MATCH (n) RETURN COUNT { (n)-->() } AS c", "MATCH (n) RETURN (COUNT (n)-->()) AS c"},
		{"MATCH (n) WHERE COUNT { (n)-->() } > 2 RETURN n", "MATCH (n) WHERE (chain (COUNT (n)-->()) > 2) RETURN n"},
		// full subquery form (RETURN optional for EXISTS and COUNT)
		{"MATCH (n) WHERE EXISTS { MATCH (n)-->(m) WHERE m.x } RETURN n", "MATCH (n) WHERE (EXISTS {MATCH (n)-->(m) WHERE (. m x)}) RETURN n"},
		{"MATCH (n) RETURN COUNT { MATCH (n)-->(m) RETURN m } AS c", "MATCH (n) RETURN (COUNT {MATCH (n)-->(m) RETURN m}) AS c"},
		{"MATCH (n) WHERE EXISTS { MATCH (n)-->(m) WITH m WHERE m.x RETURN m } RETURN n", "MATCH (n) WHERE (EXISTS {MATCH (n)-->(m) WITH m WHERE (. m x) RETURN m}) RETURN n"},
		{"MATCH (n) WHERE EXISTS { MATCH (n)-->(m) } AND NOT EXISTS { MATCH (n)<--(k) } RETURN n", "MATCH (n) WHERE (AND (EXISTS {MATCH (n)-->(m)}) (NOT (EXISTS {MATCH (n)<--(k)}))) RETURN n"},
		{"MATCH (n) WHERE COUNT { MATCH (a) WHERE EXISTS { (a)-->() } } > 0 RETURN n", "MATCH (n) WHERE (chain (COUNT {MATCH (a) WHERE (EXISTS (a)-->())}) > 0) RETURN n"},
		// COLLECT
		{"MATCH (n) RETURN COLLECT { MATCH (n)-->(m) RETURN m.name } AS names", "MATCH (n) RETURN (COLLECT {MATCH (n)-->(m) RETURN (. m name)}) AS names"},
		{"RETURN COLLECT { MATCH (a) RETURN a.x UNION MATCH (b) RETURN b.x }", "RETURN (COLLECT {MATCH (a) RETURN (. a x) UNION MATCH (b) RETURN (. b x)})"},
		{"RETURN COLLECT { WHEN true THEN { RETURN 1 AS r } ELSE { RETURN 2 AS r } }", "RETURN (COLLECT {WHEN true THEN {RETURN 1 AS r} ELSE {RETURN 2 AS r}})"},
		// existing function forms are unaffected
		{"RETURN exists(n.x), count(*), count(n), collect(n.x)", "RETURN (call exists (. n x)), (call count *), (call count n), (call collect (. n x))"},

		// CALL subqueries
		{"CALL { MATCH (n) RETURN n } RETURN n", "CALL {MATCH (n) RETURN n} RETURN n"},
		{"MATCH (a) CALL (a) { MATCH (a)-->(b) RETURN b } RETURN b", "MATCH (a) CALL (a) {MATCH (a)-->(b) RETURN b} RETURN b"},
		{"MATCH (a), (b) CALL (a, b) { RETURN a.x AS x } RETURN x", "MATCH (a), (b) CALL (a, b) {RETURN (. a x) AS x} RETURN x"},
		{"MATCH (a) CALL (*) { RETURN a.x AS x } RETURN x", "MATCH (a) CALL (*) {RETURN (. a x) AS x} RETURN x"},
		{"CALL () { RETURN 1 AS x } RETURN x", "CALL () {RETURN 1 AS x} RETURN x"},
		{"MATCH (a) OPTIONAL CALL (a) { MATCH (a)-->(b) RETURN b } RETURN b", "MATCH (a) OPTIONAL CALL (a) {MATCH (a)-->(b) RETURN b} RETURN b"},
		{"MATCH (a) CALL (a) { CREATE (b) }", "MATCH (a) CALL (a) {CREATE (b)}"},
		{"MATCH (a) CALL (a) { CREATE (b) } RETURN a", "MATCH (a) CALL (a) {CREATE (b)} RETURN a"},
		{"CALL { RETURN 1 AS x UNION RETURN 2 AS x } RETURN x", "CALL {RETURN 1 AS x UNION RETURN 2 AS x} RETURN x"},
		{"CALL { CALL { RETURN 1 AS x } RETURN x } RETURN x", "CALL {CALL {RETURN 1 AS x} RETURN x} RETURN x"},
		// deprecated importing WITH
		{"MATCH (a) CALL { WITH a RETURN a.x AS x } RETURN x", "MATCH (a) CALL {WITH a RETURN (. a x) AS x} RETURN x"},
		// conditional body
		{"MATCH (n) CALL (*) { WHEN n.x THEN { RETURN 1 AS r } ELSE { RETURN 2 AS r } } RETURN r", "MATCH (n) CALL (*) {WHEN (. n x) THEN {RETURN 1 AS r} ELSE {RETURN 2 AS r}} RETURN r"},
		{"CALL () { WHEN $a THEN { RETURN 1 AS r } WHEN $b THEN { RETURN 2 AS r } } RETURN r", "CALL () {WHEN $a THEN {RETURN 1 AS r} WHEN $b THEN {RETURN 2 AS r}} RETURN r"},
		// IN TRANSACTIONS
		{"UNWIND [1, 2] AS x CALL (x) { CREATE (:N {v: x}) } IN TRANSACTIONS", "UNWIND [1 2] AS x CALL (x) {CREATE (:N {v:x})} IN TRANSACTIONS"},
		{"UNWIND [1] AS x CALL (x) { CREATE (:N) } IN TRANSACTIONS OF 100 ROWS", "UNWIND [1] AS x CALL (x) {CREATE (:N)} IN TRANSACTIONS OF 100"},
		{"UNWIND [1] AS x CALL (x) { CREATE (:N) } IN TRANSACTIONS OF 1 ROW ON ERROR CONTINUE", "UNWIND [1] AS x CALL (x) {CREATE (:N)} IN TRANSACTIONS OF 1 ON ERROR CONTINUE"},
		{"UNWIND [1] AS x CALL (x) { CREATE (:N) } IN 3 CONCURRENT TRANSACTIONS", "UNWIND [1] AS x CALL (x) {CREATE (:N)} IN 3 CONCURRENT TRANSACTIONS"},
		{"UNWIND [1] AS x CALL (x) { CREATE (:N) } IN CONCURRENT TRANSACTIONS DISJOINT BY AUTO", "UNWIND [1] AS x CALL (x) {CREATE (:N)} IN  CONCURRENT TRANSACTIONS DISJOINT AUTO"},
		{"UNWIND [1] AS x CALL (x) { CREATE (:N) } IN TRANSACTIONS REPORT STATUS AS s ON ERROR RETRY FOR 10 SECONDS THEN FAIL", "UNWIND [1] AS x CALL (x) {CREATE (:N)} IN TRANSACTIONS REPORT s ON ERROR RETRY FOR 10 THEN FAIL"},
		{"UNWIND [1] AS x CALL (x) { CREATE (:N) } IN TRANSACTIONS ON ERROR BREAK", "UNWIND [1] AS x CALL (x) {CREATE (:N)} IN TRANSACTIONS ON ERROR BREAK"},
		{"UNWIND [1] AS x CALL (x) { CREATE (:N) } IN TRANSACTIONS DISJOINT BY (x, x.y)", "UNWIND [1] AS x CALL (x) {CREATE (:N)} IN TRANSACTIONS"},
	}
	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			st := parseOK(t, tc.src)
			if got := sx(st); got != tc.want {
				t.Errorf("Parse(%q)\n got %s\nwant %s", tc.src, got, tc.want)
			}
		})
	}
}

func TestParse_SubqueryErrors(t *testing.T) {
	tests := []struct {
		name, src string
		line, col int
		msg       string
	}{
		{"empty exists", "RETURN EXISTS { }", 1, 17, "unexpected token"},
		{"unterminated exists", "RETURN EXISTS { (a)-->(b) ", 1, 27, "end of input"},
		{"collect needs return", "RETURN COLLECT { MATCH (a) }", 1, 18, "must end with RETURN"},
		{"collect pattern shorthand", "RETURN COLLECT { (a)-->(b) }", 1, 18, "unexpected token"},
		{"exists clause order still checked", "RETURN EXISTS { CREATE (a) MATCH (b) }", 1, 28, "WITH is required"},
		{"read-only call body without return", "CALL { MATCH (n) } RETURN 1", 1, 8, "cannot conclude with MATCH"},
		{"call scope unclosed", "CALL (a { RETURN 1 AS x } RETURN x", 1, 9, "unexpected token"},
		{"call scope bad variable", "CALL (1) { RETURN 1 AS x } RETURN x", 1, 7, "unexpected token"},
		{"call body unterminated", "CALL { RETURN 1 AS x ", 1, 22, "end of input"},
		{"in transactions missing keyword", "CALL { CREATE (n) } IN", 1, 23, "end of input"},
		{"in transactions bad error action", "CALL { CREATE (n) } IN TRANSACTIONS ON ERROR EXPLODE", 1, 46, "unexpected token"},
		{"in transactions rows keyword", "CALL { CREATE (n) } IN TRANSACTIONS OF 5", 1, 41, "end of input"},
		{"report status needs as", "CALL { CREATE (n) } IN TRANSACTIONS REPORT STATUS s", 1, 51, "unexpected token"},
		{"when without then", "CALL () { WHEN true { RETURN 1 AS r } } RETURN r", 1, 21, "unexpected token"},
		{"when branch not braced", "CALL () { WHEN true THEN RETURN 1 AS r } RETURN r", 1, 26, "unexpected token"},
		{"nested too deep", "RETURN " + strings.Repeat("COUNT { MATCH (a) WHERE ", 300) + "true" + strings.Repeat(" }", 300), 1, 0, "nested too deeply"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.src)
			var se *SyntaxError
			if !errors.As(err, &se) {
				t.Fatalf("Parse(%q) error = %v, want *SyntaxError", tc.src, err)
			}
			if tc.col != 0 && (se.Pos.Line != tc.line || se.Pos.Col != tc.col) {
				t.Errorf("error at %d:%d, want %d:%d: %v", se.Pos.Line, se.Pos.Col, tc.line, tc.col, err)
			}
			if !strings.Contains(se.Error(), tc.msg) {
				t.Errorf("error %q does not contain %q", se.Error(), tc.msg)
			}
		})
	}
}
