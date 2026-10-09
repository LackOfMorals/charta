package interp

import (
	"sync/atomic"

	"github.com/LackOfMorals/charta/cypher/syntax"
)

// reverseDisabled turns off starting a pattern from its last node; tests use it
// to compare both directions on the same data.
var reverseDisabled bool

// reverseUsed counts how often a pattern was walked backwards (for tests).
var reverseUsed atomic.Int64

// startScore ranks how selective a node pattern is as the place to begin
// matching: a variable already bound to a node (3), a property equality SQLite
// can use (2), a label (1), nothing (0).
func (ex *exec) startScore(np *syntax.NodePattern, r row) int {
	if np.Var != "" {
		if _, bound := r[np.Var].(*Node); bound {
			return 3
		}
	}
	if len(ex.scanHints(np, r)) > 0 {
		return 2
	}
	if scanLabel(np.Labels) != "" {
		return 1
	}
	return 0
}

// reversed returns the pattern's elements in the opposite order with every
// relationship direction flipped, if walking the pattern from its last node is
// worth it and cannot change the result: the last node must be a better place to
// start than the first, the pattern must be a plain chain with no path variable
// or group, and no property map or WHERE may refer to a variable the pattern
// itself introduces (it would be evaluated before that variable is bound).
func (ex *exec) reversed(part *syntax.PatternPart, r row) ([]syntax.PatternElem, bool) {
	if reverseDisabled || part.Var != "" || len(part.Elems) < 3 {
		return nil, false
	}
	first, ok1 := part.Elems[0].(*syntax.NodePattern)
	last, ok2 := part.Elems[len(part.Elems)-1].(*syntax.NodePattern)
	if !ok1 || !ok2 || ex.startScore(last, r) <= ex.startScore(first, r) {
		return nil, false
	}
	declared := map[string]bool{}
	for _, el := range part.Elems {
		switch el := el.(type) {
		case *syntax.NodePattern:
			if el.Var != "" {
				declared[el.Var] = true
			}
		case *syntax.RelPattern:
			if el.Var != "" {
				declared[el.Var] = true
			}
			if rangeOf(el) != nil && el.Var != "" {
				return nil, false // the relationship list would come out reversed
			}
		default:
			return nil, false
		}
	}
	for _, el := range part.Elems {
		var props, where syntax.Expr
		switch el := el.(type) {
		case *syntax.NodePattern:
			props, where = el.Props, el.Where
		case *syntax.RelPattern:
			props, where = el.Props, el.Where
		}
		if where != nil || touches(props, declared) {
			return nil, false
		}
	}
	out := make([]syntax.PatternElem, len(part.Elems))
	for i, el := range part.Elems {
		if rp, ok := el.(*syntax.RelPattern); ok {
			el = flipDir(rp)
		}
		out[len(out)-1-i] = el
	}
	return out, true
}

// touches reports whether e refers to one of the named variables (conservatively
// true for expressions that open a nested scope).
func touches(e syntax.Expr, names map[string]bool) bool {
	found := false
	walkExpr(e, func(x syntax.Expr) bool {
		switch x := x.(type) {
		case *syntax.Ident:
			if names[x.Name] {
				found = true
			}
		case *syntax.PatternExpr, *syntax.SubqueryExpr, *syntax.PatternComp:
			found = true
		}
		return !found
	})
	return found
}
