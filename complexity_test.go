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
