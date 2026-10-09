package analyze

import (
	"sort"

	"github.com/LackOfMorals/graphlite/v2/cypher/syntax"
)

// projectionClause analyses a WITH or RETURN: output names, grouping, ORDER BY,
// SKIP/LIMIT and (for WITH) WHERE. It returns the scope after the projection
// and the output column names.
func (c *checker) projectionClause(pos syntax.Pos, p *syntax.Projection, where syntax.Expr, isReturn bool, sc *scope) (*scope, []string) {
	var starNames []string
	if p.Star {
		starNames = sc.names()
		sort.Strings(starNames)
		if isReturn && len(starNames) == 0 && !sc.isOpen() {
			c.fail(CodeNoVariablesInScope, pos, "RETURN * / WITH * has no variables in scope")
		}
	}

	var cols []string
	seen := map[string]bool{}
	addName := func(name string, at syntax.Pos) {
		if seen[name] {
			c.fail(CodeColumnNameConflict, at, "multiple result columns with the name `%s`", name)
		}
		seen[name] = true
		cols = append(cols, name)
	}
	for _, n := range starNames {
		addName(n, pos)
	}

	type projected struct {
		name string
		expr syntax.Expr
		t    typ
		agg  bool
	}
	var items []projected
	var unaliased *syntax.Pos // first WITH item that needs an alias; reported last
	aggregating := false
	for _, it := range p.Items {
		t := c.expr(it.Expr, env{sc: sc, aggOK: true})
		agg := hasAggregate(it.Expr)
		name := it.Alias
		if name == "" {
			if id, ok := it.Expr.(*syntax.Ident); ok {
				name = id.Name
			} else {
				if !isReturn && unaliased == nil {
					at := it.Pos()
					unaliased = &at
				}
				name = it.Source
			}
		}
		addName(name, it.Pos())
		items = append(items, projected{name, it.Expr, t, agg})
		aggregating = aggregating || agg
	}

	// Implicit grouping: inside an aggregating item every variable or property
	// reference outside an aggregate must be a projected grouping key.
	var keys, all []syntax.Expr
	for _, n := range starNames {
		keys = append(keys, &syntax.Ident{Name: n})
	}
	roots := map[string]bool{}
	for _, it := range items {
		all = append(all, it.expr)
		identsIn(it.expr, roots)
		if !it.agg {
			keys = append(keys, it.expr)
		}
	}
	if aggregating {
		for _, it := range items {
			if it.agg {
				c.checkGrouping(it.expr, keys)
			}
		}
	}

	// The scope after the projection.
	ns := newScope(sc.parent)
	if p.Star {
		for _, n := range starNames {
			t, _ := sc.lookup(n)
			ns.declare(n, t)
		}
		ns.open = sc.open
	}
	for _, it := range items {
		ns.declare(it.name, it.t)
	}

	if p.Skip != nil {
		c.constantArg(p.Skip, "SKIP")
	}
	if p.Limit != nil {
		c.constantArg(p.Limit, "LIMIT")
	}

	restricted := aggregating || p.Distinct
	if len(p.Order) > 0 {
		var ev env
		if restricted {
			ev = env{sc: ns, aggOK: aggregating, sort: &sortInfo{projected: all, roots: roots, aggregating: aggregating, old: sc}}
		} else {
			ev = env{sc: mergedScope(ns, sc)}
		}
		for _, s := range p.Order {
			if aggregating && hasAggregate(s.Expr) && !equalsAny(s.Expr, all) {
				// An ORDER BY expression that combines an aggregation with other
				// terms follows the grouping rule leaf by leaf: a projected
				// compound expression does not make its parts grouping keys.
				c.checkSortGrouping(s.Expr, keys, ns, roots)
			}
			c.expr(s.Expr, ev)
		}
	}
	if where != nil {
		// WITH … WHERE sees the projected names and, unless the projection
		// aggregates, the variables from before it (even with DISTINCT).
		ws := ns
		if !aggregating {
			ws = mergedScope(ns, sc)
		}
		c.predicate(where, env{sc: ws, boolCtx: true})
	}
	if unaliased != nil {
		c.fail(CodeNoExpressionAlias, *unaliased, "expression in WITH must be aliased (use AS)")
	}
	return ns, cols
}

// mergedScope returns a scope seeing the projected names first, then the
// variables from before the projection.
func mergedScope(projected, before *scope) *scope {
	m := newScope(before)
	for k, v := range projected.vars {
		m.vars[k] = v
	}
	m.open = projected.open
	return m
}

// checkGrouping enforces the implicit-grouping rule on an aggregating item.
func (c *checker) checkGrouping(e syntax.Expr, keys []syntax.Expr) {
	matches := func(x syntax.Expr) bool {
		for _, k := range keys {
			if sameExpr(k, x) {
				return true
			}
		}
		return false
	}
	var visit func(e syntax.Expr)
	visit = func(e syntax.Expr) {
		switch x := e.(type) {
		case *syntax.FuncCall:
			if isAggregateCall(x) {
				return // its arguments are aggregated
			}
		case *syntax.Ident, *syntax.Property:
			if r := rootIdent(x); r != nil {
				if !matches(x) {
					c.fail(CodeAmbiguousAggregationExpression, x.Pos(), "`%s` is used inside an aggregating expression without being a grouping key", syntax.Dump(x))
				}
				return
			}
		case *syntax.ListComp:
			visit(x.In)
			return
		case *syntax.Quantifier:
			visit(x.In)
			return
		case *syntax.Reduce:
			visit(x.Init)
			visit(x.In)
			return
		case *syntax.PatternComp, *syntax.SubqueryExpr, *syntax.PatternExpr:
			return
		}
		for _, ch := range children(e) {
			visit(ch)
		}
	}
	visit(e)
}

// constantArg checks a SKIP/LIMIT argument: it must be an integer constant
// that does not depend on variables.
func (c *checker) constantArg(e syntax.Expr, what string) {
	walk(e, func(n syntax.Expr) bool {
		switch x := n.(type) {
		case *syntax.Ident:
			c.fail(CodeNonConstantExpression, x.Pos(), "%s must not depend on variables", what)
		}
		return true
	})
	switch x := e.(type) {
	case *syntax.IntLit:
		if x.Value < 0 {
			c.fail(CodeNegativeIntegerArgument, x.Pos(), "%s must not be negative", what)
		}
	case *syntax.FloatLit:
		c.fail(CodeInvalidArgumentType, x.Pos(), "%s must be an integer, got a float", what)
	}
}

func equalsAny(e syntax.Expr, list []syntax.Expr) bool {
	for _, x := range list {
		if sameExpr(x, e) {
			return true
		}
	}
	return false
}

// checkSortGrouping applies the implicit-grouping rule to an ORDER BY
// expression of an aggregating projection. A leaf is acceptable if it is a
// grouping key or a projected name; otherwise it is ambiguous when its variable
// appears in the projection and undefined when it does not.
func (c *checker) checkSortGrouping(e syntax.Expr, keys []syntax.Expr, ns *scope, roots map[string]bool) {
	var visit func(e syntax.Expr)
	visit = func(e syntax.Expr) {
		switch x := e.(type) {
		case *syntax.FuncCall:
			if isAggregateCall(x) {
				return // validated by sortSpecial
			}
		case *syntax.Ident, *syntax.Property:
			if r := rootIdent(x); r != nil {
				if equalsAny(x, keys) {
					return
				}
				if _, projected := ns.vars[r.Name]; projected {
					return
				}
				if roots[r.Name] {
					c.fail(CodeAmbiguousAggregationExpression, x.Pos(), "`%s` is used inside an aggregating expression without being a grouping key", syntax.Dump(x))
				}
				c.fail(CodeUndefinedVariable, r.Pos(), "variable `%s` not defined", r.Name)
			}
		}
		for _, ch := range children(e) {
			visit(ch)
		}
	}
	visit(e)
}
