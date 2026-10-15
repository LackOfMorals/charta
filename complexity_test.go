package graingraph_test

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestOrderByLongExpressionIsLinear(t *testing.T) {
	db := openDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	q := `UNWIND range(1, 2000) AS i RETURN i, count(*) ORDER BY i` + strings.Repeat(" + 1", 5000)
	start := time.Now()
	res, err := db.RunQuery(ctx, q, nil)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := res.Collect(ctx)
	if err != nil || len(recs) != 2000 {
		t.Fatalf("%d rows, err %v", len(recs), err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestAnalyzerOrderByGroupingIsLinear(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	p := "1" + strings.Repeat(" + 1", 5000)
	q := "RETURN " + p + " AS a, count(*) AS c ORDER BY " + "1" + strings.Repeat(" + 1", 5000)
	start := time.Now()
	if _, err := db.RunQuery(ctx, q, nil); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestLongStatementsObserveContext(t *testing.T) {
	db := openDB(t)
	for _, q := range []string{
		`UNWIND range(1, 4000000000) AS i RETURN count(i)`,
		`RETURN reduce(s = 0, i IN range(1, 9000000) | s + i * 2 + 1) AS r`,
		`UNWIND range(1, 9000000) AS i WITH i ORDER BY i DESC RETURN count(*)`,
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		start := time.Now()
		res, err := db.RunQuery(ctx, q, nil)
		if err == nil {
			_, err = res.Collect(ctx)
		}
		cancel()
		if err == nil {
			t.Errorf("%s: expected a cancellation error", q)
		}
		if d := time.Since(start); d > 15*time.Second {
			t.Errorf("%s: took %v after cancellation", q, d)
		}
	}
}

func TestRangeIsBounded(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	if _, err := db.RunQuery(ctx, `RETURN size(range(0, 4000000000))`, nil); err == nil {
		t.Fatal("expected an error")
	}
}

func TestValueGrowthIsBounded(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	big := strings.Repeat("x", 1<<20)
	for _, q := range []string{
		`RETURN size(reduce(s = 'x', i IN range(1, 40) | s + s))`,
		`RETURN size(reduce(l = [0], i IN range(1, 40) | l + l))`,
		`RETURN size(reduce(s = 'x', i IN range(1, 40) | s || s))`,
		`RETURN size(replace($s, '', $s))`,
	} {
		res, err := db.RunQuery(ctx, q, map[string]any{"s": big})
		if err == nil {
			_, err = res.Collect(ctx)
		}
		if err == nil {
			t.Errorf("%s: expected an error", q)
		}
	}
}

func TestResourceBoundsKeepStatementsWorking(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	collect := func(q string, params map[string]any) []any {
		t.Helper()
		res, err := db.RunQuery(ctx, q, params)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		recs, err := res.Collect(ctx)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if len(recs) == 0 {
			return nil
		}
		return recs[0].Values()
	}
	// many distinct regex patterns still match (the cache is only an optimisation)
	if got := collect(`UNWIND range(1, 3000) AS i RETURN count(CASE WHEN 'x5' =~ ('x' + toString(i)) THEN 1 END)`, nil)[0]; got != int64(1) {
		t.Fatalf("regex count = %v", got)
	}
	// a statement larger than the cacheable size runs fine, repeatedly
	big := `RETURN 1 /* ` + strings.Repeat("c", 200000) + ` */`
	for i := 0; i < 3; i++ {
		collect(big, nil)
	}
	// shortestPath over parallel relationships returns promptly
	collect(`CREATE (s:S)-[:R]->(m:M)-[:R]->(e:E)`, nil)
	collect(`MATCH (s:S), (m:M) UNWIND range(1, 300) AS i CREATE (s)-[:R]->(m)`, nil)
	collect(`MATCH (m:M), (e:E) UNWIND range(1, 300) AS i CREATE (m)-[:R]->(e)`, nil)
	if got := collect(`MATCH (s:S), (e:E) MATCH p = shortestPath((s)-[*2..]->(e)) RETURN length(p)`, nil)[0]; got != int64(2) {
		t.Fatalf("length = %v", got)
	}
}
