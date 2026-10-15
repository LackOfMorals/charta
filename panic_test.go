package graingraph_test

import (
	"context"
	"testing"
	"time"

	"github.com/graingraph/graingraph"
	"github.com/graingraph/graingraph/cypher/proc"
)

func registerBoom(db *graingraph.DB) {
	db.RegisterProcedure(&proc.Procedure{
		Signature: proc.Signature{Name: "test.boom"},
		Fn:        func(context.Context, []any) ([]map[string]any, error) { panic("boom") },
	})
}

// A panic inside the interpreter must fail the statement, not wedge the writer.
func TestPanicDoesNotLeakTransaction(t *testing.T) {
	db := openDB(t)
	registerBoom(db)
	ctx := context.Background()
	if _, err := db.RunQuery(ctx, `CREATE (:A) WITH 1 AS x CALL test.boom()`, nil); err == nil {
		t.Fatal("expected an error from the panicking procedure")
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := db.RunQuery(wctx, `CREATE (:B)`, nil); err != nil {
		t.Fatalf("write after panic: %v", err)
	}
	res, err := db.RunQuery(wctx, `MATCH (n) RETURN count(n) AS c`, nil)
	if err != nil {
		t.Fatal(err)
	}
	recs, _ := res.Collect(wctx)
	if got := recs[0].Values()[0]; got != int64(1) {
		t.Fatalf("panicked statement's writes must roll back; count = %v", got)
	}
}

// A panic in an explicit transaction rolls it back and closes it.
func TestPanicInExplicitTxRollsBack(t *testing.T) {
	db := openDB(t)
	registerBoom(db)
	ctx := context.Background()
	tx, err := db.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Run(ctx, `CREATE (:A) WITH 1 AS x CALL test.boom()`, nil); err == nil {
		t.Fatal("expected error")
	}
	if err := tx.Commit(); err == nil {
		t.Fatal("commit after a panicked statement must fail")
	}
	res, _ := db.RunQuery(ctx, `MATCH (n) RETURN count(n)`, nil)
	recs, _ := res.Collect(ctx)
	if recs[0].Values()[0] != int64(0) {
		t.Fatalf("rows persisted: %v", recs[0].Values()[0])
	}
}

func TestInterpreterInputValidation(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	bad := []string{
		`RETURN abs()`, `RETURN head()`, `RETURN toInteger()`, `RETURN labels()`,
		`RETURN duration({days: 1}) / (1.0/0.0)`,
		`RETURN point({x: 0.0/0.0, y: 1.0})`,
		`CREATE (:`+"`A,B`"+`)`,
	}
	for _, q := range bad[:4] {
		if _, err := db.RunQuery(ctx, q, nil); err == nil {
			t.Errorf("%s: expected an error", q)
		}
	}
	for _, q := range bad[4:] {
		res, err := db.RunQuery(ctx, q, nil)
		if err == nil {
			_, err = res.Collect(ctx)
		}
		if err == nil {
			t.Errorf("%s: expected an error", q)
		}
	}
	res, err := db.RunQuery(ctx, `RETURN substring('abc', 1, 9223372036854775807) AS s`, nil)
	if err != nil {
		t.Fatal(err)
	}
	recs, _ := res.Collect(ctx)
	if recs[0].Values()[0] != "bc" {
		t.Fatalf("got %v", recs[0].Values()[0])
	}
}
