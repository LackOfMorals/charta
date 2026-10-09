package graphlite_test

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/LackOfMorals/graphlite/v2"
)

func openFileDB(t *testing.T, opts ...graphlite.Option) (*graphlite.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "g.db")
	db, err := graphlite.Open(path, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(context.Background()) })
	return db, path
}

func count(t testing.TB, db *graphlite.DB, q string) int64 {
	t.Helper()
	res, err := db.RunQuery(context.Background(), q, nil)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	rec, err := res.Single(context.Background())
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	n, _ := rec.Values()[0].(int64)
	return n
}

// A reader must not wait for a writer that holds a transaction open: the
// explicit transaction owns the only write connection, so this would block
// forever if reads ran on it.
func TestReadsDoNotWaitForAnOpenWriteTransaction(t *testing.T) {
	ctx := context.Background()
	db, _ := openFileDB(t)
	if _, err := db.RunQuery(ctx, "CREATE (:Seed)", nil); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Run(ctx, "CREATE (:Uncommitted)", nil); err != nil {
		t.Fatal(err)
	}

	done := make(chan int64, 1)
	go func() { done <- count(t, db, "MATCH (n) RETURN count(n)") }()
	select {
	case n := <-done:
		if n != 1 {
			t.Errorf("reader saw %d nodes, want 1 (the uncommitted node must be invisible)", n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a read blocked behind an open write transaction")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, "MATCH (n) RETURN count(n)"); n != 2 {
		t.Errorf("after commit a reader sees %d nodes, want 2", n)
	}
}

func TestConcurrentReadsAndWrites(t *testing.T) {
	ctx := context.Background()
	db, _ := openFileDB(t, graphlite.WithBusyTimeout(5*time.Second))
	const writes = 150

	stop := make(chan struct{})
	var readers sync.WaitGroup
	errs := make(chan error, 16)
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			var last int64
			for {
				select {
				case <-stop:
					return
				default:
				}
				// Atomicity: every Pair has exactly two nodes, never one.
				res, err := db.RunQuery(ctx, "MATCH (p:Pair) RETURN p.i AS i, count(*) AS c", nil)
				if err != nil {
					errs <- err
					return
				}
				recs, err := res.Collect(ctx)
				if err != nil {
					errs <- err
					return
				}
				for _, rec := range recs {
					if c, _ := rec.Values()[1].(int64); c != 2 {
						errs <- fmt.Errorf("pair %v seen with %d node(s): a write was observed half-applied", rec.Values()[0], c)
						return
					}
				}
				// Committed data never disappears for a reader.
				if n := int64(len(recs)); n < last {
					errs <- fmt.Errorf("reader went backwards: %d then %d pairs", last, n)
					return
				} else {
					last = n
				}
			}
		}()
	}
	for i := 0; i < writes; i++ {
		if _, err := db.RunQuery(ctx, "CREATE (:Pair {i: $i}), (:Pair {i: $i})", map[string]any{"i": int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	readers.Wait()
	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}
	if n := count(t, db, "MATCH (p:Pair) RETURN count(p)"); n != 2*writes {
		t.Errorf("final count %d, want %d", n, 2*writes)
	}
}

func TestReadOnlyDatabaseServesReadsFromThePool(t *testing.T) {
	ctx := context.Background()
	db, path := openFileDB(t)
	if _, err := db.RunQuery(ctx, "CREATE (:A {x: 1})", nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(ctx); err != nil {
		t.Fatal(err)
	}
	ro, err := graphlite.Open(path, graphlite.WithReadOnly())
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close(ctx)
	if n := count(t, ro, "MATCH (a:A) RETURN count(a)"); n != 1 {
		t.Errorf("read-only database returned %d", n)
	}
	if _, err := ro.RunQuery(ctx, "CREATE (:B)", nil); err == nil {
		t.Error("a write on a read-only database must fail")
	}
}

func TestMaxReadConnsOption(t *testing.T) {
	ctx := context.Background()
	db, _ := openFileDB(t, graphlite.WithMaxReadConns(2))
	if _, err := db.RunQuery(ctx, "UNWIND range(1, 100) AS i CREATE (:N {i: i})", nil); err != nil {
		t.Fatal(err)
	}
	// More concurrent readers than connections: they queue, none fail.
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if n := count(t, db, "MATCH (n:N) RETURN count(n)"); n != 100 {
				t.Errorf("got %d", n)
			}
		}()
	}
	wg.Wait()
}

func TestCloseDoesNotLeakGoroutines(t *testing.T) {
	ctx := context.Background()
	runtime.GC()
	before := runtime.NumGoroutine()
	for i := 0; i < 50; i++ {
		path := filepath.Join(t.TempDir(), "g.db")
		db, err := graphlite.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.RunQuery(ctx, "CREATE (:A)", nil); err != nil {
			t.Fatal(err)
		}
		count(t, db, "MATCH (a) RETURN count(a)")
		if err := db.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(100 * time.Millisecond)
	runtime.GC()
	if after := runtime.NumGoroutine(); after > before+3 {
		t.Errorf("goroutines grew from %d to %d across 50 open/close cycles", before, after)
	}
}

func TestMemoryDatabaseIsUnchanged(t *testing.T) {
	ctx := context.Background()
	db, err := graphlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	if _, err := db.RunQuery(ctx, "CREATE (:A)", nil); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, "MATCH (a:A) RETURN count(a)"); n != 1 {
		t.Errorf("got %d", n)
	}
}
