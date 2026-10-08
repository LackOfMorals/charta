package cypher_test

import (
	"strings"
	"testing"

	"github.com/LackOfMorals/graphlite/v2/cypher"
)

// These tests pin down behaviours of Parse that were established while
// replacing the former ANTLR-based parser (see
// .plans/prd-handwritten-cypher-parser.md): where that parser was wrong, where
// Parse is deliberately more lenient, and what the Query AST cannot express.

func TestParse_ChainedComparisonComparesAdjacentOperands(t *testing.T) {
	q := mustParse(t, "MATCH (n) WHERE 1 < n.x <= 10 RETURN n")
	m := q.Clauses[0].(*cypher.MatchClause)
	and, ok := m.Where.(*cypher.BoolExpr)
	if !ok || and.Op != "AND" {
		t.Fatalf("WHERE = %#v, want AND of two comparisons", m.Where)
	}
	left, right := and.Left.(*cypher.ComparisonExpr), and.Right.(*cypher.ComparisonExpr)
	if left.Op != "<" || right.Op != "<=" {
		t.Fatalf("ops = %q, %q", left.Op, right.Op)
	}
	// The second comparison must start from the first one's right operand
	// (n.x), not repeat its left operand (1).
	if p, ok := right.Left.(*cypher.PropExpr); !ok || p.Variable != "n" || p.Property != "x" {
		t.Errorf("second comparison left operand = %#v, want n.x", right.Left)
	}
}

func TestParse_VarLengthBoundsWithProperties(t *testing.T) {
	tests := []struct {
		pattern  string
		min, max int
	}{
		{"[*]", 1, 0}, {"[*3]", 3, 3}, {"[*2..]", 2, 0}, {"[*..5]", 1, 5}, {"[*1..3]", 1, 3}, {"[*0..2]", 0, 2},
		{"[*2..4 {w: 1}]", 2, 4}, {"[:T*..4 {w: 1}]", 1, 4}, {"[r:T|U*2 {w: 1}]", 2, 2},
	}
	for _, tc := range tests {
		q := mustParse(t, "MATCH (a)-"+tc.pattern+"->(b) RETURN a")
		rel := q.Clauses[0].(*cypher.MatchClause).Pattern[0].Chain[0].Rel
		if !rel.VarLength || rel.MinHops != tc.min || rel.MaxHops != tc.max {
			t.Errorf("%s: VarLength=%v hops=%d..%d, want %d..%d", tc.pattern, rel.VarLength, rel.MinHops, rel.MaxHops, tc.min, tc.max)
		}
	}
}

func TestParse_SetOnParenthesisedVariableIsTyped(t *testing.T) {
	q := mustParse(t, "MATCH (n) SET (n).x = 1")
	item := q.Clauses[1].(*cypher.SetClause).Items[0]
	if lit, ok := item.Expr.(*cypher.LiteralExpr); item.Variable != "n" || item.Property != "x" || !ok || lit.Value != int64(1) {
		t.Errorf("SetItem = %#v", item)
	}
}

// Property values, SET values, ORDER BY keys and DELETE targets are typed
// expressions, not source text.
func TestParse_TypedValues(t *testing.T) {
	q := mustParse(t, "MATCH (n {a:  1 + 2, tags: [1,  'x y', 3], m: {k: 1}, p: $p, neg: -4, f: -1.5, s: 'it\\'s'}) RETURN n.a  +  1  AS s ORDER BY  n.a   DESC")
	node := q.Clauses[0].(*cypher.MatchClause).Pattern[0].Start
	if arith, ok := node.Props["a"].(*cypher.ArithExpr); !ok || arith.Op != "+" {
		t.Errorf("Props[a] = %#v, want an ArithExpr", node.Props["a"])
	}
	if list, ok := node.Props["tags"].(*cypher.ListLiteralExpr); !ok || len(list.Items) != 3 {
		t.Errorf("Props[tags] = %#v, want a 3-element ListLiteralExpr", node.Props["tags"])
	}
	if _, ok := node.Props["m"].(*cypher.RawExpr); !ok {
		t.Errorf("Props[m] = %#v, want RawExpr (map values are not modelled yet)", node.Props["m"])
	}
	if p, ok := node.Props["p"].(*cypher.ParamRef); !ok || p.Name != "p" {
		t.Errorf("Props[p] = %#v", node.Props["p"])
	}
	// Negative literals are plain literals, and escapes are decoded.
	for key, want := range map[string]any{"neg": int64(-4), "f": -1.5, "s": "it's"} {
		if lit, ok := node.Props[key].(*cypher.LiteralExpr); !ok || lit.Value != want {
			t.Errorf("Props[%s] = %#v, want literal %v", key, node.Props[key], want)
		}
	}
	ret := q.Clauses[1].(*cypher.ReturnClause)
	if ret.Items[0].Source != "n.a  +  1" || ret.Items[0].Alias != "s" {
		t.Errorf("item = %#v (Source must keep the verbatim text for column naming)", ret.Items[0])
	}
	if _, ok := ret.Items[0].Expr.(*cypher.ArithExpr); !ok {
		t.Errorf("item expr = %#v", ret.Items[0].Expr)
	}
	if p, ok := ret.OrderBy[0].Expr.(*cypher.PropExpr); !ok || p.Variable != "n" || p.Property != "a" || !ret.OrderBy[0].Descending {
		t.Errorf("order = %#v", ret.OrderBy[0])
	}
}

func TestParse_ParamPropertyMapUsesDollarKey(t *testing.T) {
	q := mustParse(t, "CREATE (n:L $props)")
	node := q.Clauses[0].(*cypher.CreateClause).Pattern[0].Start
	if p, ok := node.Props["$"].(*cypher.ParamRef); !ok || p.Name != "props" || !node.HasExplicitProps {
		t.Errorf("node = %#v", node)
	}
}

func TestParse_SetMergeProps(t *testing.T) {
	q := mustParse(t, "MATCH (n) SET n += {a: 1, b: [1, 2], c: $p}")
	item := q.Clauses[1].(*cypher.SetClause).Items[0]
	if !item.Merge || len(item.Props) != 3 {
		t.Fatalf("item = %#v", item)
	}
	if _, ok := item.Props["b"].(*cypher.ListLiteralExpr); !ok {
		t.Errorf("Props[b] = %#v", item.Props["b"])
	}
	// A right-hand side that is not a map literal gives an empty map.
	q = mustParse(t, "MATCH (n) SET n += $props")
	if item := q.Clauses[1].(*cypher.SetClause).Items[0]; !item.Merge || item.Props == nil || len(item.Props) != 0 {
		t.Errorf("item = %#v", item)
	}
}

func TestParse_StarProjectionIsAnEmptyItemList(t *testing.T) {
	for _, src := range []string{"MATCH (n) RETURN *", "MATCH (n) WITH * RETURN n"} {
		q := mustParse(t, src)
		var items int
		switch c := q.Clauses[1].(type) {
		case *cypher.ReturnClause:
			items = len(c.Items)
		case *cypher.WithClause:
			items = len(c.Items)
		}
		if items != 0 {
			t.Errorf("%s: %d items, want 0 (planner reads empty as 'all variables')", src, items)
		}
	}
}

func TestParse_Lenient(t *testing.T) {
	for _, src := range []string{
		"MATCH (n) RETURN n LIMIT 10 SKIP 5",
		"MATCH (n) RETURN n OFFSET 5",
		"MATCH (n) NODETACH DELETE n",
		"MATCH (match) RETURN match",
		"MATCH (n:A&B) RETURN n",
	} {
		if _, err := cypher.Parse(src); err != nil {
			t.Errorf("Parse(%q): %v", src, err)
		}
	}
}

func TestParse_RejectsWhatTheQueryASTCannotExpress(t *testing.T) {
	tests := []struct{ src, want string }{
		{"MATCH (n) RETURN n UNION MATCH (m) RETURN m", "UNION"},
		{"UNWIND [1, 2] AS x RETURN x", "only MATCH"},
		{"MATCH (n) CALL db.labels() YIELD label RETURN label", "only MATCH"},
		{"CALL db.labels()", "standalone CALL"},
		{"MATCH (n) FOREACH (x IN [1] | SET n.v = x)", "FOREACH"},
		{"MATCH (n) WITH n MATCH (m) WITH n, m RETURN n", "multiple WITH"},
		{"MATCH (n) SET n:Label", "SET items"},
		{"MATCH (n) SET n = {a: 1}", "SET items"},
		{"MATCH p = shortestPath((a)-[*]-(b)) RETURN p", "shortestPath"},
		{"MATCH (n:A|B) RETURN n", "label expression"},
		{"MATCH (n:!A) RETURN n", "label expression"},
		{"MATCH (a)-[:A&B]->(b) RETURN a", "type expression"},
		{"MATCH (n WHERE n.x > 1) RETURN n", "WHERE inside a node"},
		{"MATCH (a)-[r WHERE r.x > 1]->(b) RETURN a", "WHERE inside a relationship"},
		{"MATCH (n) RETURN n SKIP 1 + 1", "SKIP"},
		{"MATCH (n) WITH n SKIP $s RETURN n", "WITH SKIP"},
		{"MATCH (n) WHERE n.a =~ 'x' RETURN n", "operator"},
	}
	for _, tc := range tests {
		_, err := cypher.Parse(tc.src)
		if err == nil {
			t.Errorf("Parse(%q) succeeded, want an error", tc.src)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Parse(%q) error = %q, want it to mention %q", tc.src, err, tc.want)
		}
	}
}

func TestParse_RejectsNeo4jExtensionsWithAClearMessage(t *testing.T) {
	// These parse in cypher/syntax but the Query AST cannot express them yet.
	tests := []struct{ src, want string }{
		{"EXPLAIN MATCH (n) RETURN n", "EXPLAIN/PROFILE"},
		{"PROFILE MATCH (n) RETURN n", "EXPLAIN/PROFILE"},
		{"MATCH (n) CALL (n) { RETURN 1 AS x } RETURN x", "CALL {}"},
		{"MATCH (n) WHERE EXISTS { (n)-->() } RETURN n", "subquery"},
		{"MATCH (a)-[:R]->+(b) RETURN a", "quantified relationship"},
		{"MATCH (a) ((x)-->(y))+ (b) RETURN a", "quantified path pattern"},
		{"MATCH SHORTEST 1 (a)-[:R]-+(b) RETURN a", "path selector"},
		{"MATCH DIFFERENT RELATIONSHIPS (a)-->(b) RETURN a", "match mode"},
		{"MATCH (n) USING INDEX n:L(p) RETURN n", "hint"},
		{"LOAD CSV FROM 'f' AS r RETURN r", "LOAD CSV"},
		{"MATCH (n) FILTER n.x RETURN n", "FILTER"},
		{"MATCH (n) LET x = 1 RETURN x", "LET"},
		{"MATCH (n) FINISH", "FINISH"},
		{"CREATE INDEX FOR (n:L) ON (n.p)", "CREATE INDEX"},
		{"CREATE CONSTRAINT FOR (n:L) REQUIRE n.p IS UNIQUE", "CREATE CONSTRAINT"},
		{"DROP INDEX i", "DROP INDEX"},
		{"SHOW INDEXES", "SHOW"},
		{"USE db MATCH (n) RETURN n", "server command USE"},
		{"CREATE USER u SET PASSWORD 'p'", "server command CREATE USER"},
		{"MATCH (n) WHERE n.x IS :: INTEGER RETURN n", "type predicate"},
		{"MATCH (n) WHERE n.s IS NORMALIZED RETURN n", "NORMALIZED"},
		{"MATCH (n) SET n:$($label)", "SET items"},
		{"MATCH (n) SET n[$k] = 1", "dynamic property"},
		{"MATCH (n:$($label)) RETURN n", "dynamic label"},
		{"MATCH (a)-[:$($type)]->(b) RETURN a", "dynamic relationship type"},
	}
	for _, tc := range tests {
		_, err := cypher.Parse(tc.src)
		if err == nil {
			t.Errorf("Parse(%q) succeeded, want an error", tc.src)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "not supported") && !strings.Contains(err.Error(), "only") {
			t.Errorf("Parse(%q) error = %q, want a not-supported error mentioning %q", tc.src, err, tc.want)
		}
	}
}

func TestParse_AcceptsCypherVersionPrefix(t *testing.T) {
	q := mustParse(t, "CYPHER 25 runtime=parallel MATCH (n) RETURN n")
	if len(q.Clauses) != 2 {
		t.Errorf("clauses = %d, want 2", len(q.Clauses))
	}
	if _, err := cypher.Parse("CYPHER 5 MATCH (n) RETURN n"); err == nil || !strings.Contains(err.Error(), "Cypher 25") {
		t.Errorf("CYPHER 5 error = %v, want a message naming Cypher 25", err)
	}
}

func TestParse_SyntaxErrorsCarryAPosition(t *testing.T) {
	_, err := cypher.Parse("MATCH (n\nRETURN n")
	if err == nil || !strings.Contains(err.Error(), "line 2, column 1") {
		t.Errorf("error = %v, want a syntax error at line 2, column 1", err)
	}
}

func TestParse_ConcurrentUse(t *testing.T) {
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			var err error
			for j := 0; j < 200 && err == nil; j++ {
				_, err = cypher.Parse("MATCH (a:P {x: $x})-[:R*1..3]->(b) WHERE b.y > 1 WITH a, count(*) AS c ORDER BY c DESC LIMIT 5 RETURN a, c")
			}
			done <- err
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func BenchmarkParse(b *testing.B) {
	const q = `MATCH (a:Person {name: $n})-[:KNOWS]->(b) WHERE b.age > 30 AND a.x = 1 RETURN b.name, count(*) ORDER BY b.name LIMIT 10`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := cypher.Parse(q); err != nil {
			b.Fatal(err)
		}
	}
}
