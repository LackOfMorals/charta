package analyze

import (
	"strings"

	"github.com/LackOfMorals/graphlite/v2/cypher/syntax"
)

// env is the context an expression is checked in.
type env struct {
	sc      *scope
	aggOK   bool      // aggregate functions are allowed here
	inAgg   bool      // inside an aggregate function's arguments
	boolCtx bool      // pattern predicates are allowed here
	sort    *sortInfo // set while checking ORDER BY of an aggregating or DISTINCT projection
}

// child returns the environment for a sub-expression that is not itself a
// predicate position.
func (e env) child() env {
	e.boolCtx = false
	return e
}

// sortInfo describes the projection an ORDER BY expression is checked against.
type sortInfo struct {
	projected   []syntax.Expr   // every projected expression
	roots       map[string]bool // variables referenced by projected expressions
	aggregating bool
	old         *scope // scope before the projection
}

func (c *checker) expr(e syntax.Expr, ev env) typ {
	if ev.sort != nil {
		if t, ok := c.sortSpecial(e, ev); ok {
			return t
		}
	}
	switch e := e.(type) {
	case *syntax.IntLit:
		return tInt
	case *syntax.FloatLit:
		return tFloat
	case *syntax.StringLit:
		return tString
	case *syntax.BoolLit:
		return tBool
	case *syntax.NullLit:
		return tNull
	case *syntax.Param:
		return tAny
	case *syntax.Ident:
		return c.ident(e, ev)
	case *syntax.ListLit:
		ts := make([]typ, len(e.Elems))
		for i, el := range e.Elems {
			ts[i] = c.expr(el, ev.child())
		}
		return listOf(commonElem(ts))
	case *syntax.MapLit:
		for _, en := range e.Entries {
			c.expr(en.Value, ev.child())
		}
		return tMap
	case *syntax.Property:
		st := c.expr(e.Subject, ev.child())
		switch st.k {
		case kInt, kFloat, kString, kBool, kList:
			c.failType(CodeInvalidArgumentType, e.Pos(), "cannot access property `%s` of %s", e.Key, st.name())
		case kPath:
			c.fail(CodeInvalidArgumentType, e.Pos(), "cannot access property `%s` of a path", e.Key)
		}
		return tAny
	case *syntax.Subscript:
		st := c.expr(e.Subject, ev.child())
		c.expr(e.Index, ev.child())
		return st.elemType()
	case *syntax.Slice:
		st := c.expr(e.Subject, ev.child())
		if e.From != nil {
			c.expr(e.From, ev.child())
		}
		if e.To != nil {
			c.expr(e.To, ev.child())
		}
		return st
	case *syntax.Unary:
		return c.unary(e, ev)
	case *syntax.Binary:
		return c.binary(e, ev)
	case *syntax.Comparison:
		for _, o := range e.Operands {
			c.expr(o, ev.child())
		}
		return tBool
	case *syntax.IsNull:
		c.expr(e.X, ev.child())
		return tBool
	case *syntax.TypePredicate:
		c.expr(e.X, ev.child())
		return tBool
	case *syntax.Normalized:
		c.expr(e.X, ev.child())
		return tBool
	case *syntax.HasLabels:
		c.expr(e.X, ev.child())
		c.labelExpr(e.Labels, ev.sc)
		return tBool
	case *syntax.FuncCall:
		return c.funcCall(e, ev)
	case *syntax.Case:
		if e.Subject != nil {
			c.expr(e.Subject, ev.child())
		}
		for _, w := range e.Whens {
			cev := ev.child()
			cev.boolCtx = e.Subject == nil
			c.expr(w.Cond, cev)
			c.expr(w.Then, ev.child())
		}
		if e.Else != nil {
			c.expr(e.Else, ev.child())
		}
		return tAny
	case *syntax.ListComp:
		inT := c.expr(e.In, ev.child())
		inner := newScope(ev.sc)
		inner.declare(e.Var, inT.elemType())
		iev := env{sc: inner}
		if e.Where != nil {
			c.predicate(e.Where, iev)
		}
		if e.Proj != nil {
			return listOf(c.expr(e.Proj, iev))
		}
		return listOf(inT.elemType())
	case *syntax.PatternComp:
		inner := newScope(ev.sc)
		c.declareLenient([]*syntax.PatternPart{e.Pattern}, inner)
		iev := env{sc: inner}
		if e.Where != nil {
			c.predicate(e.Where, iev)
		}
		return listOf(c.expr(e.Proj, iev))
	case *syntax.Quantifier:
		inT := c.expr(e.In, ev.child())
		inner := newScope(ev.sc)
		inner.declare(e.Var, inT.elemType())
		if e.Where != nil {
			c.predicate(e.Where, env{sc: inner})
		}
		return tBool
	case *syntax.Reduce:
		initT := c.expr(e.Init, ev.child())
		inT := c.expr(e.In, ev.child())
		inner := newScope(ev.sc)
		inner.declare(e.Acc, initT)
		inner.declare(e.Var, inT.elemType())
		c.expr(e.Expr, env{sc: inner})
		return tAny
	case *syntax.SubqueryExpr:
		return c.subquery(e, ev)
	case *syntax.PatternExpr:
		if e.Part.Func == syntax.FuncNone {
			if !ev.boolCtx {
				c.fail(CodeUnexpectedSyntax, e.Pos(), "a pattern expression is only allowed as a predicate; use a pattern comprehension or EXISTS { }")
			}
			c.checkBoundPattern(e.Part, ev)
			return tBool
		}
		c.checkBoundPattern(e.Part, ev)
		return tPath
	case *syntax.MapProjection:
		c.expr(e.Subject, ev.child())
		for _, it := range e.Items {
			switch it.Kind {
			case syntax.ProjLiteral:
				c.expr(it.Value, ev.child())
			case syntax.ProjVariable:
				c.ident(&syntax.Ident{Loc: it.Loc, Name: it.Key}, ev)
			}
		}
		return tMap
	}
	return tAny
}

func (c *checker) ident(e *syntax.Ident, ev env) typ {
	if t, ok := ev.sc.lookup(e.Name); ok {
		return t
	}
	if ev.sc.isOpen() {
		return tAny
	}
	if ev.sort != nil && ev.sort.aggregating && ev.sort.roots[e.Name] {
		c.fail(CodeAmbiguousAggregationExpression, e.Pos(), "`%s` is used inside an aggregating expression without being a grouping key", e.Name)
	}
	c.fail(CodeUndefinedVariable, e.Pos(), "variable `%s` not defined", e.Name)
	return tAny
}

func (c *checker) unary(e *syntax.Unary, ev env) typ {
	switch e.Op {
	case syntax.OpNot:
		bev := ev.child()
		bev.boolCtx = true
		t := c.expr(e.X, bev)
		if !t.acceptable(kBool) {
			c.fail(CodeInvalidArgumentType, e.X.Pos(), "NOT expects a boolean, got %s", t.name())
		}
		return tBool
	}
	t := c.expr(e.X, ev.child())
	if !t.acceptable(kInt, kFloat) {
		c.fail(CodeInvalidArgumentType, e.X.Pos(), "unary %s expects a number, got %s", e.Op, t.name())
	}
	return t
}

func (c *checker) binary(e *syntax.Binary, ev env) typ {
	switch e.Op {
	case syntax.OpAnd, syntax.OpOr, syntax.OpXor:
		bev := ev.child()
		bev.boolCtx = true
		for _, operand := range []syntax.Expr{e.L, e.R} {
			t := c.expr(operand, bev)
			if !t.acceptable(kBool) {
				c.fail(CodeInvalidArgumentType, operand.Pos(), "%s expects booleans, got %s", e.Op, t.name())
			}
		}
		return tBool
	case syntax.OpIn:
		c.expr(e.L, ev.child())
		rt := c.expr(e.R, ev.child())
		if !rt.acceptable(kList) {
			c.fail(CodeInvalidArgumentType, e.R.Pos(), "IN expects a list, got %s", rt.name())
		}
		return tBool
	case syntax.OpStartsWith, syntax.OpEndsWith, syntax.OpContains, syntax.OpRegexMatch:
		c.expr(e.L, ev.child())
		c.expr(e.R, ev.child())
		return tBool
	case syntax.OpConcat:
		c.expr(e.L, ev.child())
		c.expr(e.R, ev.child())
		return tString
	}
	lt, rt := c.expr(e.L, ev.child()), c.expr(e.R, ev.child())
	if e.Op == syntax.OpAdd {
		return numericResult(lt, rt, false)
	}
	for _, p := range []struct {
		t typ
		e syntax.Expr
	}{{lt, e.L}, {rt, e.R}} {
		if !p.t.acceptable(kInt, kFloat) {
			c.fail(CodeInvalidArgumentType, p.e.Pos(), "operator %s expects numbers, got %s", e.Op, p.t.name())
		}
	}
	return numericResult(lt, rt, e.Op == syntax.OpPow)
}

// numericResult infers the result type of an arithmetic operator.
func numericResult(l, r typ, pow bool) typ {
	switch {
	case pow && l.numeric() && r.numeric():
		return tFloat
	case l.k == kInt && r.k == kInt:
		return tInt
	case l.numeric() && r.numeric():
		return tFloat
	}
	return tAny
}

func (c *checker) funcCall(e *syntax.FuncCall, ev env) typ {
	fi, ok := lookupFunction(e.Namespace, e.Name)
	if !ok {
		c.fail(CodeUnknownFunction, e.Pos(), "unknown function `%s`", strings.Join(append(append([]string{}, e.Namespace...), e.Name), "."))
	}
	name := strings.ToLower(e.Name)
	if fi.aggregate {
		if ev.inAgg {
			c.fail(CodeNestedAggregation, e.Pos(), "aggregate functions cannot be nested")
		}
		if !ev.aggOK {
			c.fail(CodeInvalidAggregation, e.Pos(), "aggregate function %s is not allowed here", e.Name)
		}
		aev := ev.child()
		aev.inAgg, aev.aggOK = true, false
		for _, a := range e.Args {
			c.expr(a, aev)
		}
		return fi.ret
	}
	if ev.inAgg && fi.nondeterminic {
		c.fail(CodeNonConstantExpression, e.Pos(), "non-deterministic function %s cannot be used in an aggregation", e.Name)
	}
	aev := ev.child()
	aev.boolCtx = name == "exists" // exists((a)-->(b)) is a predicate
	args := make([]typ, len(e.Args))
	for i, a := range e.Args {
		if _, isPattern := a.(*syntax.PatternExpr); isPattern && name == "size" {
			c.fail(CodeUnexpectedSyntax, a.Pos(), "size() does not accept a pattern; use COUNT { } or a pattern comprehension")
		}
		args[i] = c.expr(a, aev)
	}
	if len(args) == 1 {
		c.checkArg(name, e.Args[0], args[0])
	}
	return fi.ret
}

// checkArg applies the argument-type rules of the functions the TCK exercises.
func (c *checker) checkArg(name string, arg syntax.Expr, t typ) {
	bad := func() {
		c.fail(CodeInvalidArgumentType, arg.Pos(), "%s() does not accept %s", name, t.name())
	}
	switch name {
	case "length":
		if t.k == kNode || t.k == kRel {
			bad()
		}
	case "type":
		if !t.acceptable(kRel) {
			bad()
		}
	case "properties":
		if !t.acceptable(kNode, kRel, kMap) {
			bad()
		}
	case "labels":
		if !t.acceptable(kNode) {
			bad()
		}
	case "size":
		if !t.acceptable(kList, kString) {
			bad()
		}
	}
}

func (c *checker) subquery(e *syntax.SubqueryExpr, ev env) typ {
	inner := newScope(ev.sc)
	switch {
	case e.Query != nil:
		c.rejectUpdates(e.Query)
		c.body(e.Query, inner)
	default:
		c.declareLenient(e.Patterns, inner)
		if e.Where != nil {
			c.predicate(e.Where, env{sc: inner, boolCtx: true})
		}
	}
	switch e.Kind {
	case syntax.SubqueryCount:
		return tInt
	case syntax.SubqueryCollect:
		return typ{k: kList}
	}
	return tBool
}

// rejectUpdates reports an updating clause inside an EXISTS/COUNT/COLLECT body.
func (c *checker) rejectUpdates(b syntax.Body) {
	check := func(q *syntax.SingleQuery) {
		for _, cl := range q.Clauses {
			switch cl.(type) {
			case *syntax.Create, *syntax.Merge, *syntax.Set, *syntax.Remove, *syntax.Delete, *syntax.Foreach:
				c.fail(CodeInvalidClauseComposition, cl.Pos(), "updating clauses are not allowed in a subquery expression")
			}
		}
	}
	switch b := b.(type) {
	case *syntax.SingleQuery:
		check(b)
	case *syntax.UnionQuery:
		for _, q := range b.Queries {
			check(q)
		}
	}
}

// sortSpecial handles ORDER BY sub-expressions of an aggregating or DISTINCT
// projection: an expression equal to a projected one is accepted as is, and an
// aggregate call is checked against the scope before the projection.
func (c *checker) sortSpecial(e syntax.Expr, ev env) (typ, bool) {
	for _, p := range ev.sort.projected {
		if sameExpr(p, e) {
			return tAny, true
		}
	}
	if f, ok := e.(*syntax.FuncCall); ok && isAggregateCall(f) && ev.sort.aggregating {
		// ORDER BY can only use aggregations that the projection computes.
		c.fail(CodeUndefinedVariable, f.Pos(), "ORDER BY cannot introduce the aggregation %s; project it first", syntax.Dump(f))
	}
	return tAny, false
}

// ─── expression helpers ──────────────────────────────────────────────────────

func isAggregateCall(f *syntax.FuncCall) bool {
	if len(f.Namespace) > 0 {
		return false
	}
	fi, ok := builtins[strings.ToLower(f.Name)]
	return ok && fi.aggregate
}

// children returns the direct sub-expressions of e that share its scope.
func children(e syntax.Expr) []syntax.Expr {
	switch e := e.(type) {
	case *syntax.Binary:
		return []syntax.Expr{e.L, e.R}
	case *syntax.Unary:
		return []syntax.Expr{e.X}
	case *syntax.Comparison:
		return e.Operands
	case *syntax.IsNull:
		return []syntax.Expr{e.X}
	case *syntax.TypePredicate:
		return []syntax.Expr{e.X}
	case *syntax.Normalized:
		return []syntax.Expr{e.X}
	case *syntax.HasLabels:
		return []syntax.Expr{e.X}
	case *syntax.Property:
		return []syntax.Expr{e.Subject}
	case *syntax.Subscript:
		return []syntax.Expr{e.Subject, e.Index}
	case *syntax.Slice:
		out := []syntax.Expr{e.Subject}
		if e.From != nil {
			out = append(out, e.From)
		}
		if e.To != nil {
			out = append(out, e.To)
		}
		return out
	case *syntax.FuncCall:
		return e.Args
	case *syntax.Case:
		var out []syntax.Expr
		if e.Subject != nil {
			out = append(out, e.Subject)
		}
		for _, w := range e.Whens {
			out = append(out, w.Cond, w.Then)
		}
		if e.Else != nil {
			out = append(out, e.Else)
		}
		return out
	case *syntax.ListLit:
		return e.Elems
	case *syntax.MapLit:
		out := make([]syntax.Expr, len(e.Entries))
		for i, en := range e.Entries {
			out[i] = en.Value
		}
		return out
	case *syntax.ListComp:
		out := []syntax.Expr{e.In}
		if e.Where != nil {
			out = append(out, e.Where)
		}
		if e.Proj != nil {
			out = append(out, e.Proj)
		}
		return out
	case *syntax.PatternComp:
		out := []syntax.Expr{e.Proj}
		if e.Where != nil {
			out = append(out, e.Where)
		}
		return out
	case *syntax.Quantifier:
		out := []syntax.Expr{e.In}
		if e.Where != nil {
			out = append(out, e.Where)
		}
		return out
	case *syntax.Reduce:
		return []syntax.Expr{e.Init, e.In, e.Expr}
	case *syntax.MapProjection:
		out := []syntax.Expr{e.Subject}
		for _, it := range e.Items {
			if it.Value != nil {
				out = append(out, it.Value)
			}
		}
		return out
	}
	return nil
}

// walk visits e and its sub-expressions in pre-order; fn returns false to skip
// a node's children.
func walk(e syntax.Expr, fn func(syntax.Expr) bool) {
	if e == nil || !fn(e) {
		return
	}
	for _, ch := range children(e) {
		walk(ch, fn)
	}
}

// hasAggregate reports whether e contains an aggregate function call.
func hasAggregate(e syntax.Expr) bool {
	found := false
	walk(e, func(n syntax.Expr) bool {
		if f, ok := n.(*syntax.FuncCall); ok && isAggregateCall(f) {
			found = true
		}
		return !found
	})
	return found
}

// rootIdent returns the variable at the root of a property-access chain
// (`a`, `a.b`, `a.b.c`), or nil.
func rootIdent(e syntax.Expr) *syntax.Ident {
	for {
		switch x := e.(type) {
		case *syntax.Ident:
			return x
		case *syntax.Property:
			e = x.Subject
		default:
			return nil
		}
	}
}

// identsIn collects the variable names referenced by e.
func identsIn(e syntax.Expr, into map[string]bool) {
	walk(e, func(n syntax.Expr) bool {
		if id, ok := n.(*syntax.Ident); ok {
			into[id.Name] = true
		}
		return true
	})
}
