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
		"n{.a, k: 1}", "x IS :: INTEGER | FLOAT", "x IS NFC NORMALIZED", "n:A&!B", "n:$(x)", "(a)-[:R]->+(b)", "(a) ((x)-->(y))+ (b)",
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
		"MATCH (a)-[:R]->{1,3}(b) RETURN a", "MATCH (a) ((x)-->(y) WHERE y.v > 1)+ (b) RETURN a", "MATCH p = ANY SHORTEST (a)-[:L]-+(b) RETURN p",
		"MATCH SHORTEST 2 GROUPS (p = (a)--+(b) WHERE length(p) > 1) RETURN p", "MATCH DIFFERENT RELATIONSHIPS (a)-->(b) RETURN a",
		"MATCH (n:A&(B|!C)) WHERE n:%|D RETURN n", "MATCH (n) WHERE EXISTS { (n)-->() WHERE true } RETURN COUNT { MATCH (n) RETURN n }",
		"MATCH (a) CALL (a) { MATCH (a)-->(b) RETURN b } IN TRANSACTIONS OF 5 ROWS ON ERROR RETRY FOR 3 SECONDS THEN FAIL RETURN b",
		"CALL (*) { WHEN $a THEN { RETURN 1 AS r } ELSE { RETURN 2 AS r } } RETURN r", "RETURN n{.a, b: 1, c, .*} AS m",
		"RETURN x IS :: LIST<INTEGER NOT NULL> | STRING, y IS NOT TYPED FLOAT, s IS NFC NORMALIZED", "MATCH (n) SET n:$(x), n[$k] = 1 REMOVE n:$(y)",
		"CREATE POINT INDEX i IF NOT EXISTS FOR (n:L) ON (n.p) OPTIONS {a: {b: 1}}", "CREATE CONSTRAINT c FOR ()-[r:T]-() REQUIRE (r.a, r.b) IS :: INTEGER",
		"CREATE FULLTEXT INDEX i FOR (n:A|B) ON EACH [n.a, n.b]", "CREATE LOOKUP INDEX i FOR (n) ON EACH labels(n)", "DROP CONSTRAINT c IF EXISTS",
		"SHOW INDEXES YIELD name WHERE name <> 'x'", "USE db MATCH (n) RETURN n", "CREATE USER u SET PASSWORD 'p'", "EXPLAIN CYPHER 25 runtime=parallel RETURN 1",
		"LOAD CSV WITH HEADERS FROM $u AS r FIELDTERMINATOR ';' CREATE (:N {a: r.a})", "MATCH (n) FILTER WHERE n.x LET y = n.y, z = 1 RETURN y",
		"MATCH (n:L) USING INDEX n:L(p) USING SCAN n:L WHERE n.p = 1 RETURN n", "MATCH (n) FINISH", "RETURN extract(x IN l | x)",
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
