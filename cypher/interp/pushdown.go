package interp

import (
	"math"
	"strings"
	"sync/atomic"

	"github.com/LackOfMorals/charta/cypher/syntax"
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

// hintExprDisabled limits scan hints to literals, parameters and bound
// variables, as they were before expressions were allowed; tests use it to
// compare both ways on the same data.
var hintExprDisabled bool

// hintsUsed counts the scan hints taken from an expression that is more than a
// literal, parameter or variable (for tests).
var hintsUsed atomic.Int64

// pureFuncs are the functions whose result depends only on their arguments, so
// evaluating them once for a scan hint gives what evaluating them per candidate
// would. (rand(), randomUUID() and the like are not here.)
var pureFuncs = map[string]bool{
	"tointeger": true, "tointegerornull": true, "tofloat": true, "tofloatornull": true,
	"tostring": true, "tostringornull": true, "tolower": true, "toupper": true,
	"trim": true, "ltrim": true, "rtrim": true, "replace": true, "substring": true,
	"left": true, "right": true, "abs": true, "ceil": true, "floor": true,
	"round": true, "sign": true, "sqrt": true, "size": true, "coalesce": true,
	"head": true, "last": true,
}

// hintable reports whether e can be evaluated once, from the row alone, to
// give the value a scan hint compares with: literals, parameters, variables
// already bound in r, arithmetic and concatenation, property and index access,
// and the pure functions above, nested as deep as the query likes. Anything
// that could differ between two evaluations, or that this does not know, is
// left to the check made on each candidate.
func hintable(e syntax.Expr, r row) bool {
	switch e := e.(type) {
	case *syntax.IntLit, *syntax.FloatLit, *syntax.StringLit, *syntax.BoolLit, *syntax.NullLit, *syntax.Param:
		return true
	case *syntax.Ident:
		_, bound := r[e.Name]
		return bound
	case *syntax.Unary:
		return (e.Op == syntax.OpMinus || e.Op == syntax.OpPlus) && hintable(e.X, r)
	case *syntax.Binary:
		switch e.Op {
		case syntax.OpAdd, syntax.OpSub, syntax.OpMul, syntax.OpDiv, syntax.OpMod, syntax.OpPow, syntax.OpConcat:
			return hintable(e.L, r) && hintable(e.R, r)
		}
	case *syntax.Property:
		return hintable(e.Subject, r)
	case *syntax.Subscript:
		return hintable(e.Subject, r) && hintable(e.Index, r)
	case *syntax.FuncCall:
		if len(e.Namespace) != 0 || e.Distinct || e.Star || !pureFuncs[strings.ToLower(e.Name)] {
			return false
		}
		for _, a := range e.Args {
			if !hintable(a, r) {
				return false
			}
		}
		return true
	}
	return false
}

// simpleHint reports whether e is one of the forms hints were limited to
// before expressions were allowed.
func simpleHint(e syntax.Expr, r row) bool {
	switch e := e.(type) {
	case *syntax.IntLit, *syntax.FloatLit, *syntax.StringLit, *syntax.Param:
		return true
	case *syntax.Ident:
		_, bound := r[e.Name]
		return bound
	}
	return false
}

// hintValue evaluates a pushable expression to a scan hint value, or reports
// false. An expression that fails to evaluate or gives a value SQLite's `=` would
// not agree with is left to the Go-side check, so no error is raised that the
// query would not raise.
func (ex *exec) hintValue(e syntax.Expr, r row) (any, bool) {
	if hintExprDisabled {
		if !simpleHint(e, r) {
			return nil, false
		}
	} else if !hintable(e, r) {
		return nil, false
	}
	v, err := ex.eval(e, r)
	if err != nil {
		return nil, false
	}
	switch x := v.(type) {
	case string, int64:
	case float64:
		if math.IsNaN(x) {
			return nil, false
		}
	default:
		return nil, false
	}
	if !simpleHint(e, r) {
		hintsUsed.Add(1)
	}
	return v, true
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
