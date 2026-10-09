package analyze

import (
	"fmt"
	"sort"
	"strings"

	"github.com/LackOfMorals/charta/cypher/proc"
	"github.com/LackOfMorals/charta/cypher/syntax"
)

// Check analyses a parsed statement and returns the first compile-time error
// as an *Error, or nil. It reports only what it can prove: anything whose type
// or binding is not statically known is accepted.
func Check(st *syntax.Statement) error { return CheckWith(st, nil) }

// CheckWith is Check with a registry of user-registered procedures, consulted
// in addition to the built-in db.* procedures.
func CheckWith(st *syntax.Statement, reg proc.Registry) (err error) {
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(*Error)
			if !ok {
				panic(r)
			}
			err = e
		}
	}()
	c := &checker{reg: reg, standalone: isStandaloneCall(st)}
	c.body(st.Body, newScope(nil))
	return nil
}

type checker struct {
	reg proc.Registry
	// standalone is true when the statement is a lone CALL.
	standalone bool
}

// fail aborts the analysis with a SyntaxError.
func (c *checker) fail(code string, pos syntax.Pos, format string, args ...any) {
	panic(&Error{Class: ClassSyntax, Code: code, Pos: pos, Msg: sprintf(format, args...)})
}

// failType aborts the analysis with a TypeError.
func (c *checker) failType(code string, pos syntax.Pos, format string, args ...any) {
	panic(&Error{Class: ClassType, Code: code, Pos: pos, Msg: sprintf(format, args...)})
}

// body analyses a statement body and returns the output column names of its
// final RETURN (nil if it has none).
func (c *checker) body(b syntax.Body, outer *scope) []string {
	switch b := b.(type) {
	case *syntax.SingleQuery:
		return c.query(b, outer)
	case *syntax.UnionQuery:
		var first []string
		for i, q := range b.Queries {
			cols := c.query(q, outer)
			if i == 0 {
				first = cols
			} else if !sameSet(first, cols) {
				c.fail(CodeDifferentColumnsInUnion, q.Pos(), "all sub queries in a UNION must have the same column names")
			}
		}
		return first
	case *syntax.Conditional:
		for _, w := range b.Branches {
			c.expr(w.Cond, env{sc: outer, boolCtx: true})
			c.body(w.Body, outer)
		}
		if b.Else != nil {
			c.body(b.Else, outer)
		}
	}
	return nil
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// query analyses the clauses of one query in order.
func (c *checker) query(q *syntax.SingleQuery, outer *scope) []string {
	sc := newScope(outer)
	var cols []string
	for _, cl := range q.Clauses {
		sc, cols = c.clause(cl, sc, cols)
	}
	return cols
}

// clause analyses one clause. It returns the scope after the clause and the
// output columns so far.
func (c *checker) clause(cl syntax.Clause, sc *scope, cols []string) (*scope, []string) {
	switch cl := cl.(type) {
	case *syntax.Match:
		st := &patState{mode: modeMatch, rels: map[string]bool{}}
		c.declarePatterns(cl.Patterns, sc, st)
		c.evalPatterns(cl.Patterns, sc, st)
		if cl.Where != nil {
			c.predicate(cl.Where, env{sc: sc, boolCtx: true})
		}
	case *syntax.Unwind:
		t := c.expr(cl.Expr, env{sc: sc})
		sc.declare(cl.Var, t.elemType())
	case *syntax.Create:
		st := &patState{mode: modeCreate, rels: map[string]bool{}}
		c.declarePatterns(cl.Patterns, sc, st)
		c.evalPatterns(cl.Patterns, sc, st)
	case *syntax.Merge:
		st := &patState{mode: modeMerge, rels: map[string]bool{}}
		parts := []*syntax.PatternPart{cl.Pattern}
		c.declarePatterns(parts, sc, st)
		c.evalPatterns(parts, sc, st)
		for _, a := range cl.Actions {
			c.setItems(a.Items, sc)
		}
	case *syntax.Set:
		c.setItems(cl.Items, sc)
	case *syntax.Remove:
		for _, it := range cl.Items {
			c.expr(it.Target, env{sc: sc})
		}
	case *syntax.Delete:
		for _, e := range cl.Exprs {
			if hl, ok := e.(*syntax.HasLabels); ok {
				c.fail(CodeInvalidDelete, hl.Pos(), "DELETE cannot remove labels or relationship types")
			}
			t := c.expr(e, env{sc: sc})
			if !t.acceptable(kNode, kRel, kPath, kList) {
				c.fail(CodeInvalidArgumentType, e.Pos(), "DELETE expects a node, relationship or path, got %s", t.name())
			}
		}
	case *syntax.Foreach:
		t := c.expr(cl.In, env{sc: sc})
		inner := newScope(sc)
		inner.declare(cl.Var, t.elemType())
		for _, b := range cl.Body {
			inner, _ = c.clause(b, inner, nil)
		}
	case *syntax.With:
		return c.projectionClause(cl.Pos(), &cl.Projection, cl.Where, false, sc)
	case *syntax.Return:
		return c.projectionClause(cl.Pos(), &cl.Projection, nil, true, sc)
	case *syntax.Call:
		sig := c.lookupProcedure(strings.Join(cl.Name, "."))
		if sig == nil && !knownProcedure(cl.Name) {
			panic(&Error{Class: ClassProcedure, Code: CodeProcedureNotFound, Pos: cl.Pos(),
				Msg: sprintf("there is no procedure with the name `%s` registered", strings.Join(cl.Name, "."))})
		}
		for _, a := range cl.Args {
			c.expr(a, env{sc: sc})
		}
		if sig != nil {
			c.checkProcedureArgs(cl, sig)
		}
		if cl.Yield != nil {
			if cl.Yield.Star {
				if !c.standalone {
					c.fail(CodeUnexpectedSyntax, cl.Yield.Pos(), "YIELD * is only allowed in a standalone CALL")
				}
				sc.open = true
			}
			yielded := map[string]bool{}
			for _, y := range cl.Yield.Items {
				name := y.Alias
				if name == "" {
					name = y.Name
				}
				if _, bound := sc.lookup(name); bound || yielded[name] {
					c.fail(CodeVariableAlreadyBound, y.Pos(), "variable `%s` already declared", name)
				}
				yielded[name] = true
				sc.declare(name, tAny)
			}
			if cl.Yield.Where != nil {
				c.predicate(cl.Yield.Where, env{sc: sc, boolCtx: true})
			}
		}
	case *syntax.CallSubquery:
		inner := newScope(sc)
		for _, name := range c.body(cl.Body, inner) {
			sc.declare(name, tAny)
		}
	case *syntax.LoadCSV:
		c.expr(cl.From, env{sc: sc})
		if cl.FieldTerminator != nil {
			c.expr(cl.FieldTerminator, env{sc: sc})
		}
		sc.declare(cl.Var, tAny)
	case *syntax.Filter:
		c.predicate(cl.Cond, env{sc: sc, boolCtx: true})
	case *syntax.Let:
		for _, it := range cl.Items {
			t := c.expr(it.Expr, env{sc: sc})
			sc.declare(it.Var, t)
		}
	}
	return sc, cols
}

func (c *checker) setItems(items []syntax.SetItem, sc *scope) {
	for _, it := range items {
		c.expr(it.Target, env{sc: sc})
		if it.Labels != nil {
			c.labelExpr(it.Labels, sc)
		}
		if it.Value != nil {
			c.expr(it.Value, env{sc: sc})
		}
	}
}

func (c *checker) labelExpr(le syntax.LabelExpr, sc *scope) {
	switch le := le.(type) {
	case *syntax.LabelName:
		if le.Dynamic != nil {
			c.expr(le.Dynamic, env{sc: sc})
		}
	case *syntax.LabelNot:
		c.labelExpr(le.X, sc)
	case *syntax.LabelAnd:
		c.labelExpr(le.L, sc)
		c.labelExpr(le.R, sc)
	case *syntax.LabelOr:
		c.labelExpr(le.L, sc)
		c.labelExpr(le.R, sc)
	}
}

// predicate checks an expression used as a boolean condition.
func (c *checker) predicate(e syntax.Expr, ev env) {
	ev.boolCtx = true
	t := c.expr(e, ev)
	if !t.acceptable(kBool) {
		c.fail(CodeInvalidArgumentType, e.Pos(), "a predicate must be a boolean, got %s", t.name())
	}
}

var typeNames = map[kind]string{
	kNull: "null", kBool: "a boolean", kInt: "an integer", kFloat: "a float", kString: "a string",
	kList: "a list", kMap: "a map", kNode: "a node", kRel: "a relationship", kPath: "a path",
}

func (t typ) name() string {
	if n, ok := typeNames[t.k]; ok {
		return n
	}
	return "an unknown value"
}

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// knownProcedure reports whether a procedure name is in a namespace the
// database provides (db.*, dbms.*). charta has no user-defined procedures.
func knownProcedure(name []string) bool {
	if len(name) < 2 {
		return false
	}
	switch strings.ToLower(name[0]) {
	case "db", "dbms", "tx":
		return true
	}
	return false
}

// lookupProcedure finds a registered or built-in procedure signature.
func (c *checker) lookupProcedure(name string) *proc.Signature {
	if c.reg != nil {
		if sig, ok := c.reg.Lookup(name); ok {
			return sig
		}
	}
	for _, sig := range proc.Builtin {
		if proc.Key(sig.Name) == proc.Key(name) {
			return sig
		}
	}
	return nil
}

// checkProcedureArgs validates argument count, passing mode and literal types.
func (c *checker) checkProcedureArgs(cl *syntax.Call, sig *proc.Signature) {
	if cl.ArgsOmitted {
		if len(sig.Inputs) > 0 && !c.standalone {
			c.fail(CodeInvalidArgumentPassingMode, cl.Pos(), "procedure `%s` takes arguments, which cannot be passed implicitly in a query", sig.Name)
		}
		return // implicit arguments come from parameters; checked at run time
	}
	if len(cl.Args) != len(sig.Inputs) {
		c.fail(CodeInvalidNumberOfArguments, cl.Pos(), "procedure `%s` expects %d arguments but got %d",
			sig.Name, len(sig.Inputs), len(cl.Args))
	}
	for i, a := range cl.Args {
		if kind := literalKind(a); kind != "" && !sig.Inputs[i].Accepts(kind) {
			c.fail(CodeInvalidArgumentType, a.Pos(), "argument `%s` of procedure `%s` expects %s",
				sig.Inputs[i].Name, sig.Name, sig.Inputs[i].Type)
		}
	}
}

// literalKind returns the proc type name of a literal expression, or "".
func literalKind(e syntax.Expr) string {
	switch e.(type) {
	case *syntax.IntLit:
		return "INTEGER"
	case *syntax.FloatLit:
		return "FLOAT"
	case *syntax.StringLit:
		return "STRING"
	case *syntax.BoolLit:
		return "BOOLEAN"
	case *syntax.NullLit:
		return "NULL"
	case *syntax.ListLit:
		return "LIST"
	case *syntax.MapLit:
		return "MAP"
	}
	return ""
}

// isStandaloneCall reports whether the statement consists of one CALL clause.
func isStandaloneCall(st *syntax.Statement) bool {
	q, ok := st.Body.(*syntax.SingleQuery)
	if !ok || len(q.Clauses) != 1 {
		return false
	}
	_, ok = q.Clauses[0].(*syntax.Call)
	return ok
}
