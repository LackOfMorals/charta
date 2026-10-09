package graphlite

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/LackOfMorals/graphlite/v2/cypher/analyze"
	"github.com/LackOfMorals/graphlite/v2/cypher/interp"
	"github.com/LackOfMorals/graphlite/v2/cypher/syntax"
)

// Engine selection. GRAPHLITE_ENGINE chooses how queries execute:
//
//	exec  the Go interpreter (cypher/interp; full openCypher semantics)
//	sql   the SQL translator (supports a fixed set of query shapes; being retired)
//
// The default is "exec".
const engineEnv = "GRAPHLITE_ENGINE"

func useInterpreter() bool { return os.Getenv(engineEnv) != "sql" }

// parseSyntax parses and analyses a query, wrapping errors the way the SQL path
// does so analyze.Describe sees the typed compile-time error.
func parseSyntax(cypherStr string, eng *interp.Engine) (*syntax.Statement, error) {
	if st, ok := eng.Statement(cypherStr); ok {
		return st, nil
	}
	st, err := syntax.Parse(cypherStr)
	if err != nil {
		return nil, fmt.Errorf("graphlite: parse: cypher syntax error: %w", err)
	}
	if err := analyze.CheckWith(st, &eng.Procs); err != nil {
		return nil, fmt.Errorf("graphlite: parse: cypher: %w", err)
	}
	eng.CacheStatement(cypherStr, st)
	return st, nil
}

// runInterp executes a query with the interpreter. When beginTxFn is non-nil
// the query runs in its own transaction, committed on success and rolled back on
// error; otherwise ex is already transaction-scoped.
func runInterp(ctx context.Context, ex execer, cypherStr string, params map[string]any, beginTxFn func(context.Context) (txExecer, error), readOnly bool, eng *interp.Engine) (*Result, error) {
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
		return nil, fmt.Errorf("graphlite: parse: cypher: %w", err)
	}
	if readOnly && hasWrites(st) {
		return nil, ErrReadOnly
	}
	var res *interp.Result
	if beginTxFn != nil {
		tx, err := beginTxFn(ctx)
		if err != nil {
			return nil, fmt.Errorf("graphlite: begin transaction: %w", err)
		}
		res, err = interp.RunWith(ctx, tx, st, params, eng)
		if err != nil {
			_ = tx.Rollback()
			eng.ResetIndexState()
			return nil, fmt.Errorf("graphlite: execute: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("graphlite: commit: %w", err)
		}
	} else {
		res, err = interp.RunWith(ctx, ex, st, params, eng)
		if err != nil {
			return nil, fmt.Errorf("graphlite: execute: %w", err)
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
