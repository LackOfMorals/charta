package interp

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/LackOfMorals/graphlite/v2/cypher/proc"
	"github.com/LackOfMorals/graphlite/v2/cypher/syntax"
)

// Engine is the per-database state the interpreter keeps between statements:
// registered procedures and the property-index advisor.
type Engine struct {
	// Procs are the procedures callable with CALL, besides the built-ins.
	Procs proc.Set
	// MaxPathHops, when positive, caps variable-length patterns: an unbounded
	// pattern stops at this many hops and an explicit upper bound above it is
	// an error. Zero means no cap.
	MaxPathHops int

	mu      sync.Mutex
	loaded  bool
	indexed map[string]bool // property keys with an automatic index
	scans   map[string]int  // how often each key was used to narrow a scan

	stmtMu sync.RWMutex
	stmts  map[string]*syntax.Statement // parsed and analysed statements, by text
}

// maxCachedStatements bounds the statement cache; it is emptied when full.
const maxCachedStatements = 512

// Statement returns the cached statement for query text, if any. Statements
// are immutable once built and may be shared between goroutines.
func (e *Engine) Statement(q string) (*syntax.Statement, bool) {
	e.stmtMu.RLock()
	defer e.stmtMu.RUnlock()
	st, ok := e.stmts[q]
	return st, ok
}

// CacheStatement remembers a parsed and analysed statement.
func (e *Engine) CacheStatement(q string, st *syntax.Statement) {
	e.stmtMu.Lock()
	defer e.stmtMu.Unlock()
	if e.stmts == nil || len(e.stmts) >= maxCachedStatements {
		e.stmts = make(map[string]*syntax.Statement, 64)
	}
	e.stmts[q] = st
}

// ClearStatements drops the statement cache. Call it when the set of
// procedures changes, since analysis depends on it.
func (e *Engine) ClearStatements() {
	e.stmtMu.Lock()
	e.stmts = nil
	e.stmtMu.Unlock()
}

// Automatic property indexes. A key becomes indexed once it has narrowed node
// scans indexScansBeforeIndex times on a graph of at least indexMinNodes nodes
// (a scan of fewer nodes costs less than maintaining an index), capped at
// indexMax indexes. Indexes are never dropped automatically.
const (
	indexPrefix           = "idx_auto_np_"
	indexScansBeforeIndex = 3
	indexMinNodes         = 500
	indexMax              = 32
)

// ResetIndexState forgets what the engine knows about existing indexes. Call it
// after a rolled-back statement, which may have undone an index creation.
func (e *Engine) ResetIndexState() {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.loaded = false
	e.mu.Unlock()
}

// noteScan records that key narrowed a node scan and creates an expression
// index on it when the policy says so. Failures (a read-only database, say)
// are ignored: an index only ever makes queries faster.
func (e *Engine) noteScan(ctx context.Context, db DB, key string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.loaded {
		e.indexed = map[string]bool{}
		if e.scans == nil {
			e.scans = map[string]int{}
		}
		rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'index' AND name LIKE ?`, indexPrefix+"%")
		if err != nil {
			return
		}
		for rows.Next() {
			var name string
			if rows.Scan(&name) == nil {
				e.indexed[strings.TrimPrefix(name, indexPrefix)] = true
			}
		}
		rows.Close()
		e.loaded = true
	}
	id := fmt.Sprintf("%x", key)
	if e.indexed[id] {
		return
	}
	e.scans[key]++
	if e.scans[key] < indexScansBeforeIndex || len(e.indexed) >= indexMax {
		return
	}
	var maxID int64
	rows, err := db.QueryContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM nodes`)
	if err != nil {
		return
	}
	if rows.Next() {
		_ = rows.Scan(&maxID)
	}
	rows.Close()
	if maxID < indexMinNodes {
		return
	}
	// key is restricted to [A-Za-z0-9_] by pushableKey, so it is safe to inline.
	_, err = db.ExecContext(ctx, fmt.Sprintf(
		`CREATE INDEX IF NOT EXISTS %s%s ON nodes(json_extract(props, '$."%s"'))`, indexPrefix, id, key))
	if err == nil {
		e.indexed[id] = true
	} else {
		e.scans[key] = -1 << 30 // do not retry
	}
}
