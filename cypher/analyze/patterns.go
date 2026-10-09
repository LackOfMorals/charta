package analyze

import "github.com/LackOfMorals/graphlite/v2/cypher/syntax"

type patMode uint8

const (
	modeMatch patMode = iota
	modeCreate
	modeMerge
	modeLenient // pattern comprehensions and subqueries: just declare variables
)

// patState carries the per-clause state of pattern analysis.
type patState struct {
	mode patMode
	rels map[string]bool // relationship variables seen in this clause
}

// declarePatterns binds the variables of a clause's patterns in order,
// checking for conflicting and repeated bindings. A path variable is bound after
// its elements, so `p = (p)-->()` reports p as already bound.
func (c *checker) declarePatterns(parts []*syntax.PatternPart, sc *scope, st *patState) {
	for _, part := range parts {
		single := len(part.Elems) == 1
		c.declareElems(part.Elems, sc, st, single)
		if part.Var != "" {
			if _, exists := sc.lookup(part.Var); exists {
				c.fail(CodeVariableAlreadyBound, part.Pos(), "variable `%s` already declared", part.Var)
			}
			sc.declare(part.Var, tPath)
		}
	}
}

func (c *checker) declareElems(elems []syntax.PatternElem, sc *scope, st *patState, single bool) {
	for _, el := range elems {
		switch el := el.(type) {
		case *syntax.NodePattern:
			c.declareNode(el, sc, st, single)
		case *syntax.RelPattern:
			c.declareRel(el, sc, st)
		case *syntax.GroupPattern:
			// Variables inside a quantified group are group variables (lists).
			inner := &patState{mode: modeLenient, rels: map[string]bool{}}
			c.declareElems(el.Elems, sc, inner, false)
			if el.Quant != nil {
				for _, inner := range el.Elems {
					name := ""
					switch ie := inner.(type) {
					case *syntax.NodePattern:
						name = ie.Var
					case *syntax.RelPattern:
						name = ie.Var
					}
					if name != "" {
						t, _ := sc.lookup(name)
						sc.declare(name, listOf(t))
					}
				}
			}
			if el.Var != "" {
				sc.declare(el.Var, tAny)
			}
		}
	}
}

func (c *checker) declareNode(n *syntax.NodePattern, sc *scope, st *patState, single bool) {
	if n.Var == "" {
		return
	}
	t, exists := sc.lookup(n.Var)
	switch {
	case !exists:
		sc.declare(n.Var, tNode)
	case t.known() && t.k != kNode:
		c.fail(CodeVariableTypeConflict, n.Pos(), "variable `%s` already declared as %s, not a node", n.Var, t.name())
	case st.mode == modeCreate || st.mode == modeMerge:
		// A bound node may only be referenced bare, as the end of a relationship.
		bare := n.Labels == nil && n.Props == nil && n.Where == nil
		if !bare || single {
			c.fail(CodeVariableAlreadyBound, n.Pos(), "variable `%s` already declared", n.Var)
		}
	}
}

func (c *checker) declareRel(r *syntax.RelPattern, sc *scope, st *patState) {
	if st.mode == modeCreate || st.mode == modeMerge {
		if r.Var != "" {
			if _, bound := sc.lookup(r.Var); bound {
				c.fail(CodeVariableAlreadyBound, r.Pos(), "variable `%s` already declared", r.Var)
			}
		}
		switch {
		case r.Range != nil || r.Quant != nil:
			c.fail(CodeCreatingVarLength, r.Pos(), "variable length relationships cannot be created")
		case !singleType(r.Types):
			c.fail(CodeNoSingleRelationshipType, r.Pos(), "exactly one relationship type must be specified")
		case st.mode == modeCreate && (r.Dir == syntax.DirNone || r.Dir == syntax.DirBoth):
			c.fail(CodeRequiresDirectedRelationship, r.Pos(), "only directed relationships are supported in CREATE")
		}
	}
	if r.Var == "" {
		return
	}
	rt := tRel
	if r.Range != nil || r.Quant != nil {
		rt = listOf(tRel)
	}
	if st.mode == modeMatch {
		if st.rels[r.Var] {
			c.fail(CodeRelationshipUniquenessViolation, r.Pos(), "cannot use the same relationship variable `%s` for multiple patterns", r.Var)
		}
		st.rels[r.Var] = true
	}
	t, exists := sc.lookup(r.Var)
	switch {
	case !exists:
		sc.declare(r.Var, rt)
	case st.mode == modeCreate || st.mode == modeMerge:
		c.fail(CodeVariableAlreadyBound, r.Pos(), "variable `%s` already declared", r.Var)
	case t.known() && t.k != kRel && !(t.k == kList && t.elem != nil && t.elem.k == kRel):
		c.fail(CodeVariableTypeConflict, r.Pos(), "variable `%s` already declared as %s, not a relationship", r.Var, t.name())
	}
}

// singleType reports whether a relationship type expression names exactly one
// type.
func singleType(le syntax.LabelExpr) bool {
	_, ok := le.(*syntax.LabelName) // a literal or dynamic $(expr) type
	return ok
}

// evalPatterns checks the expressions inside patterns (property maps, inline
// WHERE) once all of the clause's variables are bound.
func (c *checker) evalPatterns(parts []*syntax.PatternPart, sc *scope, st *patState) {
	var walk func(elems []syntax.PatternElem)
	cur := sc // the scope expressions are checked in
	props := func(e syntax.Expr) {
		switch p := e.(type) {
		case nil:
		case *syntax.Param:
			if st.mode == modeMatch || st.mode == modeMerge {
				c.fail(CodeInvalidParameterUse, p.Pos(), "parameter maps cannot be used in MATCH or MERGE patterns")
			}
		default:
			c.expr(e, env{sc: cur})
		}
	}
	walk = func(elems []syntax.PatternElem) {
		for _, el := range elems {
			switch el := el.(type) {
			case *syntax.NodePattern:
				props(el.Props)
				if el.Where != nil {
					c.predicate(el.Where, env{sc: cur, boolCtx: true})
				}
				if el.Labels != nil {
					c.labelExpr(el.Labels, cur)
				}
			case *syntax.RelPattern:
				props(el.Props)
				if el.Where != nil {
					c.predicate(el.Where, env{sc: cur, boolCtx: true})
				}
				if el.Types != nil {
					c.labelExpr(el.Types, cur)
				}
			case *syntax.GroupPattern:
				// Inside a quantified group each variable is the single value of
				// one iteration, although outside it is a list of them.
				saved := cur
				if el.Quant != nil {
					cur = newScope(sc)
					for _, inner := range el.Elems {
						name := ""
						switch ie := inner.(type) {
						case *syntax.NodePattern:
							name = ie.Var
						case *syntax.RelPattern:
							name = ie.Var
						}
						if name != "" {
							t, _ := sc.lookup(name)
							cur.declare(name, t.elemType())
						}
					}
				}
				walk(el.Elems)
				if el.Where != nil {
					c.predicate(el.Where, env{sc: cur, boolCtx: true})
				}
				cur = saved
			}
		}
	}
	for _, part := range parts {
		walk(part.Elems)
	}
}

// checkBoundPattern verifies that every named variable of a pattern predicate
// is already bound: a predicate cannot introduce new variables.
func (c *checker) checkBoundPattern(part *syntax.PatternPart, ev env) {
	var walk func(elems []syntax.PatternElem)
	require := func(name string, pos syntax.Pos) {
		if name == "" {
			return
		}
		if _, ok := ev.sc.lookup(name); !ok && !ev.sc.isOpen() {
			c.fail(CodeUndefinedVariable, pos, "variable `%s` not defined", name)
		}
	}
	walk = func(elems []syntax.PatternElem) {
		for _, el := range elems {
			switch el := el.(type) {
			case *syntax.NodePattern:
				require(el.Var, el.Pos())
				if el.Props != nil {
					c.expr(el.Props, env{sc: ev.sc})
				}
			case *syntax.RelPattern:
				require(el.Var, el.Pos())
				if el.Props != nil {
					c.expr(el.Props, env{sc: ev.sc})
				}
			case *syntax.GroupPattern:
				walk(el.Elems)
			}
		}
	}
	walk(part.Elems)
}

// declareLenient binds pattern variables without conflict checks, for pattern
// comprehensions and subqueries.
func (c *checker) declareLenient(parts []*syntax.PatternPart, sc *scope) {
	st := &patState{mode: modeLenient, rels: map[string]bool{}}
	for _, part := range parts {
		c.declareElemsLenient(part.Elems, sc, st)
		if part.Var != "" {
			sc.declare(part.Var, tPath)
		}
	}
}

func (c *checker) declareElemsLenient(elems []syntax.PatternElem, sc *scope, st *patState) {
	for _, el := range elems {
		switch el := el.(type) {
		case *syntax.NodePattern:
			if el.Var != "" {
				if _, ok := sc.lookup(el.Var); !ok {
					sc.declare(el.Var, tNode)
				}
			}
		case *syntax.RelPattern:
			if el.Var != "" {
				if _, ok := sc.lookup(el.Var); !ok {
					sc.declare(el.Var, tRel)
				}
			}
		case *syntax.GroupPattern:
			c.declareElemsLenient(el.Elems, sc, st)
		}
	}
}
