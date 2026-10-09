package graphlite

import (
	"context"
	"testing"
	"time"
)

// Option is a functional option for configuring a graphlite database.
// Pass one or more Options to [Open] to customise behaviour.
type Option func(*dbConfig)

type dbConfig struct {
	busyTimeout time.Duration
	readOnly    bool
	maxPathHops int
}

// WithBusyTimeout sets the SQLite busy_timeout pragma. When a write operation
// encounters a locked database, SQLite will retry for up to d before returning
// an error. The default (zero) uses SQLite's built-in behaviour (no retry).
//
// Useful when multiple goroutines or processes share the same database file.
func WithBusyTimeout(d time.Duration) Option {
	return func(c *dbConfig) { c.busyTimeout = d }
}

// WithReadOnly opens the database in read-only mode. Write queries issued via
// [DB.RunQuery] return [ErrReadOnly], and [DB.BeginTx] returns [ErrReadOnly]
// before any transaction is opened. The database file must already exist and
// contain the graphlite schema.
//
// Read-only enforcement is applied in the graphlite API layer: any Cypher
// statement that would mutate the graph (CREATE, SET, DELETE, MERGE) is
// rejected before reaching SQLite.
func WithReadOnly() Option {
	return func(c *dbConfig) { c.readOnly = true }
}

// WithMaxPathHops caps the number of hops in variable-length Cypher path
// patterns such as MATCH (a)-[*1..n]->(b). An unbounded pattern (-[*]->) stops
// at n hops, and an explicit upper bound above n returns an error.
//
// By default there is no cap (relationships are never reused within a path, so
// traversal always terminates, but it can take exponential time on dense
// graphs). Passing n <= 0 is a no-op.
func WithMaxPathHops(n int) Option {
	return func(c *dbConfig) {
		if n > 0 {
			c.maxPathHops = n
		}
	}
}

// NewTestDB opens an in-memory graphlite database, registers db.Close with
// t.Cleanup, and returns a ready-to-use *DB. t.Fatal is called on any error.
//
// This is the recommended way to create a graphlite database in tests:
//
//	func TestMyFeature(t *testing.T) {
//	    db := graphlite.NewTestDB(t)
//	    // db is closed automatically when the test ends
//	}
func NewTestDB(t testing.TB, opts ...Option) *DB {
	t.Helper()
	db, err := Open(":memory:", opts...)
	if err != nil {
		t.Fatalf("graphlite.NewTestDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return db
}
