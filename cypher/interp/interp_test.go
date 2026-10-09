package interp_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/LackOfMorals/graphlite/v2/cypher/analyze"
	"github.com/LackOfMorals/graphlite/v2/cypher/interp"
	"github.com/LackOfMorals/graphlite/v2/cypher/proc"
	"github.com/LackOfMorals/graphlite/v2/cypher/syntax"
	"github.com/LackOfMorals/graphlite/v2/store"
)

// newDB opens an empty in-memory graph.
func newDB(t *testing.T) *store.SQLiteStore {
	t.Helper()
	s, err := store.Open(":memory:", store.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// run parses, analyses and executes q in its own transaction.
func run(db *store.SQLiteStore, eng *interp.Engine, q string, params map[string]any) (*interp.Result, error) {
	st, err := syntax.Parse(q)
	if err != nil {
		return nil, err
	}
	var procs *proc.Set
	if eng != nil {
		procs = &eng.Procs
	}
	if err := analyze.CheckWith(st, procs); err != nil {
		return nil, err
	}
	ctx := context.Background()
	tx, err := db.BeginExecTx(ctx)
	if err != nil {
		return nil, err
	}
	res, err := interp.RunWith(ctx, tx, st, params, eng)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return res, tx.Commit()
}

// render formats a value the way the TCK writes it, with maps key-sorted.
func render(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return "'" + x + "'"
	case float64:
		s := fmt.Sprint(x)
		if !strings.ContainsAny(s, ".eEN") {
			s += ".0"
		}
		return s
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = render(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k + ": " + render(x[k])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case *interp.Node:
		return fmt.Sprintf("(%s%s)", strings.Join(x.Labels, ":"), props(x.Props))
	case *interp.Rel:
		return fmt.Sprintf("[:%s%s]", x.Type, props(x.Props))
	}
	return fmt.Sprint(v)
}

func props(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	return " " + render(m)
}

// rows renders each result row as "v1|v2|…".
func rows(res *interp.Result) []string {
	var out []string
	for _, r := range res.Rows {
		parts := make([]string, len(r))
		for i, v := range r {
			parts[i] = render(v)
		}
		out = append(out, strings.Join(parts, "|"))
	}
	return out
}

func TestQueries(t *testing.T) {
	const people = `CREATE (a:P {name:'Alice', age:30}), (b:P {name:'Bob', age:25}), (c:P {name:'Carol'}),
		(a)-[:KNOWS {since:2020}]->(b), (b)-[:KNOWS]->(c)`
	tests := []struct {
		name  string
		setup string
		query string
		want  []string // exact, in order
		any   bool     // compare as a set instead of in order
	}{
		{name: "literal arithmetic", query: "RETURN 1 + 2 * 3 AS x, 7 / 2 AS i, 7 / 2.0 AS f, 2 ^ 3 AS p", want: []string{"7|3|3.5|8.0"}},
		{name: "three-valued logic", query: "RETURN null AND false AS a, null OR true AS b, null AND true AS c, NOT null AS d", want: []string{"false|true|null|null"}},
		{name: "null comparison", query: "RETURN 1 = null AS a, null = null AS b, 1 IN [1, null] AS c, 2 IN [1, null] AS d", want: []string{"null|null|true|null"}},
		{name: "unwind and range", query: "UNWIND range(1, 4) AS n RETURN n * n AS sq", want: []string{"1", "4", "9", "16"}},
		{name: "string functions", query: "RETURN toUpper('ab') + substring('hello', 1, 3) AS s, split('a,b', ',') AS l", want: []string{"'ABell'|['a', 'b']"}},
		{name: "list comprehension", query: "RETURN [x IN range(1, 5) WHERE x % 2 = 1 | x * 10] AS l", want: []string{"[10, 30, 50]"}},
		{name: "quantifiers", query: "RETURN all(x IN [1, 2] WHERE x > 0) AS a, any(x IN [1, 2] WHERE x > 1) AS b, none(x IN [1, 2] WHERE x > 5) AS c, single(x IN [1, 2] WHERE x > 1) AS d", want: []string{"true|true|true|true"}},
		{name: "reduce", query: "RETURN reduce(s = 0, x IN [1, 2, 3] | s + x) AS s", want: []string{"6"}},
		{name: "case", query: "UNWIND [1, 2, 3] AS n RETURN CASE WHEN n < 2 THEN 'low' WHEN n < 3 THEN 'mid' ELSE 'high' END AS c", want: []string{"'low'", "'mid'", "'high'"}},
		{name: "union", query: "RETURN 1 AS x UNION RETURN 1 AS x UNION RETURN 2 AS x", want: []string{"1", "2"}},
		{name: "union all", query: "RETURN 1 AS x UNION ALL RETURN 1 AS x", want: []string{"1", "1"}},
		{name: "match with properties", setup: people, query: "MATCH (n:P) WHERE n.age >= 25 RETURN n.name ORDER BY n.name", want: []string{"'Alice'", "'Bob'"}},
		{name: "missing property is null", setup: people, query: "MATCH (n:P {name:'Carol'}) RETURN n.age", want: []string{"null"}},
		{name: "traversal", setup: people, query: "MATCH (a:P {name:'Alice'})-[:KNOWS*1..2]->(x) RETURN x.name ORDER BY x.name", want: []string{"'Bob'", "'Carol'"}},
		{name: "relationship properties", setup: people, query: "MATCH ()-[r:KNOWS]->() WHERE r.since IS NOT NULL RETURN r.since", want: []string{"2020"}},
		{name: "optional match keeps rows", setup: people, query: "MATCH (n:P) OPTIONAL MATCH (n)-[:KNOWS]->(m) WHERE m.name = 'Bob' RETURN n.name, m.name ORDER BY n.name", want: []string{"'Alice'|'Bob'", "'Bob'|null", "'Carol'|null"}},
		{name: "aggregation", setup: people, query: "MATCH (n:P) RETURN count(*) AS c, count(n.age) AS ca, sum(n.age) AS s, avg(n.age) AS a, min(n.age) AS lo, max(n.age) AS hi", want: []string{"3|2|55|27.5|25|30"}},
		{name: "grouping and collect", setup: people, query: "MATCH (a:P)-[:KNOWS]->(b) RETURN a.name AS a, collect(b.name) AS bs ORDER BY a", want: []string{"'Alice'|['Bob']", "'Bob'|['Carol']"}},
		{name: "distinct skip limit", query: "UNWIND [3, 1, 3, 2, 1] AS n RETURN DISTINCT n ORDER BY n SKIP 1 LIMIT 1", want: []string{"2"}},
		{name: "with where", query: "UNWIND range(1, 5) AS n WITH n WHERE n > 3 RETURN n", want: []string{"4", "5"}},
		{name: "shortest path", setup: people, query: "MATCH p = shortestPath((a:P {name:'Alice'})-[*]->(c:P {name:'Carol'})) RETURN length(p)", want: []string{"2"}},
		{name: "pattern predicate", setup: people, query: "MATCH (n:P) WHERE (n)-[:KNOWS]->() RETURN n.name ORDER BY n.name", want: []string{"'Alice'", "'Bob'"}},
		{name: "exists subquery", setup: people, query: "MATCH (n:P) WHERE EXISTS { (n)<-[:KNOWS]-() } RETURN n.name ORDER BY n.name", want: []string{"'Bob'", "'Carol'"}},
		{name: "merge is idempotent", query: "MERGE (a:X {k:1}) MERGE (b:X {k:1}) RETURN count(*) AS c, a = b AS same", want: []string{"1|true"}},
		{name: "foreach", query: "FOREACH (i IN [1, 2, 3] | CREATE (:F {i: i}))", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newDB(t)
			if tt.setup != "" {
				if _, err := run(db, nil, tt.setup, nil); err != nil {
					t.Fatalf("setup: %v", err)
				}
			}
			res, err := run(db, nil, tt.query, nil)
			if err != nil {
				t.Fatal(err)
			}
			got := rows(res)
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCounters(t *testing.T) {
	db := newDB(t)
	res, err := run(db, nil, "CREATE (a:A:B {x: 1, y: 2})-[:R {w: 1}]->(b:B)", nil)
	if err != nil {
		t.Fatal(err)
	}
	c := res.Counters
	if c.NodesCreated != 2 || c.RelationshipsCreated != 1 || c.PropertiesSet != 3 || c.LabelsAdded != 2 {
		t.Errorf("create counters %+v", c)
	}

	// Overwriting a property is one added and one removed in net terms.
	res, err = run(db, nil, "MATCH (a:A) SET a.x = 5, a:C REMOVE a.y", nil)
	if err != nil {
		t.Fatal(err)
	}
	c = res.Counters
	if c.PropertiesSet != 1 || c.PropertiesRemoved != 2 || c.LabelsAdded != 1 {
		t.Errorf("update counters %+v", c)
	}

	res, err = run(db, nil, "MATCH (n) DETACH DELETE n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Counters.NodesDeleted != 2 || res.Counters.RelationshipsDeleted != 1 {
		t.Errorf("delete counters %+v", res.Counters)
	}
}

func TestRuntimeErrors(t *testing.T) {
	tests := []struct {
		name, setup, query, class, code string
	}{
		{name: "integer overflow", query: "RETURN 9223372036854775807 + 1", class: "ArithmeticError", code: "IntegerOverflow"},
		{name: "delete connected node", setup: "CREATE (:A)-[:R]->(:B)", query: "MATCH (a:A) DELETE a", class: "ConstraintVerificationFailed", code: "DeleteConnectedNode"},
		{name: "list of maps property", query: "CREATE (n {p: [{a: 1}]})", class: "TypeError", code: "InvalidPropertyType"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newDB(t)
			if tt.setup != "" {
				if _, err := run(db, nil, tt.setup, nil); err != nil {
					t.Fatal(err)
				}
			}
			_, err := run(db, nil, tt.query, nil)
			var ie *interp.Error
			if !errors.As(err, &ie) {
				t.Fatalf("got %v, want *interp.Error", err)
			}
			if ie.Class != tt.class || ie.Code != tt.code {
				t.Errorf("got %s/%s, want %s/%s", ie.Class, ie.Code, tt.class, tt.code)
			}
		})
	}
}

func TestErrorRollsBack(t *testing.T) {
	db := newDB(t)
	if _, err := run(db, nil, "CREATE (:Keep) WITH 1 AS x RETURN 9223372036854775807 + x", nil); err == nil {
		t.Fatal("expected overflow")
	}
	res, err := run(db, nil, "MATCH (n:Keep) RETURN count(*)", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := rows(res); len(got) != 1 || got[0] != "0" {
		t.Errorf("failed statement left data behind: %v", got)
	}
}

func TestParameters(t *testing.T) {
	db := newDB(t)
	res, err := run(db, nil, "UNWIND $xs AS x RETURN x + $d AS y", map[string]any{"xs": []any{int64(1), int64(2)}, "d": int64(10)})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rows(res), ","); got != "11,12" {
		t.Errorf("got %s", got)
	}
}

func TestProcedures(t *testing.T) {
	db := newDB(t)
	if _, err := run(db, nil, "CREATE (:Person), (:Pet {n: 1})-[:OWNED_BY]->(:Person)", nil); err != nil {
		t.Fatal(err)
	}

	res, err := run(db, nil, "CALL db.labels() YIELD label RETURN label ORDER BY label", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rows(res), ","); got != "'Person','Pet'" {
		t.Errorf("db.labels: %s", got)
	}
	res, err = run(db, nil, "CALL db.relationshipTypes()", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rows(res), ","); got != "'OWNED_BY'" {
		t.Errorf("db.relationshipTypes: %s", got)
	}

	eng := &interp.Engine{}
	eng.Procs.Register(&proc.Procedure{
		Signature: proc.Signature{
			Name:    "test.double",
			Inputs:  []proc.Param{{Name: "n", Type: "INTEGER"}},
			Outputs: []proc.Param{{Name: "out", Type: "INTEGER"}},
		},
		Fn: func(_ context.Context, args []any) ([]map[string]any, error) {
			return []map[string]any{{"out": args[0].(int64) * 2}}, nil
		},
	})
	res, err = run(db, eng, "UNWIND [1, 2] AS n CALL test.double(n) YIELD out RETURN out", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rows(res), ","); got != "2,4" {
		t.Errorf("test.double: %s", got)
	}
	// Standalone call with implicit parameter passing.
	res, err = run(db, eng, "CALL test.double", map[string]any{"n": int64(21)})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rows(res), ","); got != "42" {
		t.Errorf("implicit args: %s", got)
	}

	// Compile-time checks come from analysis, before anything runs.
	for q, code := range map[string]string{
		"CALL test.nope()":             "ProcedureNotFound",
		"CALL test.double()":           "InvalidNumberOfArguments",
		"CALL test.double(1, 2)":       "InvalidNumberOfArguments",
		"CALL test.double('x')":        "InvalidArgumentType",
		"WITH 1 AS n CALL test.double": "InvalidArgumentPassingMode",
	} {
		st, perr := syntax.Parse(q)
		if perr != nil {
			t.Fatalf("%s: %v", q, perr)
		}
		err := analyze.CheckWith(st, &eng.Procs)
		if _, got, ok := analyze.Describe(err); !ok || got != code {
			t.Errorf("%s: got %v, want %s", q, err, code)
		}
	}
}

// A key that keeps narrowing scans on a big enough graph gets an index, and the
// index is used (results are unchanged).
func TestAutomaticPropertyIndex(t *testing.T) {
	db := newDB(t)
	eng := &interp.Engine{}
	if _, err := run(db, eng, "UNWIND range(1, 600) AS i CREATE (:P {id: i, grp: i % 7})", nil); err != nil {
		t.Fatal(err)
	}
	indexes := func() int {
		var n int
		rows, err := db.DB().Query(`SELECT count(*) FROM sqlite_master WHERE name LIKE 'idx_auto_np_%'`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		rows.Next()
		rows.Scan(&n)
		return n
	}
	for i := 0; i < 4; i++ {
		res, err := run(db, eng, "MATCH (n:P) WHERE n.id = 300 RETURN n.grp", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := rows(res); len(got) != 1 || got[0] != "6" {
			t.Fatalf("run %d: %v", i, got)
		}
		if i < 2 && indexes() != 0 {
			t.Fatalf("index created too early (run %d)", i)
		}
	}
	if indexes() != 1 {
		t.Errorf("expected one automatic index, got %d", indexes())
	}
	// Keys that are not plain identifiers are never pushed down or indexed.
	if _, err := run(db, eng, "MATCH (n:P) WHERE n.`a b` = 1 RETURN n", nil); err != nil {
		t.Fatal(err)
	}
	if indexes() != 1 {
		t.Errorf("unexpected extra index")
	}
}

func TestMaxPathHops(t *testing.T) {
	db := newDB(t)
	if _, err := run(db, nil, "CREATE (:N {i:0})-[:R]->(:N {i:1})-[:R]->(:N {i:2})-[:R]->(:N {i:3})", nil); err != nil {
		t.Fatal(err)
	}
	eng := &interp.Engine{MaxPathHops: 2}
	res, err := run(db, eng, "MATCH (:N {i:0})-[:R*]->(n) RETURN n.i ORDER BY n.i", nil) // unbounded: capped at 2
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rows(res), ","); got != "1,2" {
		t.Errorf("capped traversal returned %s", got)
	}
	if _, err := run(db, eng, "MATCH (:N)-[:R*1..5]->(n) RETURN n", nil); err == nil {
		t.Error("an explicit bound above the cap must be an error")
	}
	res, err = run(db, nil, "MATCH (:N {i:0})-[:R*]->(n) RETURN n.i ORDER BY n.i", nil)
	if err != nil || strings.Join(rows(res), ",") != "1,2,3" {
		t.Errorf("uncapped: %v %v", rows(res), err)
	}
}
