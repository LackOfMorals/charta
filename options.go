package charta

import (
	"context"
	"testing"
	"time"
)

// Option is a functional option for configuring a charta database.
// Pass one or more Options to [Open] to customise behaviour.
type Option func(*dbConfig)

// defaultReadConns is the read-pool size of a file-backed database.
//
// Two is deliberate. The pure-Go SQLite shares allocator and file-lock state
// between connections, so heavy concurrent scans slow each other down: on a
// 20,000-node scan two readers run 1.3x faster than one, but four run 0.6x as
// fast. Light queries (indexed lookups) gain about 1.8x with two and gain
// nothing more from more; raise it with WithMaxReadConns only after measuring.
const defaultReadConns = 2

type dbConfig struct {
	busyTimeout time.Duration
	readOnly    bool
	maxPathHops int
	importDir   string
	readConns   int
	vectorCache int64
	noAutoIndex bool
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
// contain the charta schema.
//
// Read-only enforcement is applied in the charta API layer: any Cypher
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

// NewTestDB opens an in-memory charta database, registers db.Close with
// t.Cleanup, and returns a ready-to-use *DB. t.Fatal is called on any error.
//
// This is the recommended way to create a charta database in tests:
//
//	func TestMyFeature(t *testing.T) {
//	    db := charta.NewTestDB(t)
//	    // db is closed automatically when the test ends
//	}
func NewTestDB(t testing.TB, opts ...Option) *DB {
	t.Helper()
	db, err := Open(":memory:", opts...)
	if err != nil {
		t.Fatalf("charta.NewTestDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return db
}

// WithImportDirectory enables LOAD CSV and restricts it to files below dir:
// LOAD CSV FROM 'file:///people.csv' reads dir/people.csv, and nothing outside
// dir can be read. Without this option LOAD CSV is disabled, so a query cannot
// read arbitrary files.
func WithImportDirectory(dir string) Option {
	return func(c *dbConfig) { c.importDir = dir }
}

// WithMaxReadConns sets how many read-only connections a file-backed database
// keeps for concurrent queries (default 2). Statements without updating
// clauses run on these connections, each in a snapshot transaction, so readers
// never wait for the writer and the writer never waits for readers. In-memory
// databases have a single connection and ignore this option. n <= 0 is a
// no-op.
func WithMaxReadConns(n int) Option {
	return func(c *dbConfig) {
		if n > 0 {
			c.readConns = n
		}
	}
}

// WithMaxVectorCacheBytes bounds the memory used by the in-memory vector
// matrices that serve db.index.vector.queryNodes (default 512 MiB across all
// vector indexes). A search on an index that does not fit is still answered, from
// a matrix built for that one query, which is slower. n <= 0 is a no-op.
func WithMaxVectorCacheBytes(n int64) Option {
	return func(c *dbConfig) {
		if n > 0 {
			c.vectorCache = n
		}
	}
}

// WithoutAutomaticIndexes stops charta creating property indexes on its own.
// By default, once equality filters on a property have narrowed a few scans of a
// graph with at least 500 nodes, an index on that property is created in the
// background; use this option if you want to control the schema yourself and
// create indexes only with CREATE INDEX or [DB.CreatePropertyIndex].
func WithoutAutomaticIndexes() Option {
	return func(c *dbConfig) { c.noAutoIndex = true }
}
