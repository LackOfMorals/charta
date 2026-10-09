package charta

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/LackOfMorals/charta/cypher/analyze"
	"github.com/LackOfMorals/charta/cypher/interp"
	"github.com/LackOfMorals/charta/cypher/syntax"
)

// parseSyntax parses and analyses a query, wrapping errors with %w so
// analyze.Describe sees the typed compile-time error.
func parseSyntax(cypherStr string, eng *interp.Engine) (*syntax.Statement, error) {
	if st, ok := eng.Statement(cypherStr); ok {
		return st, nil
	}
	st, err := syntax.Parse(cypherStr)
	if err != nil {
		return nil, fmt.Errorf("charta: parse: cypher syntax error: %w", err)
	}
	if err := analyze.CheckWith(st, &eng.Procs); err != nil {
		return nil, fmt.Errorf("charta: parse: cypher: %w", err)
	}
	eng.CacheStatement(cypherStr, st)
	return st, nil
}

// readOnlyDB lets the interpreter read through a connection while refusing any
// write, as a guard on top of the connection's own query_only setting.
type readOnlyDB struct{ interp.DB }

func (readOnlyDB) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, errors.New("charta: write attempted on a read-only connection")
}

// runInterp executes a query with the interpreter. When beginTxFn is non-nil
// the query runs in its own transaction, committed on success and rolled back on
// error; otherwise ex is already transaction-scoped. When beginRead is also
// non-nil, a statement without updating clauses runs instead in a read-only
// transaction on the read pool, so it neither waits for nor blocks the writer.
func runInterp(ctx context.Context, ex execer, cypherStr string, params map[string]any, beginTxFn, beginRead func(context.Context) (txExecer, error), readOnly bool, eng *interp.Engine, deltaSink *[]interp.VectorDelta) (*Result, error) {
	st, err := parseSyntax(cypherStr, eng)
	if err != nil {
		return nil, err
	}
	for _, name := range st.Params {
		if _, ok := params[name]; !ok {
			return nil, &ErrMissingParameter{Name: name}
		}
	}
	if err := analyze.CheckParams(st, params, &eng.Procs); err != nil {
		return nil, fmt.Errorf("charta: parse: cypher: %w", err)
	}
	if readOnly && hasWrites(st) {
		return nil, ErrReadOnly
	}
	var res *interp.Result
	if beginRead != nil && !hasWrites(st) {
		tx, err := beginRead(ctx)
		if err != nil {
			return nil, fmt.Errorf("charta: begin read transaction: %w", err)
		}
		res, err = interp.RunReadOnly(ctx, readOnlyDB{tx}, st, params, eng)
		_ = tx.Rollback() // nothing to commit; this just ends the snapshot
		if err != nil {
			return nil, execError(err)
		}
		return interpResult(res), nil
	}
	if beginTxFn != nil {
		tx, err := beginTxFn(ctx)
		if err != nil {
			return nil, fmt.Errorf("charta: begin transaction: %w", err)
		}
		// A statement with no updating clause and no enclosing transaction has no
		// uncommitted writes of its own, so it may search the shared vector matrices.
		res, err = interp.RunWithOptions(ctx, tx, st, params, eng, interp.Options{VectorCache: !hasWrites(st)})
		if err != nil {
			_ = tx.Rollback()
			eng.ResetIndexState()
			return nil, execError(err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("charta: commit: %w", err)
		}
		eng.ApplyVectorDeltas(res.VectorDeltas) // only now that the commit succeeded
	} else {
		res, err = interp.RunWith(ctx, ex, st, params, eng)
		if err != nil {
			return nil, execError(err)
		}
		if deltaSink != nil { // inside an explicit transaction: applied when it commits
			*deltaSink = append(*deltaSink, res.VectorDeltas...)
		}
	}
	return interpResult(res), nil
}

// hasWrites reports whether a statement contains an updating clause.
func hasWrites(st *syntax.Statement) bool {
	var body func(b syntax.Body) bool
	clauses := func(cs []syntax.Clause) bool {
		for _, c := range cs {
			switch c := c.(type) {
			case *syntax.Create, *syntax.Merge, *syntax.Set, *syntax.Remove, *syntax.Delete, *syntax.Foreach:
				return true
			case *syntax.CallSubquery:
				if body(c.Body) {
					return true
				}
			}
		}
		return false
	}
	body = func(b syntax.Body) bool {
		switch b := b.(type) {
		case *syntax.SingleQuery:
			return clauses(b.Clauses)
		case *syntax.UnionQuery:
			for _, q := range b.Queries {
				if clauses(q.Clauses) {
					return true
				}
			}
		case *syntax.Conditional:
			for _, w := range b.Branches {
				if body(w.Body) {
					return true
				}
			}
			return b.Else != nil && body(b.Else)
		case *syntax.CreateIndex, *syntax.CreateConstraint, *syntax.DropSchema:
			return true
		}
		return false
	}
	return body(st.Body)
}

func interpResult(res *interp.Result) *Result {
	records := make([]*Record, 0, len(res.Rows))
	for _, row := range res.Rows {
		vals := make([]any, len(row))
		for i, v := range row {
			vals[i] = fromInterp(v)
		}
		records = append(records, newRecord(res.Columns, vals))
	}
	out := newInMemoryResult(res.Columns, records)
	c := res.Counters
	out.setCounters(queryCounters{
		nodesCreated:         c.NodesCreated,
		nodesDeleted:         c.NodesDeleted,
		relationshipsCreated: c.RelationshipsCreated,
		relationshipsDeleted: c.RelationshipsDeleted,
		propertiesSet:        c.PropertiesSet,
		propertiesRemoved:    c.PropertiesRemoved,
		labelsAdded:          c.LabelsAdded,
		labelsRemoved:        c.LabelsRemoved,
		indexesAdded:         c.IndexesAdded,
		indexesRemoved:       c.IndexesRemoved,
		constraintsAdded:     c.ConstraintsAdded,
		constraintsRemoved:   c.ConstraintsRemoved,
	})
	return out
}

// fromInterp converts an interpreter value to the public representation.
func fromInterp(v any) any {
	switch x := v.(type) {
	case *interp.Node:
		return nodeOf(x)
	case *interp.Rel:
		return relOf(x)
	case *interp.Path:
		p := Path{}
		for _, n := range x.Nodes {
			p.Nodes = append(p.Nodes, *nodeOf(n))
		}
		for _, r := range x.Rels {
			p.Relationships = append(p.Relationships, *relOf(r))
		}
		return p
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = fromInterp(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = fromInterp(e)
		}
		return out
	}
	return v
}

func nodeOf(n *interp.Node) *Node {
	return &Node{ElementId: strconv.FormatInt(n.ID, 10), Labels: append([]string(nil), n.Labels...), Props: propsOf(n.Props)}
}

func relOf(r *interp.Rel) *Relationship {
	return &Relationship{
		ElementId:      strconv.FormatInt(r.ID, 10),
		Type:           r.Type,
		StartElementId: strconv.FormatInt(r.Start, 10),
		EndElementId:   strconv.FormatInt(r.End, 10),
		Props:          propsOf(r.Props),
	}
}

func propsOf(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = fromInterp(v)
	}
	return out
}

// execError wraps an interpreter error. A construct the interpreter does not
// implement becomes an *ErrUnsupportedCypher.
func execError(err error) error {
	var ie *interp.Error
	if errors.As(err, &ie) && ie.Code == "Unsupported" {
		return &ErrUnsupportedCypher{Clause: ie.Msg, Detail: "not supported by the charta interpreter yet"}
	}
	if errors.As(err, &ie) && ie.Schema != nil && ie.Code == "ConstraintValidationFailed" {
		return &ErrConstraintViolation{
			Name: ie.Schema.Name, Kind: ie.Schema.Kind, EntityType: ie.Schema.Entity, Label: ie.Schema.Target,
			Properties: append([]string(nil), ie.Schema.Properties...), Message: ie.Msg, cause: ie,
		}
	}
	return fmt.Errorf("charta: execute: %w", err)
}
