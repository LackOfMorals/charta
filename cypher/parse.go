package cypher

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/LackOfMorals/graphlite/v2/cypher/analyze"
	"github.com/LackOfMorals/graphlite/v2/cypher/syntax"
)

// Parse parses a Cypher statement with the hand-written cypher/syntax parser,
// checks it semantically (cypher/analyze: undefined or conflicting variables,
// aggregation and type errors) and lowers the result to the Query AST that the
// planner and translator consume. Compile-time errors are *syntax.SyntaxError
// or *analyze.Error; analyze.Describe returns their TCK class and code.
//
// Constructs the Query AST cannot represent (UNION, UNWIND, CALL, FOREACH,
// multiple WITH stages, path selectors, label expressions other than simple
// conjunctions, …) are rejected with a "not supported" error. WHERE
// expressions are parsed into a typed Expr tree; forms the tree does not model
// become RawExpr, which the translator rejects unless it is a bare identifier.
//
// Parse is safe to call from multiple goroutines.
func Parse(input string) (*Query, error) {
	st, err := syntax.Parse(input)
	if err != nil {
		return nil, fmt.Errorf("cypher syntax error: %w", err)
	}
	if err := analyze.Check(st); err != nil {
		return nil, fmt.Errorf("cypher: %w", err)
	}
	return lowerStatement(st)
}

func unsupported(format string, args ...any) error {
	return fmt.Errorf("cypher: "+format+" is not supported", args...)
}

func lowerStatement(st *syntax.Statement) (*Query, error) {
	if st.Mode != syntax.ModeNone {
		return nil, unsupported("EXPLAIN/PROFILE")
	}
	switch b := st.Body.(type) {
	case *syntax.SingleQuery:
		return lowerQuery(b)
	case *syntax.UnionQuery:
		return nil, fmt.Errorf("cypher: UNION is not supported in v0.1")
	}
	return nil, unsupported("%s", bodyName(st.Body))
}

// bodyName names a statement kind for "not supported" messages.
func bodyName(b syntax.Body) string {
	switch b := b.(type) {
	case *syntax.CreateIndex:
		return "CREATE INDEX"
	case *syntax.CreateConstraint:
		return "CREATE CONSTRAINT"
	case *syntax.DropSchema:
		return "DROP INDEX/CONSTRAINT"
	case *syntax.Show:
		return "SHOW"
	case *syntax.ServerCommand:
		return "the server command " + b.Command
	case *syntax.Conditional:
		return "a conditional (WHEN) query"
	}
	return fmt.Sprintf("this statement kind (%T)", b)
}

func lowerQuery(sq *syntax.SingleQuery) (*Query, error) {
	if len(sq.Clauses) == 1 {
		if _, ok := sq.Clauses[0].(*syntax.Call); ok {
			return nil, fmt.Errorf("cypher: standalone CALL is not supported in v0.1")
		}
	}
	withs := 0
	for _, c := range sq.Clauses {
		if _, ok := c.(*syntax.With); ok {
			withs++
		}
	}
	if withs > 1 {
		return nil, fmt.Errorf("cypher: multiple WITH stages are not yet supported")
	}

	q := &Query{}
	for _, c := range sq.Clauses {
		lc, err := lowerClause(c)
		if err != nil {
			return nil, err
		}
		q.Clauses = append(q.Clauses, lc)
	}
	return q, nil
}

func lowerClause(c syntax.Clause) (Clause, error) {
	switch c := c.(type) {
	case *syntax.Match:
		return lowerMatch(c)
	case *syntax.Create:
		parts, err := lowerPatternParts(c.Patterns)
		if err != nil {
			return nil, err
		}
		return &CreateClause{Pattern: parts}, nil
	case *syntax.Merge:
		return lowerMerge(c)
	case *syntax.Set:
		items, err := lowerSetItems(c.Items)
		if err != nil {
			return nil, err
		}
		return &SetClause{Items: items}, nil
	case *syntax.Remove:
		return lowerRemove(c)
	case *syntax.Delete:
		dc := &DeleteClause{Detach: c.Detach}
		for _, e := range c.Exprs {
			le, err := lowerExpr(e)
			if err != nil {
				return nil, err
			}
			dc.Exprs = append(dc.Exprs, le)
		}
		return dc, nil
	case *syntax.With:
		return lowerWith(c)
	case *syntax.Return:
		return lowerReturn(c)
	case *syntax.Unwind, *syntax.Call, *syntax.LoadCSV:
		return nil, fmt.Errorf("cypher: only MATCH is supported as a reading clause in v0.1 (got %s)", clauseKeyword(c))
	}
	return nil, fmt.Errorf("cypher: %s is not supported in v0.1", clauseKeyword(c))
}

func clauseKeyword(c syntax.Clause) string { return syntax.ClauseName(c) }

// ─── reading / updating clauses ──────────────────────────────────────────────

func lowerMatch(m *syntax.Match) (*MatchClause, error) {
	if len(m.Hints) > 0 || m.Mode != syntax.MatchModeDefault {
		return nil, unsupported("a MATCH hint or match mode")
	}
	parts, err := lowerPatternParts(m.Patterns)
	if err != nil {
		return nil, err
	}
	mc := &MatchClause{Optional: m.Optional, Pattern: parts}
	if m.Where != nil {
		w, err := lowerExpr(m.Where)
		if err != nil {
			return nil, fmt.Errorf("cypher: WHERE clause: %w", err)
		}
		mc.Where = w
	}
	return mc, nil
}

func lowerMerge(m *syntax.Merge) (*MergeClause, error) {
	part, err := lowerPatternPart(m.Pattern)
	if err != nil {
		return nil, fmt.Errorf("cypher: MERGE pattern: %w", err)
	}
	mc := &MergeClause{Pattern: part}
	for _, a := range m.Actions {
		items, err := lowerSetItems(a.Items)
		if err != nil {
			return nil, fmt.Errorf("cypher: MERGE action SET: %w", err)
		}
		if a.OnCreate {
			mc.OnCreate = append(mc.OnCreate, items...)
		} else {
			mc.OnMatch = append(mc.OnMatch, items...)
		}
	}
	return mc, nil
}

func lowerSetItems(items []syntax.SetItem) ([]SetItem, error) {
	out := make([]SetItem, 0, len(items))
	for _, it := range items {
		switch it.Kind {
		case syntax.SetProperty:
			prop, ok := it.Target.(*syntax.Property)
			if !ok {
				if _, dyn := it.Target.(*syntax.Subscript); dyn {
					return nil, unsupported("a dynamic property assignment (SET n[key] = …)")
				}
				return nil, fmt.Errorf("cypher: SET item must have exactly one property lookup")
			}
			id, ok := prop.Subject.(*syntax.Ident)
			if !ok {
				return nil, fmt.Errorf("cypher: SET item atom is not a variable")
			}
			value, err := lowerExpr(it.Value)
			if err != nil {
				return nil, err
			}
			out = append(out, SetItem{Variable: id.Name, Property: prop.Key, Expr: value})
		case syntax.SetMerge:
			id, ok := it.Target.(*syntax.Ident)
			if !ok {
				return nil, fmt.Errorf("cypher: SET item atom is not a variable")
			}
			props := make(map[string]Expr)
			if ml, ok := it.Value.(*syntax.MapLit); ok {
				for _, e := range ml.Entries {
					v, err := lowerExpr(e.Value)
					if err != nil {
						return nil, err
					}
					props[e.Key] = v
				}
			}
			out = append(out, SetItem{Variable: id.Name, Merge: true, Props: props})
		default:
			return nil, fmt.Errorf("cypher: only 'variable.property = expr' and 'variable += {map}' SET items are supported")
		}
	}
	return out, nil
}

func lowerRemove(r *syntax.Remove) (*RemoveClause, error) {
	rc := &RemoveClause{}
	for _, it := range r.Items {
		switch t := it.Target.(type) {
		case *syntax.Ident:
			labels, err := labelList(it.Labels)
			if err != nil {
				return nil, err
			}
			rc.Items = append(rc.Items, RemoveItem{Variable: t.Name, Labels: labels})
		case *syntax.Property:
			id, ok := t.Subject.(*syntax.Ident)
			if !ok {
				return nil, fmt.Errorf("cypher: REMOVE property item must have exactly one property lookup on a variable")
			}
			rc.Items = append(rc.Items, RemoveItem{Variable: id.Name, IsProp: true, Property: t.Key})
		default:
			return nil, fmt.Errorf("cypher: unrecognised REMOVE item")
		}
	}
	return rc, nil
}

// ─── projections ─────────────────────────────────────────────────────────────

func lowerItems(p syntax.Projection) ([]ReturnItem, []SortItem, error) {
	// A leading `*` is dropped, as the legacy parser did: an empty item list
	// is how the planner represents "all variables in scope" (so `RETURN *`
	// works, while `RETURN *, x` projects only x - a legacy limitation).
	var items []ReturnItem
	for _, it := range p.Items {
		e, err := lowerExpr(it.Expr)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, ReturnItem{Source: it.Source, Alias: it.Alias, Expr: e})
	}
	var order []SortItem
	for _, s := range p.Order {
		e, err := lowerExpr(s.Expr)
		if err != nil {
			return nil, nil, err
		}
		order = append(order, SortItem{Expr: e, Descending: s.Desc})
	}
	return items, order, nil
}

// lowerSkipLimit mirrors the legacy rules: an integer literal, or (RETURN only)
// a $parameter.
func lowerSkipLimit(e syntax.Expr, allowParam bool) (val *int64, param string, err error) {
	switch e := e.(type) {
	case nil:
		return nil, "", nil
	case *syntax.Param:
		if allowParam {
			return nil, e.Name, nil
		}
		return nil, "", fmt.Errorf("expected integer literal, got %q", "$"+e.Name)
	case *syntax.IntLit:
		v, perr := strconv.ParseInt(strings.TrimSpace(e.Text), 10, 64)
		if perr != nil {
			return nil, "", fmt.Errorf("expected integer literal, got %q", e.Text)
		}
		return &v, "", nil
	}
	return nil, "", fmt.Errorf("expected integer literal, got %s", exprString(e))
}

func lowerReturn(r *syntax.Return) (*ReturnClause, error) {
	items, order, err := lowerItems(r.Projection)
	if err != nil {
		return nil, err
	}
	rc := &ReturnClause{Distinct: r.Distinct, Items: items, OrderBy: order}
	if rc.Skip, rc.SkipParam, err = lowerSkipLimit(r.Skip, true); err != nil {
		return nil, fmt.Errorf("cypher: SKIP value must be a non-negative integer literal: %w", err)
	}
	if rc.Limit, rc.LimitParam, err = lowerSkipLimit(r.Limit, true); err != nil {
		return nil, fmt.Errorf("cypher: LIMIT value must be a non-negative integer literal: %w", err)
	}
	return rc, nil
}

func lowerWith(w *syntax.With) (*WithClause, error) {
	items, order, err := lowerItems(w.Projection)
	if err != nil {
		return nil, err
	}
	wc := &WithClause{Distinct: w.Distinct, Items: items, OrderBy: order}
	if wc.Skip, _, err = lowerSkipLimit(w.Skip, false); err != nil {
		return nil, fmt.Errorf("cypher: WITH SKIP: %w", err)
	}
	if wc.Limit, _, err = lowerSkipLimit(w.Limit, false); err != nil {
		return nil, fmt.Errorf("cypher: WITH LIMIT: %w", err)
	}
	if w.Where != nil {
		if wc.Where, err = lowerExpr(w.Where); err != nil {
			return nil, fmt.Errorf("cypher: WITH WHERE: %w", err)
		}
	}
	return wc, nil
}

// ─── patterns ────────────────────────────────────────────────────────────────

func lowerPatternParts(parts []*syntax.PatternPart) ([]PatternPart, error) {
	out := make([]PatternPart, 0, len(parts))
	for _, p := range parts {
		lp, err := lowerPatternPart(p)
		if err != nil {
			return nil, err
		}
		out = append(out, lp)
	}
	return out, nil
}

func lowerPatternPart(p *syntax.PatternPart) (PatternPart, error) {
	if p.Selector != nil {
		return PatternPart{}, unsupported("a path selector")
	}
	if p.Func != syntax.FuncNone {
		return PatternPart{}, unsupported("shortestPath()/allShortestPaths()")
	}
	first, ok := p.Elems[0].(*syntax.NodePattern)
	if !ok {
		return PatternPart{}, unsupported("a quantified path pattern")
	}
	start, err := lowerNode(first)
	if err != nil {
		return PatternPart{}, err
	}
	pp := PatternPart{Variable: p.Var, Start: start}
	for i := 1; i < len(p.Elems); i += 2 {
		rel, ok := p.Elems[i].(*syntax.RelPattern)
		if !ok || i+1 >= len(p.Elems) {
			return PatternPart{}, unsupported("a quantified path pattern")
		}
		node, ok := p.Elems[i+1].(*syntax.NodePattern)
		if !ok {
			return PatternPart{}, unsupported("a quantified path pattern")
		}
		lr, err := lowerRel(rel)
		if err != nil {
			return PatternPart{}, err
		}
		ln, err := lowerNode(node)
		if err != nil {
			return PatternPart{}, err
		}
		pp.Chain = append(pp.Chain, PatternChain{Rel: lr, Node: ln})
	}
	return pp, nil
}

func lowerNode(n *syntax.NodePattern) (NodePattern, error) {
	if n.Where != nil {
		return NodePattern{}, unsupported("WHERE inside a node pattern")
	}
	labels, err := labelList(n.Labels)
	if err != nil {
		return NodePattern{}, err
	}
	props, err := lowerProps(n.Props)
	if err != nil {
		return NodePattern{}, err
	}
	return NodePattern{
		Variable:         n.Var,
		Labels:           labels,
		Props:            props,
		HasExplicitProps: n.Props != nil,
	}, nil
}

func lowerRel(r *syntax.RelPattern) (RelPattern, error) {
	if r.Where != nil {
		return RelPattern{}, unsupported("WHERE inside a relationship pattern")
	}
	if r.Quant != nil {
		return RelPattern{}, unsupported("a quantified relationship")
	}
	types, err := typeList(r.Types)
	if err != nil {
		return RelPattern{}, err
	}
	props, err := lowerProps(r.Props)
	if err != nil {
		return RelPattern{}, err
	}
	rp := RelPattern{
		Variable: r.Var,
		Types:    types,
		Props:    props,
		ToLeft:   r.Dir == syntax.DirLeft || r.Dir == syntax.DirBoth,
		ToRight:  r.Dir == syntax.DirRight || r.Dir == syntax.DirBoth,
	}
	if r.Range != nil {
		rp.VarLength = true
		rp.MinHops, rp.MaxHops = 1, 0
		if r.Range.Min != nil {
			rp.MinHops = int(*r.Range.Min)
		}
		if r.Range.Max != nil {
			rp.MaxHops = int(*r.Range.Max)
		}
	}
	return rp, nil
}

// lowerProps converts an inline property map or $param to typed expressions. A
// parameter map is stored under the key "$".
func lowerProps(e syntax.Expr) (map[string]Expr, error) {
	props := make(map[string]Expr)
	switch e := e.(type) {
	case nil:
	case *syntax.MapLit:
		for _, entry := range e.Entries {
			v, err := lowerExpr(entry.Value)
			if err != nil {
				return nil, err
			}
			props[entry.Key] = v
		}
	case *syntax.Param:
		props["$"] = &ParamRef{Name: e.Name}
	}
	return props, nil
}

// labelList flattens a conjunction of plain label names (`:A:B` / `A&B`).
func labelList(le syntax.LabelExpr) ([]string, error) {
	switch le := le.(type) {
	case nil:
		return nil, nil
	case *syntax.LabelName:
		if le.Dynamic != nil {
			return nil, unsupported("a dynamic label")
		}
		return []string{le.Name}, nil
	case *syntax.LabelAnd:
		l, err := labelList(le.L)
		if err != nil {
			return nil, err
		}
		r, err := labelList(le.R)
		if err != nil {
			return nil, err
		}
		return append(l, r...), nil
	}
	return nil, unsupported("a label expression other than a conjunction of names")
}

// typeList flattens a disjunction of plain relationship type names (`:A|B`).
func typeList(le syntax.LabelExpr) ([]string, error) {
	switch le := le.(type) {
	case nil:
		return nil, nil
	case *syntax.LabelName:
		if le.Dynamic != nil {
			return nil, unsupported("a dynamic relationship type")
		}
		return []string{le.Name}, nil
	case *syntax.LabelOr:
		l, err := typeList(le.L)
		if err != nil {
			return nil, err
		}
		r, err := typeList(le.R)
		if err != nil {
			return nil, err
		}
		return append(l, r...), nil
	}
	return nil, unsupported("a relationship type expression other than a disjunction of names")
}
