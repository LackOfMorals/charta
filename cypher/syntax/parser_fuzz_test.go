package syntax

import (
	"errors"
	"testing"
)

// FuzzParseExpr checks that the expression parser never panics or hangs and
// that every failure is a well-formed *SyntaxError.
func FuzzParseExpr(f *testing.F) {
	for _, s := range []string{
		"1 + 2 * 3", "a OR b AND NOT c", "-x ^ 2", "a < b <= c", "n.a[1..2].b", "n:A&!B|%",
		"count(DISTINCT x)", "CASE WHEN a THEN 1 ELSE 2 END", "a IS NOT NULL", "x IN [1, {a: $p}]",
		"-9223372036854775808", "0x1F", "'a\\u00e9'", "f(", "[1,", "{a:", "n:$(x)", "a.b.c(1)",
		"(a)-[r:T*1..3 {x: 1}]->(b)", "(a)<-->(b) AND x", "shortestPath((a)-[*]-(b))", "[(a)-->(b) WHERE b.x | b]",
		"[x IN l WHERE x > 1 | x]", "all(x IN l WHERE x)", "reduce(a = 0, x IN l | a + x)", "(a) - 1", "(a)-[", "[(a)]",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		e, err := ParseExpr(src)
		if err != nil {
			var se *SyntaxError
			if !errors.As(err, &se) {
				t.Fatalf("error is %T, want *SyntaxError", err)
			}
			if se.Pos.Offset < 0 || se.Pos.Offset > len(src) || se.Pos.Line < 1 || se.Pos.Col < 1 || se.Msg == "" {
				t.Fatalf("malformed error %+v", se)
			}
			return
		}
		if e == nil || e.Pos().Offset < 0 || e.Pos().Offset > len(src) {
			t.Fatalf("bad result %v", e)
		}
		_ = Dump(e) // must not panic on any tree the parser can build
	})
}

// FuzzParse checks that whole-statement parsing never panics or hangs and that
// every failure is a well-formed *SyntaxError.
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"MATCH (n) RETURN n", "MATCH (a)-[r:T*1..3]->(b) WHERE b.x > 1 RETURN a, count(*) ORDER BY a SKIP 1 LIMIT 2",
		"CREATE (n:L {a: 1})", "MERGE (n:L) ON CREATE SET n.a = 1 ON MATCH SET n += $p",
		"MATCH (n) SET n:A:B, n.x = 1 REMOVE n.y DETACH DELETE n", "UNWIND [1, 2] AS x WITH x WHERE x > 1 RETURN x",
		"FOREACH (x IN l | CREATE (:N {v: x}))", "CALL db.labels() YIELD label AS l WHERE l <> 'x'",
		"RETURN 1 UNION ALL RETURN 2", "MATCH (n) WITH n MATCH (m) RETURN m;", "OPTIONAL MATCH p = shortestPath((a)-[*]-(b)) RETURN p",
		"MATCH (n", "RETURN", "CREATE (a) MATCH (b) RETURN b", "FOREACH (", "MERGE (n) ON",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		st, err := Parse(src)
		if err != nil {
			var se *SyntaxError
			if !errors.As(err, &se) {
				t.Fatalf("error is %T, want *SyntaxError", err)
			}
			if se.Pos.Offset < 0 || se.Pos.Offset > len(src) || se.Pos.Line < 1 || se.Pos.Col < 1 || se.Msg == "" {
				t.Fatalf("malformed error %+v", se)
			}
			return
		}
		if st == nil || st.Body == nil {
			t.Fatal("nil statement")
		}
		_ = Dump(st)
	})
}
