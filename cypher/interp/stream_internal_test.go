package interp

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/graingraph/graingraph/cypher/analyze"
	"github.com/graingraph/graingraph/cypher/syntax"
	"github.com/graingraph/graingraph/store"
)

// fmtVal renders a result value for comparison: nodes, relationships and paths
// by id, lists and maps recursively, everything else with its type.
func fmtVal(v any) string {
	switch x := v.(type) {
	case *Node:
		return fmt.Sprintf("n%d", x.ID)
	case *Rel:
		return fmt.Sprintf("r%d", x.ID)
	case *Path:
		var sb strings.Builder
		sb.WriteString("p[")
		for i, n := range x.Nodes {
			if i > 0 {
				sb.WriteString(fmt.Sprintf("-r%d-", x.Rels[i-1].ID))
			}
			sb.WriteString(fmt.Sprintf("n%d", n.ID))
		}
		return sb.String() + "]"
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = fmtVal(e)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k + ":" + fmtVal(x[k])
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	return fmt.Sprintf("%T:%v", v, v)
}

// runOrdered runs q and returns its rows in output order, or the error text.
func runOrdered(t *testing.T, db *store.SQLiteStore, q string, params map[string]any) (rows []string, errText string) {
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
		return nil, err.Error()
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Rows {
		parts := make([]string, len(r))
		for i, v := range r {
			parts[i] = fmtVal(v)
		}
		rows = append(rows, strings.Join(parts, "|"))
	}
	return append([]string{"cols:" + strings.Join(res.Columns, ",")}, rows...), ""
}

// richGraph builds a graph with every kind of property value and some missing
// ones, parallel relationships and self-loops, in a fresh in-memory database.
func richGraph(t *testing.T, seed int64, nodes, rels int) *store.SQLiteStore {
	t.Helper()
	db, err := store.Open(":memory:", store.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	runInternal(t, db, fmt.Sprintf(`UNWIND range(1, %d) AS i CREATE (:N {i: i, f: i * 0.5, s: 's' + (i %% 7), b: i %% 3 = 0, g: i %% 5, l: [i %% 4, i %% 2]})`, nodes), nil)
	runInternal(t, db, fmt.Sprintf(`UNWIND range(1, %d) AS i CREATE (:M {i: i + 100000, s: 's' + (i %% 3), g: i %% 4})`, nodes/3), nil)
	// Some nodes lack properties, so grouping and aggregation meet nulls.
	runInternal(t, db, `MATCH (n:N) WHERE n.i % 11 = 0 REMOVE n.f`, nil)
	runInternal(t, db, `MATCH (n:N) WHERE n.i % 13 = 0 REMOVE n.s`, nil)
	r := rand.New(rand.NewSource(seed))
	// All the relationships in one statement, picking the ends out of the list
	// of nodes by position (node i is at index i-1), so building the graph is one
	// scan, not one scan per relationship.
	pairs := make([]string, rels)
	for k := range pairs {
		pairs[k] = fmt.Sprintf("[%d, %d, '%s', %d]", r.Intn(nodes), r.Intn(nodes), []string{"R", "S"}[r.Intn(2)], r.Intn(5))
	}
	if rels > 0 {
		runInternal(t, db, fmt.Sprintf(`MATCH (n:N) WITH n ORDER BY n.i WITH collect(n) AS ns UNWIND [%s] AS p WITH ns[p[0]] AS a, ns[p[1]] AS b, p CREATE (a)-[:R2 {t: p[2], w: p[3]}]->(b)`, strings.Join(pairs, ",")), nil)
	}
	return db
}

var streamQueries = []string{
	// bounded top-N: ties, direction, nulls, several keys, SKIP, DISTINCT, keys not projected
	"MATCH (n:N) RETURN n.i AS i, n.g AS g ORDER BY g LIMIT 9",
	"MATCH (n:N) RETURN n.i AS i, n.g AS g ORDER BY g DESC LIMIT 9",
	"MATCH (n:N) RETURN n.i AS i, n.s AS s ORDER BY s LIMIT 12",
	"MATCH (n:N) RETURN n.i AS i, n.f AS f ORDER BY f DESC, i LIMIT 6",
	"MATCH (n:N) RETURN n.i AS i ORDER BY n.g, n.i DESC SKIP 4 LIMIT 6",
	"MATCH (n:N) RETURN n.i AS i ORDER BY n.s DESC SKIP 3 LIMIT 5",
	"MATCH (n:N) RETURN DISTINCT n.g AS g ORDER BY g DESC LIMIT 3",
	"MATCH (n:N) RETURN DISTINCT n.s AS s ORDER BY s LIMIT 4",
	"MATCH (n:N) RETURN n ORDER BY n.g, n.i LIMIT 5",
	"MATCH (n:N) RETURN n.i AS i ORDER BY i LIMIT 1000000",
	"MATCH (n:N) RETURN n.i AS i ORDER BY i SKIP 2 LIMIT 0",
	"MATCH (n:N) WITH n.i AS i, n.g AS g ORDER BY g, i DESC LIMIT 8 WHERE i % 2 = 0 RETURN i, g",
	"MATCH (n:N) WITH n ORDER BY n.i DESC LIMIT 4 MATCH (n)-[r:R2]->(m) RETURN n.i AS a, m.i AS b",
	"MATCH (a:N)-[r:R2]->(b) RETURN a.i AS x, b.i AS y ORDER BY r.w DESC, x LIMIT 7",
	"MATCH (n:N) RETURN n.i AS i ORDER BY n.i / (n.g - 2) LIMIT 5",
	"MATCH (n:N) RETURN n.i AS i ORDER BY n.i / (n.g - 2) LIMIT 5 + 'x'",
	// plain reads, filters, projections
	"MATCH (n:N) RETURN n.i AS i ORDER BY i LIMIT 5",
	"MATCH (n:N) RETURN n.i AS i, n.s AS s",
	"MATCH (n:N) WHERE n.i % 4 = 0 RETURN n.i AS i, n.f AS f",
	"MATCH (n) RETURN labels(n) AS l, n.i AS i",
	"MATCH (n:N) RETURN n.i AS i LIMIT 7",
	"MATCH (n:N) RETURN n.i AS i SKIP 3 LIMIT 7",
	"MATCH (n:N) RETURN n.i AS i SKIP 5",
	"MATCH (n:N) RETURN n.i AS i LIMIT 0",
	"MATCH (n:N) RETURN n.i AS i SKIP 100000",
	"MATCH (n:N) RETURN n.i AS i SKIP 2 LIMIT 3",
	"MATCH (n:N) WITH n.i AS i LIMIT 4 RETURN i",
	"MATCH (n:N) WITH n.i AS i SKIP 2 LIMIT 4 RETURN i * 2 AS d",
	"MATCH (n:N) WITH n.i AS i LIMIT 5 WHERE i > 2 RETURN i",
	"MATCH (n:N) WITH n.i AS i SKIP 1 LIMIT 5 WHERE i % 2 = 0 RETURN i",
	"MATCH (n:N) WITH n LIMIT 3 MATCH (n)-[r:R2]->(m) RETURN n.i AS a, m.i AS b",
	"MATCH (n:N) WITH n.i AS i LIMIT 6 RETURN i LIMIT 2",
	"MATCH (n:N) RETURN * LIMIT 3",
	"MATCH (n:N) WITH * WHERE n.i < 6 RETURN n.i AS i",
	"MATCH (n:N) WITH n, n.i AS i WHERE i < 8 RETURN *",
	"MATCH (n:N) RETURN n.l AS l LIMIT 5",
	"MATCH (n:N) RETURN {a: n.i, b: n.s} AS m LIMIT 4",
	"MATCH (n:N) RETURN n.i + n.g AS x ORDER BY x DESC, n.i LIMIT 6",
	"UNWIND range(1, 20) AS i RETURN i, i * i AS sq LIMIT 8",
	"UNWIND [1, 2, 3] AS i UNWIND [10, 20] AS j RETURN i, j",
	"UNWIND [] AS i RETURN i",
	"UNWIND range(1, 6) AS i WITH i WHERE i % 2 = 1 RETURN i",
	"RETURN 1 AS x",
	"RETURN 1 + 1 AS x, 'a' AS y LIMIT 1",

	// traversals, several MATCH clauses, OPTIONAL MATCH
	"MATCH (a:N)-[r:R2]->(b:N) RETURN a.i AS a, b.i AS b, r.w AS w",
	"MATCH (a:N)-[r:R2]->(b:N) WHERE r.w > 2 RETURN a.i AS a, b.i AS b LIMIT 9",
	"MATCH (a:N)-[r:R2]->(b:N) RETURN a.i AS a, b.i AS b LIMIT 5",
	"MATCH (a:N)-[:R2]->(b:N)-[:R2]->(c:N) RETURN a.i AS a, c.i AS c LIMIT 12",
	"MATCH (a:N)-[:R2]-(b:N) RETURN a.i AS a, b.i AS b LIMIT 10",
	"MATCH (a:N)<-[:R2]-(b:N) RETURN a.i AS a, b.i AS b ORDER BY a, b LIMIT 8",
	"MATCH (a:N) MATCH (a)-[:R2]->(b) RETURN a.i AS a, b.i AS b LIMIT 8",
	"MATCH (a:N) MATCH (b:M) RETURN a.i AS a, b.i AS b LIMIT 15",
	"MATCH (a:N) WHERE a.i < 30 MATCH (b:M) WHERE b.g = a.g RETURN a.i AS a, b.i AS b ORDER BY a, b LIMIT 10",
	"MATCH (a:N) OPTIONAL MATCH (a)-[:R2]->(b:M) RETURN a.i AS a, b.i AS b LIMIT 20",
	"MATCH (a:N) OPTIONAL MATCH (a)-[r:R2]->(b) WHERE r.w = 99 RETURN a.i AS a, b LIMIT 6",
	"MATCH (a:N) OPTIONAL MATCH (a)-[:R2]->(b) RETURN a.i AS a, count(b) AS k ORDER BY a LIMIT 10",
	"MATCH (a:N) WHERE EXISTS { (a)-[:R2]->() } RETURN a.i AS a LIMIT 7",
	"MATCH (a:N) WHERE NOT EXISTS { (a)-[:R2]->() } RETURN a.i AS a LIMIT 7",
	"MATCH (a:N) WHERE (a)-[:R2]->(:N) RETURN a.i AS a LIMIT 7",
	"MATCH (a:N) RETURN a.i AS a, [(a)-[:R2]->(b) | b.i] AS bs LIMIT 6",
	"MATCH (a:N) RETURN a.i AS a, size([(a)-[:R2]->(b) | b]) AS k ORDER BY k DESC, a LIMIT 6",
	"MATCH p = (a:N)-[:R2]->(b:N) RETURN length(p) AS l, a.i AS a LIMIT 6",
	"MATCH (a:N)-[:R2*1..2]->(b:N) RETURN a.i AS a, b.i AS b LIMIT 15",
	"MATCH (a:N {i: 5})-[:R2]->(b) RETURN b.i AS b",
	"MATCH (a:N) WITH a ORDER BY a.i LIMIT 3 MATCH (a)-[:R2]->(b) RETURN a.i AS a, b.i AS b",

	// An upstream WHERE narrows its own MATCH's scan only: a pattern in a later
	// projection that reuses the name must scan the whole label.
	"MATCH (a:N) WHERE a.i = 5 WITH 1 AS one RETURN size([(a:N)-[:R2]->(b) | b.i]) AS c",
	"MATCH (a:N) WHERE a.i = 5 WITH a.i AS i RETURN i, size([(a:N)-[:R2]->(b) | b.i]) AS c",
	"MATCH (a:N) WHERE a.i = 99999 WITH 1 AS one WHERE EXISTS { (a:N)-[:R2]->() } RETURN one",
	"MATCH (a:N) WHERE a.i = 99999 RETURN count(*) AS c",
	"MATCH (a:N {i: 3}) MATCH (b:N) WHERE b.i = 4 RETURN a.i AS a, b.i AS b",

	// aggregation
	"MATCH (n:N) RETURN count(*) AS c",
	"MATCH (n:N) RETURN count(n.f) AS c, count(n.s) AS d, count(DISTINCT n.g) AS e",
	"MATCH (n:N) RETURN sum(n.i) AS s, sum(n.f) AS f, avg(n.i) AS a, avg(n.f) AS af",
	"MATCH (n:N) RETURN min(n.i) AS lo, max(n.i) AS hi, min(n.s) AS ls, max(n.s) AS hs, min(n.f) AS lf",
	"MATCH (n:N) RETURN collect(n.g) AS gs",
	"MATCH (n:N) RETURN collect(DISTINCT n.g) AS gs, collect(DISTINCT n.s) AS ss",
	"MATCH (n:N) RETURN n.s AS s, count(*) AS c ORDER BY s",
	"MATCH (n:N) RETURN n.g AS g, n.s AS s, count(*) AS c, sum(n.i) AS t ORDER BY g, s",
	"MATCH (n:N) RETURN n.g AS g, collect(n.i) AS xs ORDER BY g",
	"MATCH (n:N) RETURN n.g AS g, avg(n.f) AS a, min(n.f) AS lo, max(n.f) AS hi ORDER BY g",
	"MATCH (n:N) RETURN n.b AS b, count(*) AS c ORDER BY b",
	"MATCH (n:N) RETURN stDev(n.i) AS sd, stDevP(n.i) AS sp",
	"MATCH (n:N) RETURN percentileCont(n.i, 0.5) AS m, percentileDisc(n.i, 0.9) AS p",
	"MATCH (n:N) RETURN n.g AS g, percentileDisc(n.i, 0.5) AS m ORDER BY g",
	"MATCH (n:N) WHERE n.i < 0 RETURN count(*) AS c",
	"MATCH (n:N) WHERE n.i < 0 RETURN sum(n.i) AS s, avg(n.i) AS a, min(n.i) AS lo, collect(n.i) AS xs, count(n) AS c",
	"MATCH (n:N) WHERE n.i < 0 RETURN n.g AS g, count(*) AS c",
	"MATCH (n:N) RETURN count(*) + 1 AS c, sum(n.i) * 2 AS s",
	"MATCH (n:N) RETURN n.g AS g, count(*) * 10 AS c ORDER BY g",
	"MATCH (n:N) RETURN n.g AS g, count(*) AS c ORDER BY c DESC, g LIMIT 3",
	"MATCH (n:N) RETURN n.g AS g, count(*) AS c ORDER BY c DESC, g SKIP 1 LIMIT 2",
	"MATCH (n:N) WITH n.g AS g, count(*) AS c WHERE c > 5 RETURN g, c ORDER BY g",
	"MATCH (n:N) WITH n.g AS g, count(*) AS c RETURN count(*) AS groups, sum(c) AS total",
	"MATCH (n:N) WITH n.g AS g, collect(n.i) AS xs UNWIND xs AS x RETURN g, x ORDER BY g, x LIMIT 12",
	"MATCH (n:N) WITH n.s AS s, count(*) AS c ORDER BY c DESC, s LIMIT 3 RETURN s, c",
	"MATCH (a:N)-[:R2]->(b:N) RETURN a.g AS g, count(*) AS c, count(DISTINCT b) AS d ORDER BY g",
	"MATCH (a:N)-[r:R2]->(b:N) RETURN r.t AS t, avg(r.w) AS w, max(b.i) AS m ORDER BY t",
	"MATCH (a:N)-[:R2]->(b) RETURN a.i AS a, count(b) AS k ORDER BY k DESC, a LIMIT 5",
	"MATCH (a:N)-[:R2]->(b:N)-[:R2]->(c:N) RETURN count(*) AS paths, count(DISTINCT c) AS ends",
	"MATCH (a:N) OPTIONAL MATCH (a)-[:R2]->(b) WITH a, count(b) AS k WHERE k = 0 RETURN count(a) AS isolated",
	"MATCH (n:N) RETURN n.g AS g, size(collect(n)) AS k ORDER BY g",
	"MATCH (n:N) RETURN max(n.s) AS m, min(n.l) AS l",
	"MATCH (n:N) RETURN n.f AS f, count(*) AS c ORDER BY f LIMIT 6",
	"UNWIND [1, 2, 3, 4, 5, 6] AS x RETURN x % 3 AS k, sum(x) AS s, collect(x) AS xs ORDER BY k",
	"UNWIND [1, 2.5, 3] AS x RETURN sum(x) AS s, avg(x) AS a",
	"UNWIND [1, null, 3] AS x RETURN count(x) AS c, count(*) AS r, sum(x) AS s, collect(x) AS xs",
	"UNWIND [] AS x RETURN count(x) AS c, sum(x) AS s, collect(x) AS xs",
	"UNWIND range(1, 50) AS x RETURN x % 5 AS k, count(*) AS c ORDER BY k",

	// grouping where the first row is not kept (every aggregate item is a bare
	// aggregate call) and where it is (an item mixes an aggregate with a variable)
	"MATCH (a:N)-[:R2]->(b:N) WITH b, count(a) AS k WHERE k > 1 RETURN b.i AS b, k ORDER BY k DESC, b LIMIT 8",
	"MATCH (a:N)-[:R2]->(b:N) WITH b, count(a) AS k RETURN b.s AS s, sum(k) AS t ORDER BY s",
	"MATCH (a:N)-[:R2]->(b:N) RETURN b.i AS k, b.i + count(a) AS x ORDER BY x DESC, k LIMIT 6",
	"MATCH (a:N)-[:R2]->(b:N) RETURN b.i AS k, count(a) * 2 + b.i AS x ORDER BY x DESC, k LIMIT 5",
	"MATCH (a:N)-[:R2]->(b:N) RETURN b.s AS s, b.i AS k, size(collect(a)) + b.i AS x ORDER BY x DESC, k LIMIT 5",
	"MATCH (a:N)-[:R2]->(b:N) RETURN b.i AS b, count(a) AS k, collect(a.i)[0] AS firstA ORDER BY k DESC, b LIMIT 5",
	"MATCH (a:N)-[:R2]->(b:N) RETURN b.g AS g, percentileDisc(a.i, 0.5) AS m, count(*) AS c ORDER BY g",
	"MATCH (n:N) RETURN n.g AS g, n.s AS s, count(*) AS c, max(n.i) AS hi ORDER BY g, s",
	"MATCH (n:N) RETURN n AS node, count(*) AS c ORDER BY node.i LIMIT 4",
	"MATCH (n:N) RETURN n.g + 1 AS k, count(DISTINCT n.s) AS d, [x IN collect(n.i) WHERE x < 5] AS small ORDER BY k",
	"MATCH (n:N) WITH n.g AS g, count(*) AS c ORDER BY c DESC, g LIMIT 2 RETURN g, c",
	// property-narrowed scans, paged
	"MATCH (n:N {g: 2}) RETURN n.i AS i ORDER BY i LIMIT 9",
	"MATCH (n:N) WHERE n.g = 3 RETURN count(*) AS c",
	"MATCH (n:N) WHERE n.g = 3 AND n.s = 's2' RETURN n.i AS i",
	"MATCH (n:N) WHERE n.g = 3 RETURN n.i AS i LIMIT 4",
	"MATCH (n:N) WHERE n.g = 99 RETURN n.i AS i",
	"MATCH (n:N {s: 's1'})-[:R2]->(m) RETURN m.i AS m LIMIT 7",
	"MATCH (n) WHERE n.g = 1 RETURN labels(n) AS l, n.i AS i",

	// DISTINCT and ORDER BY
	"MATCH (n:N) RETURN DISTINCT n.g AS g ORDER BY g",
	"MATCH (n:N) RETURN DISTINCT n.s AS s ORDER BY s",
	"MATCH (n:N) RETURN DISTINCT n.g AS g, n.s AS s ORDER BY g, s LIMIT 7",
	"MATCH (n:N) RETURN DISTINCT n.g AS g LIMIT 3",
	"MATCH (n:N) RETURN DISTINCT n.g AS g SKIP 1 LIMIT 2",
	"MATCH (a:N)-[:R2]->(b:N) RETURN DISTINCT b.g AS g ORDER BY g",
	"MATCH (a:N)-[:R2]->(b:N) WITH DISTINCT b.g AS g RETURN g ORDER BY g",
	"MATCH (n:N) RETURN n.i AS i ORDER BY n.s, n.i DESC LIMIT 6",
	"MATCH (n:N) RETURN n.s AS s ORDER BY n.i DESC LIMIT 4",
	"MATCH (n:N) WITH n ORDER BY n.i DESC LIMIT 5 RETURN n.i AS i",
	"MATCH (n:N) WITH n.g AS g, n.i AS i ORDER BY i DESC RETURN g, collect(i)[0..2] AS top ORDER BY g",
	"MATCH (n:N) RETURN n.i AS i ORDER BY i SKIP 10 LIMIT 5",
	"MATCH (n:N) RETURN n.i AS i, n.f AS f ORDER BY f, i LIMIT 9",

	// set-like and unions
	"MATCH (n:N) RETURN n.g AS g LIMIT 3 UNION MATCH (m:M) RETURN m.g AS g LIMIT 3",
	"MATCH (n:N) RETURN n.g AS g UNION ALL MATCH (m:M) RETURN m.g AS g",
	"MATCH (n:N) RETURN count(*) AS c UNION ALL MATCH (m:M) RETURN count(*) AS c",

	// queries that must not stream still agree (they take the other path)
	"MATCH (n:N) WITH n LIMIT 3 CALL { WITH n MATCH (n)-[:R2]->(m) RETURN count(m) AS k } RETURN n.i AS i, k",
	"MATCH (n:N) WHERE n.i < 4 CALL { MATCH (m:M) RETURN count(m) AS k } RETURN n.i AS i, k",
	"MATCH (n:N) WHERE n.i < 4 FOREACH (x IN [1] | SET n.tmp = x) RETURN count(n) AS c",
}

// bigNodes is the size of the graph the differential tests run on: more than
// one chunk of start nodes (startChunk), so chunk boundaries are crossed.
func bigNodes() int {
	if raceEnabled {
		return startChunk + 110
	}
	return 1300
}

// Streaming must give exactly the rows, in the order, and the errors the
// clause-at-a-time executor gives.
func TestStreamAgreesWithClauseAtATime(t *testing.T) {
	for _, size := range []struct {
		name        string
		nodes, rels int
	}{{"small", 70, 160}, {"across chunk boundaries", bigNodes(), bigNodes() * 3 / 2}} {
		t.Run(size.name, func(t *testing.T) {
			db := richGraph(t, 7, size.nodes, size.rels)
			before := streamUsed.Load()
			for _, q := range streamQueries {
				streamDisabled = false
				got, gerr := runOrdered(t, db, q, nil)
				streamDisabled = true
				want, werr := runOrdered(t, db, q, nil)
				streamDisabled = false
				if gerr != werr {
					t.Fatalf("%s\n streamed error: %q\n legacy error:   %q", q, gerr, werr)
				}
				if strings.Join(got, "\n") != strings.Join(want, "\n") {
					t.Fatalf("%s\n streamed: %.600v\n legacy:   %.600v", q, got, want)
				}
			}
			if used := streamUsed.Load() - before; used < int64(len(streamQueries))*2/3 {
				t.Fatalf("only %d of %d queries went through the pipeline", used, len(streamQueries))
			}
		})
	}
}

// With the caches trimmed after every chunk of start nodes, results are
// unchanged: nothing may depend on a cached node or relationship surviving.
func TestStreamWithConstantCacheTrimming(t *testing.T) {
	old := cacheLimit
	cacheLimit = 1
	t.Cleanup(func() { cacheLimit = old })
	db := richGraph(t, 11, bigNodes(), bigNodes()*3/2)
	for i, q := range streamQueries {
		if raceEnabled && i%3 != 0 {
			continue // a sample: the race detector makes every query 15-20 times slower
		}
		streamDisabled = false
		got, gerr := runOrdered(t, db, q, nil)
		streamDisabled = true
		want, werr := runOrdered(t, db, q, nil)
		streamDisabled = false
		if gerr != werr || strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("%s\n trimmed: %.400v / %q\n legacy:  %.400v / %q", q, got, gerr, want, werr)
		}
	}
}

// Errors raised by a streamed query are the ones the other executor raises.
// (A literal negative LIMIT is rejected by the analyzer before either runs, so
// those cases go in as parameters.)
func TestStreamErrorsMatch(t *testing.T) {
	db := richGraph(t, 3, 40, 60)
	for _, tc := range []struct {
		q      string
		params map[string]any
	}{
		{"MATCH (n:N) RETURN n.i / 0 AS x", nil},
		{"MATCH (n:N) RETURN sum(n.s) AS x", nil},
		{"MATCH (n:N) RETURN avg(n.s) AS x", nil},
		{"MATCH (n:N) RETURN n.i AS i LIMIT $n", map[string]any{"n": int64(-1)}},
		{"MATCH (n:N) RETURN n.i AS i SKIP $n", map[string]any{"n": int64(-2)}},
		{"MATCH (n:N) RETURN n.i AS i LIMIT $n", map[string]any{"n": "x"}},
		{"MATCH (n:N) WHERE n.i < 0 RETURN n.i AS i LIMIT $n", map[string]any{"n": int64(-1)}},
		{"UNWIND [9223372036854775807, 1] AS x RETURN sum(x) AS s", nil},
		{"MATCH (n:N) WITH n.i AS i WHERE i / 0 > 1 RETURN i", nil},
		{"UNWIND range(1, 3) AS x RETURN percentileCont(x, 2.0) AS p", nil},
		{"MATCH (n:N) RETURN toInteger(n.s) + 'a' AS x", nil},
		{"MATCH (n:N) RETURN $missing AS x", nil},
	} {
		streamDisabled = false
		got, gerr := runOrdered(t, db, tc.q, tc.params)
		streamDisabled = true
		want, werr := runOrdered(t, db, tc.q, tc.params)
		streamDisabled = false
		if gerr != werr || (gerr == "" && strings.Join(got, "\n") != strings.Join(want, "\n")) {
			t.Fatalf("%s\n streamed: %q %.200v\n legacy:   %q %.200v", tc.q, gerr, got, werr, want)
		}
	}
}

// A LIMIT stops the matcher feeding it: the query reads the first page of the
// label (startChunk rows), not all of it.
func TestStreamLimitStopsTheMatcher(t *testing.T) {
	db := richGraph(t, 5, 6000, 100)
	for _, c := range []struct {
		q       string
		maxRows int // rows read when streaming
	}{
		{"MATCH (n:N) RETURN n.i AS i LIMIT 3", startChunk},
		{"MATCH (n) RETURN n.i AS i LIMIT 3", startChunk},
		{"MATCH (n:N) WHERE n.i % 2 = 0 RETURN n.i AS i LIMIT 3", startChunk},
		{"MATCH (n:N) WITH n LIMIT 2 MATCH (n)-[:R2]->(m) RETURN count(m) AS c", startChunk},
		{"MATCH (n:N) RETURN n.i AS i SKIP 2500 LIMIT 3", startChunk + scanPageSize},
	} {
		streamDisabled = false
		start := scanRowsRead.Load()
		got, _ := runCounted(t, db, c.q, nil)
		streamed := scanRowsRead.Load() - start
		streamDisabled = true
		start = scanRowsRead.Load()
		want, _ := runCounted(t, db, c.q, nil)
		legacy := scanRowsRead.Load() - start
		streamDisabled = false
		t.Logf("%-72s read %5d rows streaming, %5d clause by clause", c.q, streamed, legacy)
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s: results differ: %v vs %v", c.q, got, want)
		}
		if streamed > int64(c.maxRows) {
			t.Errorf("%s: read %d rows, want at most %d", c.q, streamed, c.maxRows)
		}
		if legacy < 6000 {
			t.Errorf("%s: the clause-at-a-time executor read only %d rows; the test shows no saving", c.q, legacy)
		}
	}
}

// Writes after a read run in order and see exactly what they did before.
func TestStreamLeavesWritesAlone(t *testing.T) {
	for _, q := range []string{
		"MATCH (n:N) WHERE n.i < 6 SET n.mark = true RETURN count(n) AS c",
		"MATCH (n:N) WHERE n.i < 6 CREATE (:Copy {i: n.i}) RETURN count(*) AS c",
		"MATCH (n:N) WITH n.g AS g, count(*) AS c CREATE (:Tally {g: g, c: c}) RETURN count(*) AS c",
		"MATCH (n:N) WHERE n.i < 5 DETACH DELETE n RETURN count(*) AS c",
		"MATCH (n:N) WHERE n.i < 5 MERGE (m:Seen {i: n.i}) RETURN count(m) AS c",
		"UNWIND range(1, 5) AS i CREATE (:Fresh {i: i}) RETURN count(*) AS c",
	} {
		a := richGraph(t, 9, 40, 50)
		b := richGraph(t, 9, 40, 50)
		streamDisabled = false
		got, gerr := runOrdered(t, a, q, nil)
		after1, _ := runOrdered(t, a, "MATCH (n) RETURN labels(n) AS l, properties(n) AS p ORDER BY toString(n.i), toString(l)", nil)
		streamDisabled = true
		want, werr := runOrdered(t, b, q, nil)
		after2, _ := runOrdered(t, b, "MATCH (n) RETURN labels(n) AS l, properties(n) AS p ORDER BY toString(n.i), toString(l)", nil)
		streamDisabled = false
		if gerr != werr || strings.Join(got, "\n") != strings.Join(want, "\n") || strings.Join(after1, "\n") != strings.Join(after2, "\n") {
			t.Fatalf("%s\n result: %v / %v\n  legacy: %v / %v", q, got, gerr, want, werr)
		}
	}
}

// ORDER BY ... LIMIT keeps only the best rows, and must keep the rows (and their
// order among ties) the sort of the whole input gives.
func TestTopNBoundsTheRowsKept(t *testing.T) {
	db := richGraph(t, 5, bigNodes(), 0)
	before := topNUsed.Load()
	for _, q := range []string{
		"MATCH (n:N) RETURN n.i AS i, n.g AS g ORDER BY g LIMIT 20",
		"MATCH (n:N) RETURN n.i AS i, n.g AS g ORDER BY g DESC SKIP 7 LIMIT 20",
		"MATCH (n:N) RETURN DISTINCT n.g AS g ORDER BY g LIMIT 3",
	} {
		topNDisabled = true
		want, werr := runOrdered(t, db, q, nil)
		topNDisabled = false
		got, gerr := runOrdered(t, db, q, nil)
		if gerr != werr || strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s\n top-N: %.300v / %q\n whole:  %.300v / %q", q, got, gerr, want, werr)
		}
	}
	if topNUsed.Load()-before != 3 {
		t.Errorf("top-N ran %d times, want 3", topNUsed.Load()-before)
	}
}
