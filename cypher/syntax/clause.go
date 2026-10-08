package syntax

// Clause grammar.
//
//	MATCH:      [OPTIONAL] MATCH pattern-part (',' pattern-part)* [WHERE expr]
//	UNWIND:     UNWIND expr AS var
//	WITH/RETURN: [DISTINCT] ('*' | item) (',' item)* [ORDER BY …] [SKIP|OFFSET n] [LIMIT n]
//	            (WITH additionally takes a trailing [WHERE expr])
//	CREATE:     CREATE pattern-part (',' pattern-part)*
//	MERGE:      MERGE pattern-part (ON (CREATE|MATCH) SET item (',' item)*)*
//	SET:        SET item (',' item)*     item := target '=' expr | var ('='|'+=') expr | var ':' labels
//	REMOVE:     REMOVE target | var ':' labels   (comma separated)
//	DELETE:     [DETACH|NODETACH] DELETE expr (',' expr)*
//	FOREACH:    FOREACH '(' var IN expr '|' updating-clause+ ')'
//	CALL:       [OPTIONAL] CALL name ['(' [expr (',' expr)*] ')'] [YIELD ('*' | item (',' item)*) [WHERE expr]]

// parseClauseOpt parses one clause, or returns nil, consuming nothing, if the
// cursor is not at a clause keyword.
func (p *parser) parseClauseOpt() Clause {
	t := p.cur()
	if t.Kind != IDENT {
		return nil
	}
	switch t.Keyword {
	case KwMatch:
		return p.parseMatch(false)
	case KwOptional:
		switch {
		case p.peek(1).Is(KwMatch):
			return p.parseMatch(true)
		case p.peek(1).Is(KwCall):
			return p.parseCall()
		}
		p.next()
		p.unexpected("MATCH", "CALL")
	case KwUnwind:
		return p.parseUnwind()
	case KwWith:
		return p.parseWith()
	case KwReturn:
		return p.parseReturn()
	case KwCreate:
		p.next()
		return &Create{Loc{t.Pos}, p.parsePatternParts()}
	case KwMerge:
		return p.parseMerge()
	case KwSet:
		p.next()
		return &Set{Loc{t.Pos}, p.parseSetItems()}
	case KwRemove:
		return p.parseRemove()
	case KwDelete, KwDetach:
		return p.parseDelete()
	case KwForeach:
		return p.parseForeach()
	case KwCall:
		return p.parseCall()
	}
	if t.isWord("nodetach") && p.peek(1).Is(KwDelete) {
		return p.parseDelete()
	}
	return nil
}

// ─── reading clauses ─────────────────────────────────────────────────────────

func (p *parser) parsePatternParts() []*PatternPart {
	parts := []*PatternPart{p.parsePatternPart()}
	for p.accept(COMMA) {
		parts = append(parts, p.parsePatternPart())
	}
	return parts
}

func (p *parser) parseMatch(optional bool) Clause {
	start := p.cur().Pos
	if optional {
		p.expectKw(KwOptional)
	}
	p.expectKw(KwMatch)
	m := &Match{Loc: Loc{start}, Optional: optional, Patterns: p.parsePatternParts()}
	if p.acceptKw(KwWhere) {
		m.Where = p.parseExpr()
	}
	return m
}

func (p *parser) parseUnwind() Clause {
	start := p.expectKw(KwUnwind)
	u := &Unwind{Loc: Loc{start.Pos}, Expr: p.parseExpr()}
	p.expectKw(KwAs)
	u.Var, _ = p.name("variable")
	return u
}

// ─── WITH / RETURN ───────────────────────────────────────────────────────────

func (p *parser) parseWith() Clause {
	start := p.expectKw(KwWith)
	w := &With{Loc: Loc{start.Pos}, Projection: p.parseProjection()}
	if p.acceptKw(KwWhere) {
		w.Where = p.parseExpr()
	}
	return w
}

func (p *parser) parseReturn() Clause {
	start := p.expectKw(KwReturn)
	return &Return{Loc: Loc{start.Pos}, Projection: p.parseProjection()}
}

// parseProjection parses the body shared by WITH and RETURN. SKIP/OFFSET and
// LIMIT are accepted in either order; each may appear once.
func (p *parser) parseProjection() Projection {
	var proj Projection
	proj.Distinct = p.acceptKw(KwDistinct)
	if p.accept(STAR) {
		proj.Star = true
		if p.accept(COMMA) {
			proj.Items = p.parseProjectionItems()
		}
	} else {
		proj.Items = p.parseProjectionItems()
	}
	proj.Order, proj.Skip, proj.Limit = p.parseOrderSkipLimit()
	return proj
}

func (p *parser) parseProjectionItems() []ProjectionItem {
	var items []ProjectionItem
	for {
		first := p.i
		pos := p.cur().Pos
		e := p.parseExpr()
		item := ProjectionItem{Loc: Loc{pos}, Expr: e, Source: p.sourceBetween(first, p.i)}
		if p.acceptKw(KwAs) {
			item.Alias, _ = p.name("alias")
		}
		items = append(items, item)
		if !p.accept(COMMA) {
			return items
		}
	}
}

// parseOrderSkipLimit parses the optional ORDER BY, SKIP|OFFSET and LIMIT
// suffix of a projection.
func (p *parser) parseOrderSkipLimit() (order []SortItem, skip, limit Expr) {
	if p.atKw(KwOrder) {
		p.next()
		p.expectKw(KwBy)
		for {
			pos := p.cur().Pos
			item := SortItem{Loc: Loc{pos}, Expr: p.parseExpr()}
			switch {
			case p.acceptKw(KwDesc), p.acceptKw(KwDescending):
				item.Desc = true
			case p.acceptKw(KwAsc), p.acceptKw(KwAscending):
			}
			order = append(order, item)
			if !p.accept(COMMA) {
				break
			}
		}
	}
	for {
		switch {
		case p.atKw(KwSkip) || p.atKw(KwOffset):
			t := p.next()
			if skip != nil {
				p.fail(t.Pos, "duplicate SKIP/OFFSET")
			}
			skip = p.parseExpr()
		case p.atKw(KwLimit):
			t := p.next()
			if limit != nil {
				p.fail(t.Pos, "duplicate LIMIT")
			}
			limit = p.parseExpr()
		default:
			return order, skip, limit
		}
	}
}

// ─── updating clauses ────────────────────────────────────────────────────────

func (p *parser) parseMerge() Clause {
	start := p.expectKw(KwMerge)
	m := &Merge{Loc: Loc{start.Pos}, Pattern: p.parsePatternPart()}
	for p.atKw(KwOn) {
		on := p.next()
		act := MergeAction{Loc: Loc{on.Pos}}
		switch {
		case p.acceptKw(KwCreate):
			act.OnCreate = true
		case p.acceptKw(KwMatch):
		default:
			p.unexpected("CREATE", "MATCH")
		}
		p.expectKw(KwSet)
		act.Items = p.parseSetItems()
		m.Actions = append(m.Actions, act)
	}
	return m
}

func (p *parser) parseSetItems() []SetItem {
	var items []SetItem
	for {
		items = append(items, p.parseSetItem())
		if !p.accept(COMMA) {
			return items
		}
	}
}

// parseSetItem parses one SET assignment. The target is read as a postfix
// expression, so `n:Label` arrives as a HasLabels node.
func (p *parser) parseSetItem() SetItem {
	pos := p.cur().Pos
	target := p.parsePostfix()
	item := SetItem{Loc: Loc{pos}}
	switch t := target.(type) {
	case *HasLabels:
		id, ok := t.X.(*Ident)
		if !ok {
			p.fail(pos, "SET label target must be a variable")
		}
		item.Kind, item.Target, item.Labels = SetLabels, id, t.Labels
		return item
	case *Property, *Subscript:
		p.expect(EQ)
		item.Kind, item.Target, item.Value = SetProperty, target, p.parseExpr()
		return item
	case *Ident:
		item.Target = target
		switch {
		case p.accept(EQ):
			item.Kind = SetReplace
		case p.accept(PLUSEQ):
			item.Kind = SetMerge
		default:
			p.unexpected("=", "+=", ":")
		}
		item.Value = p.parseExpr()
		return item
	}
	p.fail(pos, "invalid SET target")
	return item
}

func (p *parser) parseRemove() Clause {
	start := p.expectKw(KwRemove)
	r := &Remove{Loc: Loc{start.Pos}}
	for {
		pos := p.cur().Pos
		item := RemoveItem{Loc: Loc{pos}}
		switch t := p.parsePostfix().(type) {
		case *HasLabels:
			id, ok := t.X.(*Ident)
			if !ok {
				p.fail(pos, "REMOVE label target must be a variable")
			}
			item.Target, item.Labels = id, t.Labels
		case *Property:
			item.Target = t
		case *Subscript:
			item.Target = t
		default:
			p.fail(pos, "REMOVE expects a property or labels")
		}
		r.Items = append(r.Items, item)
		if !p.accept(COMMA) {
			return r
		}
	}
}

func (p *parser) parseDelete() Clause {
	start := p.cur().Pos
	d := &Delete{Loc: Loc{start}}
	switch {
	case p.acceptKw(KwDetach):
		d.Detach = true
	case p.cur().isWord("nodetach"):
		p.next()
	}
	p.expectKw(KwDelete)
	for {
		d.Exprs = append(d.Exprs, p.parseExpr())
		if !p.accept(COMMA) {
			return d
		}
	}
}

func (p *parser) parseForeach() Clause {
	start := p.expectKw(KwForeach)
	p.expect(LPAREN)
	f := &Foreach{Loc: Loc{start.Pos}}
	f.Var, _ = p.name("variable")
	p.expectKw(KwIn)
	f.In = p.parseExpr()
	p.expect(PIPE)
	p.enter()
	for {
		pos := p.cur().Pos
		c := p.parseClauseOpt()
		if c == nil {
			break
		}
		if !isUpdating(c) {
			p.fail(pos, "%s is not allowed inside FOREACH (only updating clauses are)", clauseName(c))
		}
		f.Body = append(f.Body, c)
	}
	p.leave()
	if len(f.Body) == 0 {
		p.unexpected("updating clause")
	}
	p.expect(RPAREN)
	return f
}

// ─── CALL ────────────────────────────────────────────────────────────────────

// parseCall parses a procedure call. CALL subqueries are added in a later
// iteration; `CALL {` and `CALL (` are reported as unsupported for now.
func (p *parser) parseCall() Clause {
	start := p.cur().Pos
	c := &Call{Loc: Loc{start}}
	c.Optional = p.acceptKw(KwOptional)
	p.expectKw(KwCall)
	if p.at(LBRACE) || p.at(LPAREN) {
		p.fail(p.cur().Pos, "CALL subqueries are not supported yet")
	}
	for {
		name, _ := p.name("procedure name")
		c.Name = append(c.Name, name)
		if !p.accept(DOT) {
			break
		}
	}
	if p.accept(LPAREN) {
		if !p.at(RPAREN) {
			for {
				c.Args = append(c.Args, p.parseExpr())
				if !p.accept(COMMA) {
					break
				}
			}
		}
		p.expect(RPAREN)
	} else {
		c.ArgsOmitted = true
	}
	if p.atKw(KwYield) {
		c.Yield = p.parseYield()
	}
	return c
}

func (p *parser) parseYield() *Yield {
	start := p.expectKw(KwYield)
	y := &Yield{Loc: Loc{start.Pos}}
	if p.accept(STAR) {
		y.Star = true
	} else {
		for {
			name, pos := p.name("result field name")
			item := YieldItem{Loc: Loc{pos}, Name: name}
			if p.acceptKw(KwAs) {
				item.Alias, _ = p.name("alias")
			}
			y.Items = append(y.Items, item)
			if !p.accept(COMMA) {
				break
			}
		}
	}
	if p.acceptKw(KwWhere) {
		y.Where = p.parseExpr()
	}
	return y
}

// ─── classification helpers ─────────────────────────────────────────────────

func isUpdating(c Clause) bool {
	switch c.(type) {
	case *Create, *Insert, *Merge, *Set, *Remove, *Delete, *Foreach:
		return true
	}
	return false
}

func isReading(c Clause) bool {
	switch c.(type) {
	case *Match, *Unwind, *LoadCSV:
		return true
	}
	return false
}

// clauseName returns the keyword(s) a clause is written with, for messages.
func clauseName(c Clause) string {
	switch c := c.(type) {
	case *Match:
		if c.Optional {
			return "OPTIONAL MATCH"
		}
		return "MATCH"
	case *Unwind:
		return "UNWIND"
	case *Create:
		return "CREATE"
	case *Insert:
		return "INSERT"
	case *Merge:
		return "MERGE"
	case *Set:
		return "SET"
	case *Remove:
		return "REMOVE"
	case *Delete:
		if c.Detach {
			return "DETACH DELETE"
		}
		return "DELETE"
	case *Foreach:
		return "FOREACH"
	case *Call:
		return "CALL"
	case *CallSubquery:
		return "CALL {}"
	case *LoadCSV:
		return "LOAD CSV"
	case *With:
		return "WITH"
	case *Return:
		return "RETURN"
	case *OrderSkipLimit:
		return "ORDER BY/SKIP/LIMIT"
	case *Filter:
		return "FILTER"
	case *Let:
		return "LET"
	case *Finish:
		return "FINISH"
	case *Conditional:
		return "WHEN"
	}
	return "clause"
}
