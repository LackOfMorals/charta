package interp

import (
	"strings"
	"testing"
)

// Walking a pattern from its last node must return exactly what walking it from
// the first does, for every direction and shape.
func TestReversedPatternsAgree(t *testing.T) {
	queries := []string{
		"MATCH (a:N)-[r:R]->(b:N {i: 3}) RETURN a.i AS a, r, b.i AS b",
		"MATCH (a:N)<-[r:R]-(b:N {i: 3}) RETURN a.i AS a, r, b.i AS b",
		"MATCH (a)-[r]-(b {i: 3}) RETURN a.i AS a, r, b.i AS b",
		"MATCH (a)-[:R]->(b)-[:S]->(c {i: 2}) RETURN a.i AS a, b.i AS b, c.i AS c",
		"MATCH (a:N)-[:R|S]->(b)<-[:R]-(c:N) WHERE c.i = 4 RETURN a.i AS a, b.i AS b",
		"MATCH (a:N)-[:R*1..3]->(b {i: 5}) RETURN a.i AS a, b.i AS b",
		"MATCH (a:N)-[*2]-(b {i: 5}) RETURN a.i AS a, b.i AS b",
		"MATCH (a)-[r:R]->(b {i: 2})-[s:S]->(c) RETURN a.i AS a, r, s, c.i AS c",
		"MATCH (x:N {i: 1}) MATCH (a:N)-[r]->(x) RETURN a.i AS a, r",
		"MATCH (a:N)-[r]->(b:N) WHERE b.i = $k RETURN a.i AS a, r",
		"MATCH (a:N)-[:R]->(b:N {i: 3}) WHERE a.i < b.i RETURN a.i AS a, b.i AS b",
		"MATCH (a:N)-[:R]->(b:N {i: a.i}) RETURN a.i AS a",
		"OPTIONAL MATCH (a:N)-[r:R]->(b:N {i: 99}) RETURN a, r, b",
	}
	used := int64(0)
	for seed := int64(1); seed <= 5; seed++ {
		db := randomGraph(t, seed, 12, 30)
		for _, q := range queries {
			params := map[string]any{"k": int64(3)}
			reverseDisabled = false
			before := reverseUsed.Load()
			got := runInternal(t, db, q, params)
			used += reverseUsed.Load() - before
			reverseDisabled = true
			want := runInternal(t, db, q, params)
			reverseDisabled = false
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("seed %d: %s\n reversed:  %v\n forwards: %v", seed, q, got, want)
			}
		}
	}
	t.Logf("%d of %d runs walked a pattern backwards", used, 5*len(queries))
	if used < 5*8 {
		t.Fatalf("too few patterns were walked backwards: %d", used)
	}
}
