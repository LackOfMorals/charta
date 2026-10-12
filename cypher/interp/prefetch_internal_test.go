package interp

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/LackOfMorals/charta/cypher/analyze"
	"github.com/LackOfMorals/charta/cypher/syntax"
	"github.com/LackOfMorals/charta/store"
)

// countingDB counts the SQL statements a query issues.
type countingDB struct {
	DB
	n *int
}

func (c countingDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	*c.n++
	return c.DB.QueryContext(ctx, q, args...)
}

func (c countingDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	*c.n++
	return c.DB.ExecContext(ctx, q, args...)
}

// runCounted runs q and reports how many SQL statements it took.
func runCounted(t *testing.T, db *store.SQLiteStore, q string, params map[string]any) ([]string, int) {
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
	var n int
	res, err := RunWith(ctx, countingDB{DB: tx, n: &n}, st, params, nil)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("%s: %v", q, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return formatRows(res), n
}

func runWithPrefetch(t *testing.T, on bool, db *store.SQLiteStore, q string, params map[string]any) ([]string, int) {
	t.Helper()
	prefetchDisabled = !on
	defer func() { prefetchDisabled = false }()
	return runCounted(t, db, q, params)
}

// Prefetching the adjacency of start nodes must not change any result. The
// graph is larger than startChunk, so chunk boundaries are crossed.
func TestStartPrefetchAgrees(t *testing.T) {
	queries := []string{
		"MATCH (a:N)-[r:R]->(b:N) RETURN a.i AS a, r, b.i AS b",
		"MATCH (a:N)<-[r:R]-(b:N) RETURN a.i AS a, r, b.i AS b",
		"MATCH (a:N)-[r]-(b:N) RETURN a.i AS a, r, b.i AS b",
		"MATCH (a:N)-[r:R|S]->(b) RETURN a.i AS a, type(r) AS t, b.i AS b",
		"MATCH (a:N)-[:S]->(b:N)-[:R]->(c:N) RETURN a.i AS a, b.i AS b, c.i AS c",
		"MATCH (a:N)-[:R]->(b)<-[:S]-(c:N) RETURN a.i AS a, b.i AS b, c.i AS c",
		"MATCH (a:N)-[:R*1..3]->(b:N) RETURN a.i AS a, b.i AS b",
		"MATCH (a:N)-[*2]-(b:N) RETURN count(*) AS c",
		"MATCH p = (a:N)-[:R]->(b:N)-[:R]->(c:N) RETURN p",
		"MATCH (a:N)-[r:R]->(b:N) WHERE a.i % 5 = 0 AND b.i % 3 = 0 RETURN a.i AS a, b.i AS b",
		"MATCH (a:N {i: 7})-[r]->(b) RETURN r, b.i AS b",
		"MATCH (a:N)-[r:R]->(a) RETURN a.i AS a, r",
		"MATCH (a:N)-[r:R]->(b:N) RETURN a.i AS a, count(r) AS k ORDER BY a",
		"MATCH (a:N) OPTIONAL MATCH (a)-[r:S]->(b:N) RETURN a.i AS a, r, b.i AS b",
		"MATCH (a:N)-[r1:R]->(b:N), (b)-[r2:S]->(c:N) RETURN a.i AS a, b.i AS b, c.i AS c",
		"MATCH (a:N)-[:R]->(b:N) WITH a, count(b) AS k WHERE k > 1 RETURN a.i AS a, k",
		"MATCH (a:N)-[r:R]->(b:N) RETURN a.i AS a, b.i AS b LIMIT 25",
		"MATCH (a:N)-[:R]->(b:N)-[:R]->(c:N)-[:R]->(d:N) RETURN count(*) AS c",
	}
	before := prefetchUsed.Load()
	for _, size := range []struct{ nodes, rels int }{{40, 90}, {700, 1500}} {
		db := randomGraph(t, int64(size.nodes), size.nodes, size.rels)
		for _, q := range queries {
			got, _ := runWithPrefetch(t, true, db, q, nil)
			want, _ := runWithPrefetch(t, false, db, q, nil)
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				// LIMIT without ORDER BY may pick different rows; only the count is defined.
				if strings.Contains(q, "LIMIT") && len(got) == len(want) {
					continue
				}
				t.Fatalf("%d nodes: %s\n prefetch: %v\n lazy:     %v", size.nodes, q, got, want)
			}
		}
	}
	if prefetchUsed.Load()-before < 20 {
		t.Fatalf("prefetching was barely exercised: %d", prefetchUsed.Load()-before)
	}
}

// Statements that change the graph while matching must behave the same with
// and without prefetching, including reading what they just wrote.
func TestStartPrefetchWithWrites(t *testing.T) {
	steps := []string{
		"MATCH (a:N)-[r:R]->(b:N) WHERE a.i % 3 = 0 DELETE r RETURN count(r) AS c",
		"MATCH (a:N)-[r:R]->(b:N) RETURN count(r) AS c",
		"MATCH (a:N)-[:S]->(b:N) WHERE a.i < 15 CREATE (a)-[:R]->(b) RETURN count(*) AS c",
		"MATCH (a:N)-[r:R]->(b:N) RETURN a.i AS a, r, b.i AS b",
		"MATCH (a:N)-[:R]->(b:N) CREATE (b)-[:T]->(a) RETURN count(*) AS c",
		"MATCH (a:N)-[:T]->(b:N)-[:R]->(c:N) RETURN a.i AS a, c.i AS c",
		"MATCH (a:N)-[r:T]->(b) SET b.seen = a.i RETURN count(*) AS c",
		"MATCH (a:N) WHERE a.seen IS NOT NULL RETURN a.i AS a, a.seen AS s",
		"MATCH (a:N)-[r]-(b:N) WHERE a.i = 4 DETACH DELETE a RETURN count(*) AS c",
		"MATCH (a:N)-[r]->(b:N) RETURN count(r) AS c",
	}
	for _, seed := range []int64{1, 2, 3} {
		on := randomGraph(t, seed, 30, 70)
		off := randomGraph(t, seed, 30, 70)
		for _, q := range steps {
			got, _ := runWithPrefetch(t, true, on, q, nil)
			want, _ := runWithPrefetch(t, false, off, q, nil)
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("seed %d: %s\n prefetch: %v\n lazy:     %v", seed, q, got, want)
			}
		}
	}
}

// A traversal over many start nodes costs a handful of statements, not one per
// node, and a selective start is not made worse. (The engine evaluates a MATCH
// fully before applying LIMIT, so prefetching never loads more than the lazy
// walk would have.)
func TestStartPrefetchRoundTrips(t *testing.T) {
	db := randomGraph(t, 11, 700, 1500)

	cases := []struct {
		name, q  string
		max      int // statements allowed with prefetching
		lazyOver int // statements the lazy walk needs at least
	}{
		// (A bare count(*) over one hop is answered by a single SQL statement
		// already, so these return rows.)
		{"one hop over all nodes", "MATCH (a:N)-[:R]->(b:N) RETURN a.i AS a, b.i AS b", 30, 600},
		{"two hops over all nodes", "MATCH (a:N)-[:R]->(b:N)-[:S]->(c:N) RETURN a.i AS a, c.i AS c", 40, 600},
		{"variable length", "MATCH (a:N)-[:R*1..3]->(b:N) RETURN a.i AS a, b.i AS b", 200, 600},
		{"limit 1", "MATCH (a:N)-[:R]->(b:N) RETURN b.i AS b LIMIT 1", 12, 600},
		// One matching start node: nothing else is prefetched.
		{"selective start", "MATCH (a:N {i: 5})-[:R]->(b:N) RETURN b.i AS b", 8, 0},
	}
	for _, c := range cases {
		got, n := runWithPrefetch(t, true, db, c.q, nil)
		want, lazy := runWithPrefetch(t, false, db, c.q, nil)
		t.Logf("%-26s prefetch %4d statements, lazy %4d", c.name, n, lazy)
		if n > c.max {
			t.Errorf("%s: %d statements with prefetching, want at most %d", c.name, n, c.max)
		}
		if lazy < c.lazyOver {
			t.Errorf("%s: lazy walk took only %d statements; the test no longer shows a saving", c.name, lazy)
		}
		if !strings.Contains(c.name, "limit") && strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s: results differ\n prefetch: %v\n lazy: %v", c.name, got, want)
		}
	}
}

// A frontier wider than maxPrefetchFrontier is not preloaded, but results are
// still correct.
func TestStartPrefetchFrontierCap(t *testing.T) {
	db, err := store.Open(":memory:", store.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	runInternal(t, db, "UNWIND range(1, 2) AS k CREATE (:H {k: k})", nil)
	n := maxPrefetchFrontier/2 + 500
	for k := 1; k <= 2; k++ {
		runInternal(t, db, fmt.Sprintf("MATCH (h:H {k: %d}) UNWIND range(1, %d) AS i CREATE (h)-[:R]->(:M {i: i})", k, n), nil)
	}
	q := "MATCH (h:H)-[:R]->(m:M) RETURN h.k AS k, m.i AS i"
	got, withCap := runWithPrefetch(t, true, db, q, nil)
	want, lazy := runWithPrefetch(t, false, db, q, nil)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("results differ: %v vs %v", got, want)
	}
	t.Logf("far frontier of %d nodes: %d statements with prefetching, %d lazy", 2*n, withCap, lazy)
	// Past the cap the far nodes load lazily, so the saving is only the adjacency.
	if withCap < n {
		t.Errorf("far nodes were preloaded beyond the cap: %d statements", withCap)
	}
}
