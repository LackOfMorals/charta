package graphlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/LackOfMorals/graphlite/v2/cypher/interp"
	"github.com/LackOfMorals/graphlite/v2/store"
)

// Read-only statements run on read-only connections, which cannot create the
// automatic property indexes; the database builds them afterwards on the write
// connection.
func TestReadPathStillBuildsAutomaticIndexes(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	if !db.hasReadPool {
		t.Fatal("expected a read pool for a file database")
	}
	if _, err := db.RunQuery(ctx, "UNWIND range(1, 700) AS i CREATE (:P {key: i})", nil); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		res, err := db.RunQuery(ctx, "MATCH (n:P {key: $k}) RETURN n.key", map[string]any{"k": int64(i)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := res.Collect(ctx); err != nil {
			t.Fatal(err)
		}
	}
	sqlDB := db.st.(*store.SQLiteStore).DB()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var n int
		if err := sqlDB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name LIKE 'idx_auto_np_%'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no automatic index appeared (found %d)", n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStatementClassification(t *testing.T) {
	tests := map[string]bool{ // true = writes
		"MATCH (n) RETURN n":              false,
		"CREATE (:A)":                     true,
		"MATCH (n) SET n.x = 1":           true,
		"CALL { CREATE (:A) } RETURN 1":   true,
		"CREATE INDEX FOR (n:A) ON (n.x)": true,
		"SHOW INDEXES":                    false,
		"MATCH (n) WHERE EXISTS { (n)-->() } RETURN count(n)": false,
		"CALL db.labels() YIELD label RETURN label":           false,
		"UNWIND [1,2] AS x RETURN x":                          false,
	}
	for q, writes := range tests {
		st, err := parseSyntax(q, &interp.Engine{})
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if got := hasWrites(st); got != writes {
			t.Errorf("hasWrites(%q) = %v, want %v", q, got, writes)
		}
	}
}

// Two readers really are in flight at once: with one read transaction held
// open, another query still runs.
func TestSecondReaderRunsWhileFirstIsOpen(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	if _, err := db.RunQuery(ctx, "CREATE (:A)", nil); err != nil {
		t.Fatal(err)
	}
	held, err := db.st.BeginReadTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()
	var n int
	if err := held.QueryRowContext(ctx, `SELECT count(*) FROM nodes`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		res, err := db.RunQuery(ctx, "MATCH (a:A) RETURN count(a)", nil)
		if err == nil {
			_, err = res.Collect(ctx)
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a second reader blocked while another read transaction was open")
	}
}
