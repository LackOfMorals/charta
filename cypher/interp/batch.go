package interp

import (
	"context"
	"database/sql"
	"strings"
	"sync/atomic"
)

// CREATE writes nodes and relationships one row at a time, which for a bulk load
// costs a statement per entity. Instead createNode and createRel assign the id
// SQLite would (the next value of the table's AUTOINCREMENT sequence), buffer the
// row, and the buffer is written as a few multi-row INSERTs. Everything that
// reads or changes the database goes through graph.db, a flushDB, which writes
// the buffer first, so nothing can observe the database without the buffered
// rows; the buffer is also written when the statement finishes, and dropped, with
// the rest of the statement's effects, when it fails and the caller rolls back.

// batchDisabled makes every create its own INSERT again; tests use it to compare.
var batchDisabled bool

// batchedRows counts the entities written through a multi-row INSERT (for tests).
var batchedRows atomic.Int64

const (
	// batchLimit is the number of buffered entities that forces a flush.
	batchLimit = 2000
	// Rows per INSERT, so that no statement binds more than ~1000 variables.
	nodeRowsPerInsert  = 300 // 3 variables each
	labelRowsPerInsert = 450 // 2
	relRowsPerInsert   = 190 // 5
)

type pendingNode struct {
	id     int64
	labels []string
	enc    string
}

type pendingRel struct {
	id         int64
	typ        string
	start, end int64
	enc        string
}

// flushDB is the DB the interpreter reads and writes through: any call writes the
// graph's buffered creates first.
type flushDB struct {
	g   *graph
	raw DB
}

func (d *flushDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if err := d.g.flushWrites(); err != nil {
		return nil, err
	}
	return d.raw.QueryContext(ctx, q, args...)
}

func (d *flushDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if err := d.g.flushWrites(); err != nil {
		return nil, err
	}
	return d.raw.ExecContext(ctx, q, args...)
}

// nextID returns the id the next row of table (nodes or edges) will get. The
// sequence is read from the database each time the buffer was empty, because
// anything may have been inserted since.
func (g *graph) nextID(table string, buffered int, last int64) (int64, error) {
	if buffered > 0 {
		return last + 1, nil
	}
	var seq int64
	rows, err := g.raw.QueryContext(g.ctx, `SELECT seq FROM sqlite_sequence WHERE name = ?`, table)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	if rows.Next() {
		if err := rows.Scan(&seq); err != nil {
			return 0, err
		}
	}
	return seq + 1, rows.Err()
}

// bufferNode queues a node for writing and returns its id.
func (g *graph) bufferNode(labels []string, enc string) (int64, error) {
	var last int64
	if k := len(g.pendNodes); k > 0 {
		last = g.pendNodes[k-1].id
	}
	id, err := g.nextID("nodes", len(g.pendNodes), last)
	if err != nil {
		return 0, err
	}
	g.pendNodes = append(g.pendNodes, pendingNode{id: id, labels: labels, enc: enc})
	if len(g.pendNodes) >= batchLimit {
		if err := g.flushWrites(); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// bufferRel queues a relationship for writing and returns its id.
func (g *graph) bufferRel(typ string, start, end int64, enc string) (int64, error) {
	var last int64
	if k := len(g.pendRels); k > 0 {
		last = g.pendRels[k-1].id
	}
	id, err := g.nextID("edges", len(g.pendRels), last)
	if err != nil {
		return 0, err
	}
	g.pendRels = append(g.pendRels, pendingRel{id: id, typ: typ, start: start, end: end, enc: enc})
	if len(g.pendRels) >= batchLimit {
		if err := g.flushWrites(); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// flushWrites writes the buffered nodes (with their labels), then the buffered
// relationships, which may refer to them.
func (g *graph) flushWrites() error {
	if len(g.pendNodes) == 0 && len(g.pendRels) == 0 {
		return nil
	}
	nodes, rels := g.pendNodes, g.pendRels
	g.pendNodes, g.pendRels = nil, nil
	for len(nodes) > 0 {
		chunk := nodes[:min(len(nodes), nodeRowsPerInsert)]
		nodes = nodes[len(chunk):]
		var sb strings.Builder
		args := make([]any, 0, 3*len(chunk))
		sb.WriteString(`INSERT INTO nodes (id, labels, props) VALUES `)
		for i, n := range chunk {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString("(?,?,?)")
			args = append(args, n.id, strings.Join(n.labels, ","), n.enc)
		}
		if _, err := g.raw.ExecContext(g.ctx, sb.String(), args...); err != nil {
			return err
		}
		batchedRows.Add(int64(len(chunk)))
		var ids []int64
		var names []string
		for _, n := range chunk {
			for _, l := range n.labels {
				ids = append(ids, n.id)
				names = append(names, l)
			}
		}
		for len(ids) > 0 {
			k := min(len(ids), labelRowsPerInsert)
			sb.Reset()
			args = args[:0]
			sb.WriteString(`INSERT INTO node_labels (node_id, label) VALUES `)
			for i := 0; i < k; i++ {
				if i > 0 {
					sb.WriteByte(',')
				}
				sb.WriteString("(?,?)")
				args = append(args, ids[i], names[i])
			}
			if _, err := g.raw.ExecContext(g.ctx, sb.String(), args...); err != nil {
				return err
			}
			ids, names = ids[k:], names[k:]
		}
	}
	for len(rels) > 0 {
		chunk := rels[:min(len(rels), relRowsPerInsert)]
		rels = rels[len(chunk):]
		var sb strings.Builder
		args := make([]any, 0, 5*len(chunk))
		sb.WriteString(`INSERT INTO edges (id, type, start_id, end_id, props) VALUES `)
		for i, r := range chunk {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString("(?,?,?,?,?)")
			args = append(args, r.id, r.typ, r.start, r.end, r.enc)
		}
		if _, err := g.raw.ExecContext(g.ctx, sb.String(), args...); err != nil {
			return err
		}
		batchedRows.Add(int64(len(chunk)))
	}
	return nil
}
