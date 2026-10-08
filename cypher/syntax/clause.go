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
	case KwLoad:
		return p.parseLoadCSV()
	case KwFilter:
		p.next()
		p.acceptKw(KwWhere)
		return &Filter{Loc{t.Pos}, p.parseExpr()}
	case KwLet:
		return p.parseLet()
	case KwFinish:
		p.next()
		return &Finish{Loc{t.Pos}}
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
	m := &Match{Loc: Loc{start}, Optional: optional}
	m.Mode = p.parseMatchModeOpt()
	m.Patterns = p.parsePatternParts()
	for p.atKw(KwUsing) {
		m.Hints = append(m.Hints, p.parseHint())
	}
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
			first := p.i
			item := SortItem{Loc: Loc{pos}, Expr: p.parseExpr()}
			item.Source = p.sourceBetween(first, p.i)
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
		first := p.i
		item.Kind, item.Target, item.Value = SetProperty, target, p.parseExpr()
		item.ValueSource = p.sourceBetween(first, p.i)
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
		first := p.i
		item.Value = p.parseExpr()
		item.ValueSource = p.sourceBetween(first, p.i)
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
		first := p.i
		d.Exprs = append(d.Exprs, p.parseExpr())
		d.Sources = append(d.Sources, p.sourceBetween(first, p.i))
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

// parseCall parses a procedure call, or a CALL subquery when the CALL is
// followed by `{` or a `(` scope clause.
func (p *parser) parseCall() Clause {
	start := p.cur().Pos
	c := &Call{Loc: Loc{start}}
	c.Optional = p.acceptKw(KwOptional)
	p.expectKw(KwCall)
	if p.at(LBRACE) || p.at(LPAREN) {
		return p.parseCallSubquery(start, c.Optional)
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
	case *Create, *Merge, *Set, *Remove, *Delete, *Foreach:
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

// parseMatchModeOpt parses the optional DIFFERENT RELATIONSHIPS /
// REPEATABLE ELEMENTS keywords after MATCH.
func (p *parser) parseMatchModeOpt() MatchMode {
	t, n := p.cur(), p.peek(1)
	if t.Kind != IDENT || n.Kind != IDENT {
		return MatchModeDefault
	}
	switch {
	case t.isWord("different") && (n.isWord("relationships") || n.isWord("relationship") || n.isWord("edges") || n.isWord("edge")):
		p.next()
		p.next()
		return MatchDifferentRelationships
	case t.isWord("repeatable") && (n.isWord("elements") || n.isWord("element") || n.isWord("bindings") || n.isWord("binding")):
		p.next()
		p.next()
		return MatchRepeatableElements
	}
	return MatchModeDefault
}

// ClauseName returns the keyword(s) a clause is written with, for messages.
func ClauseName(c Clause) string { return clauseName(c) }

// parseLoadCSV parses `LOAD CSV [WITH HEADERS] FROM expr AS var [FIELDTERMINATOR str]`.
func (p *parser) parseLoadCSV() Clause {
	start := p.expectKw(KwLoad)
	p.expectKw(KwCSV)
	l := &LoadCSV{Loc: Loc{start.Pos}}
	if p.acceptKw(KwWith) {
		p.expectKw(KwHeaders)
		l.WithHeaders = true
	}
	p.expectKw(KwFrom)
	l.From = p.parseExpr()
	p.expectKw(KwAs)
	l.Var, _ = p.name("variable")
	if p.acceptKw(KwFieldTerm) {
		l.FieldTerminator = p.parseExpr()
	}
	return l
}

// parseLet parses `LET var = expr [, var = expr]*`.
func (p *parser) parseLet() Clause {
	start := p.expectKw(KwLet)
	l := &Let{Loc: Loc{start.Pos}}
	for {
		name, pos := p.name("variable")
		p.expect(EQ)
		l.Items = append(l.Items, LetItem{Loc{pos}, name, p.parseExpr()})
		if !p.accept(COMMA) {
			return l
		}
	}
}

// atClauseStart reports whether the cursor is at a keyword that begins a
// clause, which ends a planner hint.
func (p *parser) atClauseStart() bool {
	t := p.cur()
	if t.Kind != IDENT {
		return false
	}
	switch t.Keyword {
	case KwMatch, KwOptional, KwUnwind, KwWith, KwReturn, KwCreate, KwMerge, KwSet, KwRemove,
		KwDelete, KwDetach, KwForeach, KwCall, KwLoad, KwUnion, KwFinish, KwFilter, KwLet:
		return true
	}
	return false
}

// parseHint skips a planner hint (`USING INDEX n:L(p)`, `USING JOIN ON n`,
// `USING SCAN n:L`, …). Hints do not change results, so the arguments are kept
// only as text.
func (p *parser) parseHint() Hint {
	start := p.expectKw(KwUsing)
	h := Hint{Loc: Loc{start.Pos}}
	first := p.i
	kind, _ := p.name("hint kind")
	h.Kind = upper(kind)
	if p.at(IDENT) && p.cur().Is(KwIndex) {
		h.Kind += " INDEX"
	}
	// Skip the arguments, which may contain balanced parentheses; an
	// unbalanced ')' belongs to an enclosing construct such as `EXISTS { … }`.
	depth := 0
	for !p.at(EOF) && !p.at(SEMI) && !p.at(RBRACE) && !p.atKw(KwWhere) && !p.atKw(KwUsing) && !p.atClauseStart() {
		switch p.cur().Kind {
		case LPAREN:
			depth++
		case RPAREN:
			if depth == 0 {
				goto done
			}
			depth--
		}
		p.next()
	}
done:
	h.Text = p.sourceBetween(first, p.i)
	return h
}
