package analyze_test

import (
	"testing"

	"github.com/LackOfMorals/charta/cypher/analyze"
	"github.com/LackOfMorals/charta/cypher/syntax"
)

func run(t *testing.T, q string) (class, code string) {
	t.Helper()
	return check(t, q)
}

func TestCheck_Errors(t *testing.T) {
	tests := []struct {
		q           string
		class, code string
	}{
		// scope
		{"MATCH (n) RETURN m", "SyntaxError", "UndefinedVariable"},
		{"MATCH (n) WHERE n.x = missing RETURN n", "SyntaxError", "UndefinedVariable"},
		{"MATCH (n) WITH n.x AS x RETURN n", "SyntaxError", "UndefinedVariable"},
		{"MATCH (n) SET m.x = 1", "SyntaxError", "UndefinedVariable"},
		{"MATCH (n) DELETE m", "SyntaxError", "UndefinedVariable"},
		{"MATCH (n) WHERE (n)-->(m) RETURN n", "SyntaxError", "UndefinedVariable"},
		{"RETURN [x IN [1, 2] | x] AS a, x", "SyntaxError", "UndefinedVariable"},
		{"MATCH (a) WITH a MATCH (b) RETURN a, c", "SyntaxError", "UndefinedVariable"},
		{"UNWIND [1] AS x RETURN y", "SyntaxError", "UndefinedVariable"},
		{"MATCH (n) RETURN DISTINCT n.name ORDER BY n.age", "SyntaxError", "UndefinedVariable"},
		// bindings
		{"MATCH p = (a)-->(b) MATCH p = (c)-->(d) RETURN p", "SyntaxError", "VariableAlreadyBound"},
		{"MATCH (n) CREATE (n)", "SyntaxError", "VariableAlreadyBound"},
		{"MATCH (n) CREATE (n:Extra)-[:R]->()", "SyntaxError", "VariableAlreadyBound"},
		{"MATCH (a)-[r]->(b) CREATE (a)-[r:T]->(b)", "SyntaxError", "VariableAlreadyBound"},
		{"MATCH (n) MERGE (n)", "SyntaxError", "VariableAlreadyBound"},
		{"MATCH ()-[r]-() MATCH (r) RETURN r", "SyntaxError", "VariableTypeConflict"},
		{"MATCH (r) MATCH ()-[r]-() RETURN r", "SyntaxError", "VariableTypeConflict"},
		{"WITH 1 AS n MATCH (n) RETURN n", "SyntaxError", "VariableTypeConflict"},
		{"MATCH (a)-[r]->()-[r]->(a) RETURN r", "SyntaxError", "RelationshipUniquenessViolation"},
		// projections
		{"RETURN 1 AS a, 2 AS a", "SyntaxError", "ColumnNameConflict"},
		{"WITH 1 AS a, 2 AS a RETURN a", "SyntaxError", "ColumnNameConflict"},
		{"MATCH (a) WITH a, count(*) RETURN a", "SyntaxError", "NoExpressionAlias"},
		{"MATCH () RETURN *", "SyntaxError", "NoVariablesInScope"},
		{"RETURN 1 AS a UNION RETURN 2 AS b", "SyntaxError", "DifferentColumnsInUnion"},
		{"RETURN 1 AS a UNION RETURN 2 AS a UNION ALL RETURN 3 AS a", "SyntaxError", "InvalidClauseComposition"},
		{"MATCH (n) RETURN n SKIP n.x", "SyntaxError", "NonConstantExpression"},
		{"MATCH (n) RETURN n LIMIT -1", "SyntaxError", "NegativeIntegerArgument"},
		{"MATCH (n) RETURN n LIMIT 1.5", "SyntaxError", "InvalidArgumentType"},
		// aggregation
		{"MATCH (n) WHERE count(n) > 1 RETURN n", "SyntaxError", "InvalidAggregation"},
		{"MATCH (n) RETURN n.x ORDER BY max(n.y)", "SyntaxError", "InvalidAggregation"},
		{"RETURN count(count(*))", "SyntaxError", "NestedAggregation"},
		{"RETURN count(rand())", "SyntaxError", "NonConstantExpression"},
		{"MATCH (a)--(b) RETURN a.x + count(b.y)", "SyntaxError", "AmbiguousAggregationExpression"},
		{"MATCH (a)--(b) RETURN a.x + b.y, a.x + b.y + count(*)", "SyntaxError", "AmbiguousAggregationExpression"},
		{"RETURN [x IN [1, 2] | count(*)]", "SyntaxError", "InvalidAggregation"},
		// clause rules
		{"CREATE ()-->()", "SyntaxError", "NoSingleRelationshipType"},
		{"CREATE ()-[:A|B]->()", "SyntaxError", "NoSingleRelationshipType"},
		{"CREATE (a)-[:R]-(b)", "SyntaxError", "RequiresDirectedRelationship"},
		{"CREATE ()-[:R*2]->()", "SyntaxError", "CreatingVarLength"},
		{"MERGE (a)-[:R*1..2]->(b)", "SyntaxError", "CreatingVarLength"},
		{"MATCH (n $p) RETURN n", "SyntaxError", "InvalidParameterUse"},
		{"MERGE (n $p)", "SyntaxError", "InvalidParameterUse"},
		{"MATCH (n) DELETE n:Label", "SyntaxError", "InvalidDelete"},
		{"MATCH (n) WHERE EXISTS { MATCH (n)-->(m) SET m.x = 1 } RETURN n", "SyntaxError", "InvalidClauseComposition"},
		// types
		{"RETURN NOT 1", "SyntaxError", "InvalidArgumentType"},
		{"RETURN 1 AND true", "SyntaxError", "InvalidArgumentType"},
		{"RETURN 1 IN 'x'", "SyntaxError", "InvalidArgumentType"},
		{"RETURN 'a' - 1", "SyntaxError", "InvalidArgumentType"},
		{"MATCH (n) WHERE (n) RETURN n", "SyntaxError", "InvalidArgumentType"},
		{"MATCH (n) RETURN length(n)", "SyntaxError", "InvalidArgumentType"},
		{"MATCH (n) RETURN type(n)", "SyntaxError", "InvalidArgumentType"},
		{"RETURN properties(1)", "SyntaxError", "InvalidArgumentType"},
		{"MATCH p = (a) RETURN labels(p)", "SyntaxError", "InvalidArgumentType"},
		{"MATCH p = (a)-[*]->(b) RETURN size(p)", "SyntaxError", "InvalidArgumentType"},
		{"MATCH () DELETE 1 + 1", "SyntaxError", "InvalidArgumentType"},
		{"MATCH p = (a)-->(b) WHERE p.name = 'x' RETURN p", "SyntaxError", "InvalidArgumentType"},
		{"WITH 1 AS v RETURN v.num", "TypeError", "InvalidArgumentType"},
		{"RETURN all(x IN ['a'] WHERE x % 2 = 0)", "SyntaxError", "InvalidArgumentType"},
		{"RETURN foo(1)", "SyntaxError", "UnknownFunction"},
		{"MATCH (n) RETURN (n)-->()", "SyntaxError", "UnexpectedSyntax"},
		{"MATCH (n) WITH (n)-->() AS x RETURN x", "SyntaxError", "UnexpectedSyntax"},
		{"MATCH (a), (b) RETURN size((a)-->(b))", "SyntaxError", "UnexpectedSyntax"},
		{"CALL nope.proc() YIELD x RETURN x", "ProcedureError", "ProcedureNotFound"},
		// lexer / parser codes
		{"RETURN 9223372036854775808", "SyntaxError", "IntegerOverflow"},
		{"RETURN -9223372036854775809", "SyntaxError", "IntegerOverflow"},
		{"RETURN 1.5e999", "SyntaxError", "FloatingPointOverflow"},
		{"RETURN 12abc", "SyntaxError", "InvalidNumberLiteral"},
		{"RETURN 0x", "SyntaxError", "InvalidNumberLiteral"},
		{"RETURN '\\uZZ'", "SyntaxError", "InvalidUnicodeLiteral"},
		{"RETURN 42 — 41", "SyntaxError", "InvalidUnicodeCharacter"},
		{"MATCH (a)-[:R..]->(b) RETURN a", "SyntaxError", "InvalidRelationshipPattern"},
		{"MATCH (a)-[*-2]->(b) RETURN a", "SyntaxError", "InvalidRelationshipPattern"},
		{"RETURN {1a: 1}", "SyntaxError", "UnexpectedSyntax"},
		{"RETURN [,]", "SyntaxError", "UnexpectedSyntax"},
	}
	for _, tc := range tests {
		t.Run(tc.q, func(t *testing.T) {
			class, code := run(t, tc.q)
			if class != tc.class || code != tc.code {
				t.Errorf("got %q %q, want %q %q", class, code, tc.class, tc.code)
			}
		})
	}
}

// TestCheck_ValidQueries guards against false positives, including the Neo4j
// syntax the TCK does not cover.
func TestCheck_ValidQueries(t *testing.T) {
	for _, q := range []string{
		"MATCH (n) RETURN n",
		"MATCH (a)-[r:KNOWS]->(b) RETURN a, r, b",
		"MATCH (a)-[r]->(b) MATCH (a)-[r]->(b) RETURN r", // rejoining bound variables
		"MATCH (n) WITH n MATCH (n)-->(m) RETURN m",
		"MATCH (n) WITH n.name AS name ORDER BY n.age RETURN name",
		"MATCH (n) RETURN n.name AS name ORDER BY n.age",
		"MATCH (n) RETURN DISTINCT n.name ORDER BY n.name",
		"MATCH (n) RETURN n.name AS name, count(*) AS c ORDER BY c DESC, name",
		"MATCH (a)--(b) RETURN a.x, a.x + count(b.y)",
		"MATCH (a) RETURN a, count(a) + 3",
		"MATCH (a) WITH a, count(*) AS c WHERE c > 1 RETURN a",
		"MATCH (n) WITH DISTINCT n.x AS x WHERE x > 1 RETURN x",
		"MATCH () CREATE () WITH * CREATE ()",
		"MATCH (n) RETURN *",
		"MATCH (n) WITH * RETURN n",
		"UNWIND [1, 2, 3] AS x WITH x WHERE x > 1 RETURN x",
		"UNWIND $list AS item MERGE (n:Item {id: item.id}) SET n.name = item.name RETURN n",
		"MATCH (n) RETURN [x IN range(1, 3) WHERE x > 1 | x * 2] AS xs",
		"MATCH (n) WHERE ALL(x IN n.list WHERE x > 1) RETURN n",
		"MATCH (n) RETURN reduce(acc = 0, x IN [1, 2] | acc + x)",
		"MATCH (n) RETURN [(n)-->(m) | m.name] AS names",
		"MATCH (n) WHERE (n)-->() AND NOT (n)<--() RETURN n",
		"MATCH (n) WHERE EXISTS { (n)-->(m) WHERE m.x > 1 } RETURN n",
		"MATCH (n) RETURN COUNT { (n)-->() } AS c, COLLECT { MATCH (n)-->(m) RETURN m.name } AS names",
		"MATCH (a) CALL (a) { MATCH (a)-->(b) RETURN b } RETURN a, b",
		"MATCH (a) CALL { WITH a MATCH (a)-->(b) RETURN b } RETURN a, b",
		"CALL db.labels() YIELD label RETURN label",
		"CALL db.labels() YIELD *",
		"MATCH (n) FOREACH (x IN [1, 2] | CREATE (:Item {v: x}))",
		"MATCH (a), (b) WHERE a <> b CREATE (a)-[:R]->(b)",
		"MATCH (a) CREATE (a)-[:R]->(b:Other {x: a.x})",
		"CREATE (n:L {a: 1})-[:R]->(m:L), (n)-[:S]->(m)",
		"MERGE (n:L {id: 1}) ON CREATE SET n.c = 1 ON MATCH SET n.m = 2 RETURN n",
		"MATCH (n) SET n += {a: 1}, n:Extra REMOVE n.b, n:Old",
		"MATCH p = (a)-[*1..3]->(b) RETURN p, length(p), nodes(p), relationships(p)",
		"MATCH p = shortestPath((a)-[*]-(b)) RETURN p",
		"MATCH (a) ((x)-->(y)){1,3} (b) RETURN a, b",
		"MATCH p = ANY SHORTEST (a)-[:L]-+(b) RETURN p",
		"MATCH (n) RETURN n{.name, age: n.age} AS m",
		"MATCH (n:A&B) WHERE n:C|D RETURN n",
		"MATCH (n) WHERE n.x IS :: INTEGER RETURN n",
		"MATCH (n) FILTER n.x > 1 LET y = n.x * 2 RETURN y",
		"MATCH (n) RETURN n.a + 1, 'x' + 'y', [1] + [2], -n.a, n.a % 2 = 0",
		"MATCH (n) RETURN toString(n.a), toInteger('1'), size('abc'), size([1]), head([1]), coalesce(n.a, 0)",
		"RETURN date('2020-01-01') AS d, duration({days: 1}) AS dur, date.truncate('month', date()) AS m",
		"MATCH (n) RETURN CASE WHEN n.a THEN 1 ELSE 2 END, CASE n.b WHEN 1 THEN 'x' END",
		"RETURN 1 AS a UNION RETURN 2 AS a",
		"MATCH (n) RETURN n.x AS a UNION ALL MATCH (m) RETURN m.y AS a",
		"EXPLAIN MATCH (n) RETURN n",
		"CYPHER 25 runtime=parallel MATCH (n) RETURN n",
		"CREATE INDEX FOR (n:L) ON (n.p)",
		"SHOW INDEXES",
		"RETURN $a AS a, $b + 1 AS b",
		"MATCH (n) RETURN n LIMIT $l SKIP $s",
		"WITH {a: 1} AS m RETURN m.a, m['a']",
		"WITH [1, 2] AS l RETURN l[0], l[0..1]",
		"MATCH (n)-[r]->() RETURN labels(n), keys(n), properties(n), id(n), type(r)",
	} {
		t.Run(q, func(t *testing.T) {
			if class, code := run(t, q); code != "" {
				t.Errorf("rejected with %s %s", class, code)
			}
		})
	}
}

func TestDescribe(t *testing.T) {
	if _, _, ok := analyze.Describe(nil); ok {
		t.Error("Describe(nil) should report !ok")
	}
	_, err := syntax.Parse("RETURN 9223372036854775808")
	if class, code, ok := analyze.Describe(err); !ok || class != "SyntaxError" || code != "IntegerOverflow" {
		t.Errorf("Describe = %q %q %v", class, code, ok)
	}
}

// FuzzCheck makes sure that analysis never panics on anything the parser
// accepts, and that it only ever returns *analyze.Error.
func FuzzCheck(f *testing.F) {
	for _, s := range []string{
		"MATCH (a)-[r]->(b) WITH a, count(*) AS c ORDER BY c RETURN a",
		"MATCH (n) WHERE EXISTS { (n)-->() } RETURN COUNT { (n)-->() }",
		"UNWIND [1] AS x CALL (x) { RETURN x AS y } RETURN y",
		"MATCH (a) ((x)-->(y)){1,3} (b) RETURN [p IN [1] WHERE p > 0 | p]",
		"CREATE (a)-[:R]->(b) MERGE (c) ON CREATE SET c.x = 1 RETURN *",
		"RETURN reduce(a = 0, x IN [1] | a + x), all(x IN [] WHERE x)",
		"MATCH (n) FOREACH (x IN [1] | SET n.v = x)", "RETURN n{.a}", "RETURN 1 IN [1] AND NOT true",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, q string) {
		st, err := syntax.Parse(q)
		if err != nil {
			return
		}
		if err := analyze.Check(st); err != nil {
			if _, ok := err.(*analyze.Error); !ok {
				t.Fatalf("Check returned %T", err)
			}
		}
	})
}
