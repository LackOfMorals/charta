package interp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/LackOfMorals/charta/cypher/analyze"
	"github.com/LackOfMorals/charta/cypher/syntax"
	"github.com/LackOfMorals/charta/store"
)

// typedRows runs a query and renders every value with its Go type, so 1 and 1.0
// (or true and 1) cannot be mistaken for each other.
func typedRows(t *testing.T, db *store.SQLiteStore, q string) []string {
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
	defer tx.Rollback()
	res, err := RunWith(ctx, tx, st, map[string]any{"p": "p1"}, nil)
	if err != nil {
		return []string{"error: " + err.Error()}
	}
	var out []string
	for _, r := range res.Rows {
		parts := make([]string, len(res.Columns))
		for i := range res.Columns {
			parts[i] = fmt.Sprintf("%T:%v", r[i], r[i])
		}
		out = append(out, strings.Join(parts, "|"))
	}
	sort.Strings(out)
	return out
}

func TestAggregatePushdownAgrees(t *testing.T) {
	db, err := store.Open(":memory:", store.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Clean, mixed-type and awkward property values on several labels.
	for _, q := range []string{
		"UNWIND range(1, 60) AS i CREATE (:Req {status: 200 + i % 4, region: ['US', 'EU', 'SG'][i % 3], n: i, score: i / 4.0, ok: i % 2 = 0})",
		"UNWIND range(1, 20) AS i CREATE (:Req:Slow {status: 500, region: 'US', n: i * 10})",
		"UNWIND range(1, 10) AS i CREATE (:Mixed {v: CASE i % 5 WHEN 0 THEN 1 WHEN 1 THEN 1.0 WHEN 2 THEN 'x' WHEN 3 THEN true ELSE [i] END, k: i % 2})",
		"UNWIND range(1, 8) AS i CREATE (:Strs {s: ['b', 'a', 'c', 'a'][i % 4], k: i % 3})",
		"CREATE (:Empty), (:Req {region: 'US'}), (:Req {n: 1, status: null}), ()",
		"UNWIND range(1, 12) AS i CREATE (:Cl {ip: 'ip' + toString(i % 4), region: ['US', 'EU'][i % 2], n: i})",
		"UNWIND range(1, 40) AS i MATCH (c:Cl {n: i % 12 + 1}) CREATE (c)-[:MADE {ms: i * 3, ok: i % 3 = 0}]->(:Rq {status: 200 + i % 3 * 100, path: 'p' + toString(i % 5), n: i})",
		"MATCH (c:Cl {n: 1}), (r:Rq {n: 2}) CREATE (r)-[:MADE {ms: 7}]->(c), (c)-[:SELF]->(c)",
		"MATCH (a:Cl {n: 1}), (b:Cl {n: 2}) CREATE (a)-[:KNOWS]->(b), (b)-[:KNOWS]->(a), (a)-[:KNOWS]->(a)",
		"UNWIND range(1, 5) AS i CREATE (:Dates {d: date('2020-01-0' + toString(i)), k: i % 2})",
	} {
		runInternal(t, db, q, nil)
	}
	aggPushNodeProps = true
	defer func() { aggPushNodeProps = false }()
	type tc struct {
		q    string
		push bool // expected to be pushed down
	}
	cases := []tc{
		{"MATCH (r:Req) RETURN count(*) AS c", true},
		{"MATCH (r:Req) RETURN count(r) AS c, count(r.n) AS cn, count(r.status) AS cs", true},
		{"MATCH (r:Req) RETURN r.status AS status, count(*) AS n ORDER BY n DESC, status", true},
		{"MATCH (r:Req) RETURN r.region AS region, r.status AS status, count(*) AS n", true},
		{"MATCH (r:Req) RETURN r.region AS region, min(r.n) AS lo, max(r.n) AS hi, sum(r.n) AS s, avg(r.n) AS a", true},
		{"MATCH (r:Req) RETURN r.ok AS ok, count(*) AS n", true},
		{"MATCH (r:Req:Slow) RETURN count(*) AS c, sum(r.n) AS s", true},
		{"MATCH (r:Nothing) RETURN count(*) AS c, sum(r.n) AS s, min(r.n) AS m, avg(r.n) AS a", true},
		{"MATCH (r:Nothing) RETURN r.x AS x, count(*) AS c", true},
		{"MATCH (n) RETURN count(*) AS c", true},
		{"MATCH (r:Req) WITH r.region AS region, count(*) AS n WHERE n > 20 RETURN region, n", true},
		{"MATCH (r:Req) RETURN r.region AS region, count(*) AS n ORDER BY n DESC LIMIT 2", true},
		{"MATCH (r:Req) RETURN r.region AS region, count(*) AS n ORDER BY region SKIP 1", true},
		{"MATCH (s:Strs) RETURN s.k AS k, min(s.s) AS lo, max(s.s) AS hi", true},
		// One relationship.
		{"MATCH (c:Cl)-[:MADE]->(r:Rq) RETURN c.ip AS ip, count(r) AS n", true},
		{"MATCH (c:Cl)-[:MADE]->(r:Rq) RETURN c.ip AS ip, count(*) AS n ORDER BY n DESC, ip LIMIT 3", true},
		{"MATCH (c:Cl)-[m:MADE]->(r:Rq) RETURN c.region AS region, sum(m.ms) AS ms, min(m.ms) AS lo, max(r.status) AS hi", true},
		{"MATCH (r:Rq)<-[:MADE]-(c:Cl) RETURN r.path AS path, count(c) AS n", true},
		{"MATCH (r:Rq)<-[:MADE]-(c:Cl) RETURN c.region AS region, r.status AS status, count(*) AS n", true},
		{"MATCH (c:Cl)-[:MADE]->(r:Rq) WHERE r.status >= 300 RETURN c.region AS region, count(*) AS n", true},
		{"MATCH (c:Cl)-[m:MADE]->(r:Rq) WHERE r.status >= 300 AND m.ms < 90 AND c.region = 'EU' RETURN c.ip AS ip, count(*) AS n", true},
		{"MATCH (c:Cl)-[m:MADE]->(r:Rq) WHERE 200 < r.status RETURN count(*) AS n", true},
		{"MATCH (c:Cl)-[m:MADE]->(r:Rq) WHERE r.path = $p RETURN count(*) AS n", true},
		{"MATCH (c:Cl)-[m:MADE]->(r:Rq) WHERE c.region = 'EU' OR r.status = 200 RETURN count(*) AS n", false},
		{"MATCH (c:Cl)-[m:MADE]->(r:Rq) WHERE r.status <> 200 RETURN count(*) AS n", false},
		{"MATCH (c:Cl)-[m:MADE]->(r:Rq) WHERE r.status = 'x' RETURN count(*) AS n", true},
		{"MATCH (c:Cl)-[m:MADE]->(r:Rq) WHERE r.nope > 1 RETURN count(*) AS n", true},
		{"MATCH (a)-[:KNOWS]->(b) RETURN a.ip AS a, b.ip AS b, count(*) AS n", true},
		{"MATCH (a:Cl)-[e]->(b:Cl) RETURN type(e) AS t, count(*) AS n", false},
		{"MATCH (a:Cl)-[e:KNOWS|SELF]->(b) RETURN a.ip AS ip, count(*) AS n", true},
		{"MATCH (a:Cl)-[e]-(b:Cl) RETURN a.ip AS ip, count(*) AS n", false},
		{"MATCH (a)-[e]->(a) RETURN count(*) AS n", false},
		{"MATCH (a:Cl)-[e:MADE*1..2]->(b) RETURN count(*) AS n", false},
		{"MATCH (a:Cl)-[:MADE]->(b:Rq)-[:X]->(c) RETURN count(*) AS n", false},
		{"MATCH (a:Cl)-[:NOPE]->(b) RETURN a.ip AS ip, count(*) AS n", true},
		// Not eligible, or value kinds the SQL cannot reproduce: must give the same answer via the interpreter.
		{"MATCH (r:Req) RETURN r.score AS score, count(*) AS n", true},
		{"MATCH (r:Req) RETURN sum(r.score) AS s", false},
		{"MATCH (r:Req) RETURN avg(r.score) AS a", false},
		{"MATCH (r:Req) RETURN min(r.score) AS lo, max(r.score) AS hi", true},
		{"MATCH (m:Mixed) RETURN m.v AS v, count(*) AS n", false},
		{"MATCH (m:Mixed) RETURN m.k AS k, min(m.v) AS lo", false},
		{"MATCH (d:Dates) RETURN d.k AS k, count(*) AS n", true},
		{"MATCH (d:Dates) RETURN d.d AS d, count(*) AS n", false},
		{"MATCH (s:Strs) RETURN sum(s.s) AS s", false},
		{"MATCH (r:Req) RETURN count(DISTINCT r.region) AS c", false},
		{"MATCH (r:Req) WHERE r.n > 3 RETURN count(*) AS c", true},
		{"MATCH (r:Req {region: 'US'}) RETURN count(*) AS c", false},
		{"MATCH (r:Req) RETURN r.region AS region, collect(r.n) AS ns", false},
		{"MATCH (r:Req) RETURN r.region + 'x' AS region, count(*) AS n", false},
		{"MATCH (r:Req) RETURN r.region AS region, count(*) + 1 AS n", false},
		{"MATCH (r:Req) RETURN DISTINCT r.region AS region, count(*) AS n", true},
		{"MATCH (r:Req) RETURN r.status AS s, count(*) AS n ORDER BY s", true},
	}
	for _, c := range cases {
		before := aggPushUsed.Load()
		got := typedRows(t, db, c.q)
		used := aggPushUsed.Load() > before
		aggPushDisabled = true
		want := typedRows(t, db, c.q)
		aggPushDisabled = false
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s\n pushed:      %v\n interpreter: %v", c.q, got, want)
		}
		if used != c.push {
			t.Errorf("%s: pushed down = %v, want %v", c.q, used, c.push)
		}
	}
}

// Without aggPushNodeProps a single-node aggregation is only pushed down when it
// reads no property (count(*)), because that is where SQL beats the interpreter.
func TestAggregatePushdownSingleNodeDefault(t *testing.T) {
	db, err := store.Open(":memory:", store.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	runInternal(t, db, "UNWIND range(1, 30) AS i CREATE (:Req {status: 200 + i % 3})", nil)
	for q, want := range map[string]bool{
		"MATCH (r:Req) RETURN count(*) AS c":                      true,
		"MATCH (r:Req) RETURN count(r) AS c":                      true,
		"MATCH (r:Req) RETURN r.status AS s, count(*) AS c":       false,
		"MATCH (r:Req) WHERE r.status > 200 RETURN count(*) AS c": false,
		"MATCH (r:Req) RETURN sum(r.status) AS c":                 false,
	} {
		before := aggPushUsed.Load()
		got := typedRows(t, db, q)
		if used := aggPushUsed.Load() > before; used != want {
			t.Errorf("%s: pushed down = %v, want %v (%v)", q, used, want, got)
		}
	}
}
