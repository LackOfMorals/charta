package interp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/LackOfMorals/charta/cypher/analyze"
	"github.com/LackOfMorals/charta/cypher/syntax"
	"github.com/LackOfMorals/charta/store"
)

// runWithEffects runs q in its own transaction (rolled back on error, as callers
// do) and returns its rows in order, its counters, or its error.
func runWithEffects(t *testing.T, db *store.SQLiteStore, q string) string {
	t.Helper()
	st, err := syntax.Parse(q)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	if err := analyze.Check(st); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	ctx := context.Background()
	tx, err := db.BeginExecTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	res, err := RunWith(ctx, tx, st, nil, nil)
	if err != nil {
		_ = tx.Rollback()
		return "error: " + err.Error()
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for _, r := range res.Rows {
		for _, v := range r {
			sb.WriteString(fmtVal(v) + "|")
		}
		sb.WriteByte('\n')
	}
	return fmt.Sprintf("%s%+v", sb.String(), res.Counters)
}

// Buffering creates must be invisible: the same ids, rows, counters, errors and
// final graph as writing each entity as it is created, including when the
// statement reads, changes or deletes what it has just created, fails halfway,
// or violates a constraint.
func TestBatchedCreatesAgreeWithRowAtATime(t *testing.T) {
	script := []string{
		"CREATE CONSTRAINT FOR (n:U) REQUIRE n.k IS UNIQUE",
		"UNWIND range(1, 50) AS i CREATE (:A:B {i: i, s: 'x' + i})",
		"UNWIND range(1, 30) AS i CREATE (:A {i: i})-[:R {w: i}]->(:C {i: -i})",
		"CREATE (a:P {n: 1}), (b:P {n: 2}), (a)-[:K]->(b), (b)<-[:L {z: 1}]-(a) RETURN a, b",
		"CREATE (a:P {n: 3}) WITH a MATCH (p:P) RETURN count(p) AS c",
		"CREATE (a:P {n: 4}) SET a.n = 5, a:Q RETURN a.n AS n, labels(a) AS l",
		"CREATE (a:P {n: 6}) DELETE a",
		"CREATE (a:P {n: 7}), (b:P {n: 8}), (a)-[r:K]->(b) DELETE r",
		"UNWIND range(1, 5) AS i CREATE (a:W {i: i}) WITH a WHERE a.i % 2 = 0 DETACH DELETE a",
		"UNWIND range(1, 2500) AS i CREATE (:Big {i: i})",
		"UNWIND range(1, 400) AS i CREATE (:Wide1:Wide2 {i: i})",
		"MATCH (n:Wide2) RETURN count(n) AS c",
		"CREATE ()",
		"CREATE ()-[:E]->()",
		"UNWIND range(1, 2100) AS i CREATE (:Big {i: i})-[:T]->(:Big2 {i: i})",
		"UNWIND range(1, 5) AS i CREATE (:U {k: i})",
		"UNWIND [1, 2, 6, 3] AS i CREATE (:U {k: i})",
		"UNWIND [1, 2, 3] AS i CREATE (:A {i: i}) WITH i RETURN 1 / (i - 2) AS x",
		"MATCH (a:A) WITH a LIMIT 3 CREATE (a)-[:M]->(:M {i: id(a)})",
		"CREATE (n:Z) RETURN id(n) AS id",
		"MATCH (n:A) WHERE n.i < 5 CREATE (m:A {i: n.i * 100}) RETURN count(m) AS c",
		"CREATE (:Lbl1:Lbl2:Lbl3 {a: 1, b: [1, 2], c: 1.5, d: true})",
		"CREATE ()",
		"MATCH (n) RETURN count(n) AS c",
		"MATCH ()-[r]->() RETURN count(r) AS c",
		"MATCH (n) RETURN id(n) AS id, labels(n) AS l, properties(n) AS p ORDER BY id",
		"MATCH (a)-[r]->(b) RETURN id(r) AS id, type(r) AS t, id(a) AS a, id(b) AS b, properties(r) AS p ORDER BY id",
		"MATCH (n:A:B) RETURN count(n) AS c",
		"MATCH (n:Lbl2) RETURN id(n) AS id",
	}
	// Without a constraint nothing reads the database when a statement ends, so
	// the buffer must be written by the statement itself.
	run := func(disabled, constrained bool) []string {
		batchDisabled = disabled
		defer func() { batchDisabled = false }()
		db, err := store.Open(":memory:", store.Config{})
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var out []string
		for _, q := range script {
			if !constrained && strings.Contains(q, ":U ") || !constrained && strings.Contains(q, "CONSTRAINT") {
				continue
			}
			out = append(out, q+"\n"+runWithEffects(t, db, q))
		}
		return out
	}
	before := batchedRows.Load()
	for _, constrained := range []bool{true, false} {
		b0 := batchedRows.Load()
		want := run(true, constrained)
		if batchedRows.Load() != b0 {
			t.Fatal("batchDisabled still batched")
		}
		got := run(false, constrained)
		if len(got) != len(want) {
			t.Fatalf("%d != %d results", len(got), len(want))
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("constrained=%v\n batched:\n%.600s\n row at a time:\n%.600s", constrained, got[i], want[i])
			}
		}
	}
	if batchedRows.Load()-before < 10000 {
		t.Errorf("only %d entities were written in batches", batchedRows.Load()-before)
	}
}

// The LabelsAdded counter reports a label that came into use, not each node
// that was given one.
func TestLabelsAddedCountsLabelsNotNodes(t *testing.T) {
	db, err := store.Open(":memory:", store.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, tc := range []struct {
		q    string
		want int
	}{
		{"UNWIND range(1, 5) AS i CREATE (:Fresh {i: i})", 1},
		{"UNWIND range(1, 5) AS i CREATE (:Fresh {i: i})", 0},
		{"CREATE (:Fresh)", 0},
		{"CREATE (:Fresh:Other)", 1},
		{"MATCH (n:Fresh) REMOVE n:Fresh", 0},
	} {
		got := runWithEffects(t, db, tc.q)
		if want := fmt.Sprintf("LabelsAdded:%d ", tc.want); !strings.Contains(got, want) {
			t.Errorf("%s\n got %s\n want %s", tc.q, got, want)
		}
	}
}

// A slice bound given as a whole-valued float (how some HTTP clients send an
// integer parameter) works; a fractional or non-numeric bound still fails.
func TestSliceBoundsAcceptWholeFloats(t *testing.T) {
	db, err := store.Open(":memory:", store.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	run := func(q string, params map[string]any) (string, bool) {
		st, err := syntax.Parse(q)
		if err != nil {
			t.Fatal(err)
		}
		if err := analyze.Check(st); err != nil {
			t.Fatal(err)
		}
		tx, err := db.BeginExecTx(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		res, err := RunWith(context.Background(), tx, st, params, nil)
		if err != nil {
			return err.Error(), false
		}
		return fmtVal(res.Rows[0][0]), true
	}
	for _, tc := range []struct {
		q    string
		p    any
		want string
		ok   bool
	}{
		{"RETURN [1,2,3][..$b] AS r", float64(2), "[int64:1,int64:2]", true},
		{"RETURN [1,2,3][..$b] AS r", float64(1000), "[int64:1,int64:2,int64:3]", true},
		{"RETURN [1,2,3][$b..] AS r", float64(1), "[int64:2,int64:3]", true},
		{"RETURN [1,2,3][..$b] AS r", int64(2), "[int64:1,int64:2]", true},
		{"RETURN [1,2,3][..$b] AS r", 1.5, "", false},
		{"RETURN [1,2,3][..$b] AS r", "x", "", false},
	} {
		got, ok := run(tc.q, map[string]any{"b": tc.p})
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("%s with %v(%T) = %q ok=%v, want %q ok=%v", tc.q, tc.p, tc.p, got, ok, tc.want, tc.ok)
		}
	}
}
