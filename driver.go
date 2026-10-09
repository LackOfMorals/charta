// Package graphlite is a zero-infrastructure embedded property graph database
// for Go.
//
// graphlite stores a labelled property graph in a local SQLite file and
// accepts queries written in openCypher. There is no external process to run,
// no driver dependency, and no network — just open a file and query.
//
// # Quick start
//
//	db, err := graphlite.Open(":memory:")
//	result, err := db.RunQuery(ctx, `MATCH (n:Person) RETURN n.name AS name`, nil)
//	for result.Next(ctx) {
//	    fmt.Println(result.Record().Values()[0])
//	}
//
// For explicit transaction control:
//
//	tx, err := db.BeginTx(ctx)
//	result, err := tx.Run(ctx, `CREATE (n:Person {name: $name})`, map[string]any{"name": "Alice"})
//	err = tx.Commit()
//
// In tests, use [NewTestDB] to open an in-memory database that is closed
// automatically when the test ends:
//
//	db := graphlite.NewTestDB(t)
//
// # Options
//
// Pass functional options to [Open] to tune behaviour:
//
//	db, err := graphlite.Open("graph.db",
//	    graphlite.WithBusyTimeout(5*time.Second),
//	    graphlite.WithReadOnly(),
//	)
package graphlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/LackOfMorals/graphlite/v2/cypher/interp"
	"github.com/LackOfMorals/graphlite/v2/cypher/proc"
	"github.com/LackOfMorals/graphlite/v2/store"
)

// DB is an open graphlite database. All methods are safe for concurrent use
// from multiple goroutines.
type DB struct {
	st       store.Store
	readOnly bool
	// hasReadPool is true when read-only statements run on the read pool.
	hasReadPool bool

	mu     sync.Mutex // guards closed
	closed bool
	bg     sync.WaitGroup // background index builds
	eng    interp.Engine  // interpreter state: registered procedures, index advisor
}

// Open opens (or creates) a graphlite database at path and returns a *DB.
//
// Use ":memory:" for a transient in-memory database. A file path (absolute or
// relative) opens (or creates) a persistent SQLite file. Pass Option values to
// customise behaviour — see WithBusyTimeout and WithReadOnly.
//
// Open applies the schema DDL and enables WAL journal mode before returning
// (skipped when WithReadOnly is set — the schema must already exist).
//
// Path traversal protection: if path is not ":memory:", Open rejects paths
// whose resolved form contains ".." components and resolves symlinks in the
// parent directory to prevent directory traversal via both ".." sequences and
// symlinks (e.g. "../../etc/passwd" and symlinks pointing outside the working
// tree are both rejected).
func Open(path string, opts ...Option) (*DB, error) {
	cfg := &dbConfig{readConns: defaultReadConns}
	for _, o := range opts {
		o(cfg)
	}

	if path != ":memory:" {
		cleaned := filepath.Clean(path)
		// Resolve symlinks in the parent directory to catch escapes via
		// symbolic links before applying the ".." component check.
		if dir, err := filepath.EvalSymlinks(filepath.Dir(cleaned)); err == nil {
			cleaned = filepath.Join(dir, filepath.Base(cleaned))
		}
		if slices.Contains(strings.Split(cleaned, string(filepath.Separator)), "..") {
			return nil, fmt.Errorf("graphlite: Open: path traversal not allowed: %q", path)
		}
	}

	st, err := store.Open(path, store.Config{
		BusyTimeout: cfg.busyTimeout,
		ReadConns:   cfg.readConns,
	})
	if err != nil {
		return nil, fmt.Errorf("graphlite: open %q: %w", path, err)
	}
	d := &DB{st: st, readOnly: cfg.readOnly, hasReadPool: st.HasReadPool()}
	d.eng.MaxPathHops = cfg.maxPathHops
	d.eng.ImportDir = cfg.importDir
	return d, nil
}

// Snapshot writes an atomic, consistent copy of the database to path.
// path must not already exist. The resulting file is a valid SQLite database
// that can be opened with [Open]. Works on both file-backed and in-memory
// databases — snapshotting an in-memory database is useful to persist its
// state before it is discarded.
//
// Returns an error if the backend does not support snapshots.
func (d *DB) Snapshot(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("graphlite: snapshot: %q already exists", path)
	}
	sn, ok := d.st.(store.Snapshotter)
	if !ok {
		return fmt.Errorf("graphlite: snapshot: not supported by this backend")
	}
	return sn.Snapshot(path)
}

// Close releases all resources held by the database. Subsequent calls on a
// closed DB return errors.
func (d *DB) Close(_ context.Context) error {
	d.mu.Lock()
	d.closed = true
	d.mu.Unlock()
	d.bg.Wait() // let queued index builds finish before the connections go
	if err := d.st.Close(); err != nil {
		return fmt.Errorf("graphlite: close: %w", err)
	}
	return nil
}

// RunQuery executes cypherStr in auto-commit mode and returns a lazy
// Result cursor. The caller must consume or exhaust the result to release
// underlying resources.
//
// params may be nil if the query has no parameters.
// Returns ErrReadOnly if the database was opened with WithReadOnly and the
// query contains write statements.
func (d *DB) RunQuery(ctx context.Context, cypherStr string, params map[string]any) (*Result, error) {
	var beginRead func(context.Context) (txExecer, error)
	if d.hasReadPool {
		beginRead = d.st.BeginReadTx
	}
	res, err := runInterp(ctx, d.st.Exec(), cypherStr, params, d.st.BeginExecTx, beginRead, d.readOnly, &d.eng)
	if keys := d.eng.TakeWantedIndexes(); len(keys) > 0 {
		d.buildIndexes(keys)
	}
	return res, err
}

// buildIndexes creates the automatic indexes that read-only statements asked
// for. Those statements run on read-only connections and cannot create them
// themselves, so it is done here on the write connection, in the background so
// a read does not wait for it.
func (d *DB) buildIndexes(keys []string) {
	d.mu.Lock()
	if d.closed || d.readOnly {
		d.mu.Unlock()
		return
	}
	d.bg.Add(1)
	d.mu.Unlock()
	go func() {
		defer d.bg.Done()
		ctx := context.Background()
		tx, err := d.st.BeginExecTx(ctx)
		if err != nil {
			return
		}
		d.eng.CreateIndexes(ctx, tx, keys)
		if err := tx.Commit(); err != nil {
			_ = tx.Rollback()
			d.eng.ResetIndexState()
		}
	}()
}

// BeginTx starts an explicit transaction and returns a *Tx.
//
// Returns [ErrReadOnly] if the database was opened with [WithReadOnly]; use
// [DB.RunQuery] for read-only access.
func (d *DB) BeginTx(ctx context.Context) (*Tx, error) {
	if d.readOnly {
		return nil, ErrReadOnly
	}
	txEx, err := d.st.BeginExecTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("graphlite: begin transaction: %w", err)
	}
	return &Tx{rawTx: txEx, eng: &d.eng}, nil
}

// execer is an alias for store.Execer: any *sql.DB, *sql.Tx or store.TxExecer
// satisfies it.
type execer = store.Execer

type txExecer = store.TxExecer

// RegisterProcedure makes a procedure callable with CALL. A later registration
// under the same name replaces the earlier one.
func (d *DB) RegisterProcedure(p *proc.Procedure) { d.eng.Procs.Register(p); d.eng.ClearStatements() }

// ClearProcedures removes every registered procedure.
func (d *DB) ClearProcedures() { d.eng.Procs.Clear(); d.eng.ClearStatements() }
