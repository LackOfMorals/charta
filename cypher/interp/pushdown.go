package interp

import (
	"math"
	"strings"

	"github.com/LackOfMorals/graphlite/v2/cypher/syntax"
)

// propHint is a property equality, key = val, used to narrow a node scan in
// SQLite. val is a string, int64 or float64: the types for which SQLite's `=`
// agrees with Cypher's (a boolean would wrongly equal 1 and 0).
type propHint struct {
	key string
	val any
}

// pushedEq is a top-level `var.key = expr` conjunct of a MATCH ... WHERE.
type pushedEq struct {
	variable, key string
	val           syntax.Expr
}

// pushableKey reports whether key is a plain identifier, which makes it safe to
// write inside a JSON path in SQL text.
func pushableKey(key string) bool {
	return key != "" && !strings.ContainsFunc(key, func(r rune) bool {
		return !(r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	})
}

// whereEqualities extracts the `var.key = expr` conjuncts of a WHERE clause.
func whereEqualities(e syntax.Expr) []pushedEq {
	switch e := e.(type) {
	case *syntax.Binary:
		if e.Op == syntax.OpAnd {
			return append(whereEqualities(e.L), whereEqualities(e.R)...)
		}
	case *syntax.Comparison:
		if len(e.Ops) != 1 || e.Ops[0] != syntax.CmpEq {
			return nil
		}
		for i := 0; i < 2; i++ {
			prop, ok := e.Operands[i].(*syntax.Property)
			if !ok {
				continue
			}
			id, ok := prop.Subject.(*syntax.Ident)
			if !ok || !pushableKey(prop.Key) {
				continue
			}
			return []pushedEq{{variable: id.Name, key: prop.Key, val: e.Operands[1-i]}}
		}
	}
	return nil
}

// hintValue evaluates a pushable expression (a literal, a parameter or a bound
// variable) to a scan hint value, or reports false. Anything else is left to
// the Go-side check so no evaluation happens that the query would not do.
func (ex *exec) hintValue(e syntax.Expr, r row) (any, bool) {
	switch e.(type) {
	case *syntax.IntLit, *syntax.FloatLit, *syntax.StringLit, *syntax.Param:
	case *syntax.Ident:
		if _, bound := r[e.(*syntax.Ident).Name]; !bound {
			return nil, false
		}
	default:
		return nil, false
	}
	v, err := ex.eval(e, r)
	if err != nil {
		return nil, false
	}
	switch x := v.(type) {
	case string, int64:
		return x, true
	case float64:
		return x, !math.IsNaN(x)
	}
	return nil, false
}

// scanHints collects the property equalities that restrict candidates for np,
// from its inline property map and from the enclosing MATCH's WHERE.
func (ex *exec) scanHints(np *syntax.NodePattern, r row) []propHint {
	var hints []propHint
	add := func(key string, e syntax.Expr) {
		if v, ok := ex.hintValue(e, r); ok && pushableKey(key) {
			hints = append(hints, propHint{key, v})
		}
	}
	switch p := np.Props.(type) {
	case *syntax.MapLit:
		for _, en := range p.Entries {
			add(en.Key, en.Value)
		}
	case *syntax.Param:
		if m, ok := ex.params[p.Name].(map[string]any); ok {
			for k, v := range m {
				switch x := v.(type) {
				case string, int64:
					if pushableKey(k) {
						hints = append(hints, propHint{k, x})
					}
				case float64:
					if pushableKey(k) && !math.IsNaN(x) {
						hints = append(hints, propHint{k, x})
					}
				}
			}
		}
	}
	if np.Var != "" {
		for _, pe := range ex.pushed {
			if pe.variable == np.Var {
				add(pe.key, pe.val)
			}
		}
	}
	return hints
}
