package graingraph_test

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDeepRuntimeValuesFailCleanly(t *testing.T) {
	db := openDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	build := `WITH reduce(acc = [], i IN range(1, 300000) | [acc]) AS v `
	for _, q := range []string{
		build + `RETURN count(DISTINCT v)`,
		build + `RETURN v = v`,
		build + `RETURN v IN [v]`,
		build + `UNWIND [v, [v]] AS x RETURN x ORDER BY x`,
		build + `UNWIND [v, [v]] AS x RETURN min(x)`,
		build + `RETURN v`,
		build + `RETURN [v] = [v] AS e`,
		build + `DELETE v`,
		build + `UNWIND [v, v] AS x RETURN DISTINCT x`,
		build + `RETURN {a: v}`,
	} {
		res, err := db.RunQuery(ctx, q, nil)
		if err == nil {
			_, err = res.Collect(ctx)
		}
		if err == nil {
			t.Errorf("%s: expected an error", q)
		} else if !strings.Contains(err.Error(), "nested") {
			t.Errorf("%s: unexpected error %v", q, err)
		}
	}
}

func TestHugeMatchPartCountRejected(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	q := "MATCH (a)"
	for i := 0; i < 20000; i++ {
		q += ",(a)"
	}
	if _, err := db.RunQuery(ctx, q+" RETURN 1", nil); err == nil {
		t.Fatal("expected an error")
	}
}
