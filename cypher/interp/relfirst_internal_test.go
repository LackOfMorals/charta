package interp

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/LackOfMorals/charta/cypher/analyze"
	"github.com/LackOfMorals/charta/cypher/syntax"
	"github.com/LackOfMorals/charta/store"
)

func runInternal(t *testing.T, db *store.SQLiteStore, q string, params map[string]any) []string {
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
	res, err := RunWith(ctx, tx, st, params, nil)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("%s: %v", q, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, r := range res.Rows {
		parts := make([]string, len(r))
		for i, v := range r {
			switch x := v.(type) {
			case *Node:
				parts[i] = fmt.Sprintf("n%d", x.ID)
			case *Rel:
				parts[i] = fmt.Sprintf("r%d", x.ID)
			case *Path:
				parts[i] = fmt.Sprintf("p%d-%d", len(x.Nodes), len(x.Rels))
			default:
				parts[i] = fmt.Sprint(v)
			}
		}
		rows = append(rows, strings.Join(parts, "|"))
	}
	sort.Strings(rows)
	return rows
}

// The relationship-first strategy must return exactly what the node-first one
// does, for every direction, with and without a type, labels on the ends, paths,
// WHERE and parameters.
func TestRelFirstAgreesWithNodeFirst(t *testing.T) {
	db, err := store.Open(":memory:", store.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := rand.New(rand.NewSource(4))
	runInternal(t, db, "UNWIND range(1, 40) AS i CREATE (:N {i: i, g: i % 3})", nil)
	runInternal(t, db, "UNWIND range(1, 12) AS i CREATE (:M {i: i})", nil)
	for k := 0; k < 160; k++ {
		a, b := r.Intn(52)+1, r.Intn(52)+1
		typ := []string{"R", "S"}[r.Intn(2)]
		runInternal(t, db, fmt.Sprintf("MATCH (a), (b) WHERE id(a) = %d AND id(b) = %d CREATE (a)-[:%s {w: %d, tag: '%s'}]->(b)",
			a, b, typ, r.Intn(5), []string{"x", "y"}[r.Intn(2)]), nil) // includes self-loops
	}
	queries := []string{
		"MATCH (a)-[r:R {w: 2}]->(b) RETURN a, r, b",
		"MATCH (a)<-[r:R {w: 2}]-(b) RETURN a, r, b",
		"MATCH (a)-[r:R {w: 2}]-(b) RETURN a, r, b",
		"MATCH (a)-[r {w: 3}]-(b) RETURN a, r, b",
		"MATCH (a:N)-[r:R {w: 1}]->(b:M) RETURN a, r, b",
		"MATCH (a)-[r:R|S {w: 1}]->(b) RETURN a, r, b",
		"MATCH (a)-[r:R]->(b) WHERE r.w = 4 RETURN a, r, b",
		"MATCH (a)-[r:R {w: 2, tag: 'x'}]->(b) RETURN a, r, b",
		"MATCH (a)-[r:R {w: $w}]->(b) RETURN a, r, b",
		"MATCH p = (a)-[r:S {w: 0}]-(b) RETURN p, a, b",
		"MATCH (a)-[r:R {w: 1}]->(a) RETURN a, r",
		"MATCH (a)-[:R {w: 1}]->(b) RETURN count(*) AS c",
		"MATCH (a)-[r:R {w: 1}]->(b), (b)-[r2:S {w: 2}]->(c) RETURN a, r, b, r2, c",
		"MATCH (x:M) MATCH (a)-[r:R {w: 1}]->(x) RETURN a, r, x",
		"MATCH (a)-[r:R {w: 99}]->(b) RETURN a",
		"OPTIONAL MATCH (a)-[r:R {w: 2}]->(b) RETURN a, r, b",
	}
	params := map[string]any{"w": int64(2)}
	usedBefore := relFirstUsed.Load()
	for _, q := range queries {
		relFirstDisabled = true
		want := runInternal(t, db, q, params)
		relFirstDisabled = false
		got := runInternal(t, db, q, params)
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s\n relationship-first (%d rows) != node-first (%d rows)", q, len(got), len(want))
		}
	}
	relFirstDisabled = false
	if relFirstUsed.Load() == usedBefore {
		t.Error("the relationship-first strategy was never chosen, so this test compared nothing")
	}
}
