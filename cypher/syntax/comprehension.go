package syntax

// Comprehensions, quantifier predicates and reduce(). All of them are
// recognised by short, unambiguous token lookahead from parseAtom.

// listCompAhead reports whether the cursor (just after '[') starts `var IN …`.
func (p *parser) listCompAhead() bool {
	return p.at(IDENT) && p.peek(1).Is(KwIn)
}

// parseListComp parses `var IN list [WHERE cond] [| proj]]` after '['.
func (p *parser) parseListComp(open Token) Expr {
	v, _ := p.name("variable")
	p.expectKw(KwIn)
	c := &ListComp{Loc: Loc{open.Pos}, Var: v, In: p.parseExpr()}
	if p.acceptKw(KwWhere) {
		c.Where = p.parseExpr()
	}
	if p.accept(PIPE) {
		c.Proj = p.parseExpr()
	}
	p.expect(RBRACK)
	return c
}

// tryPatternComp parses `[var =] pattern [WHERE cond] | proj]` after '['. It
// returns nil, cursor restored, when the bracket holds an ordinary list.
func (p *parser) tryPatternComp(open Token) Expr {
	if !p.at(LPAREN) && !(p.at(IDENT) && p.peek(1).Kind == EQ) {
		return nil
	}
	start := p.i
	part := p.tryPatternPart(true)
	if part == nil {
		return nil
	}
	if !p.atKw(KwWhere) && !p.at(PIPE) {
		p.i = start // a list whose first element is a pattern predicate
		return nil
	}
	c := &PatternComp{Loc: Loc{open.Pos}, Pattern: part}
	if p.acceptKw(KwWhere) {
		c.Where = p.parseExpr()
	}
	p.expect(PIPE)
	c.Proj = p.parseExpr()
	p.expect(RBRACK)
	return c
}

func quantifierKind(t Token) (QuantifierKind, bool) {
	switch t.Keyword {
	case KwAll:
		return QuantAll, true
	case KwAny:
		return QuantAny, true
	case KwNone:
		return QuantNone, true
	case KwSingle:
		return QuantSingle, true
	}
	return "", false
}

// quantifierAhead reports whether the cursor is at `all(var IN` and siblings.
func (p *parser) quantifierAhead() (QuantifierKind, bool) {
	k, ok := quantifierKind(p.cur())
	if ok && p.peek(1).Kind == LPAREN && p.peek(2).Kind == IDENT && p.peek(3).Is(KwIn) {
		return k, true
	}
	return "", false
}

func (p *parser) parseQuantifier(kind QuantifierKind) Expr {
	start := p.next()
	p.expect(LPAREN)
	v, _ := p.name("variable")
	p.expectKw(KwIn)
	q := &Quantifier{Loc: Loc{start.Pos}, Kind: kind, Var: v, In: p.parseExpr()}
	if p.acceptKw(KwWhere) {
		q.Where = p.parseExpr()
	}
	p.expect(RPAREN)
	return q
}

// reduceAhead reports whether the cursor is at `reduce(var =`.
func (p *parser) reduceAhead() bool {
	return p.cur().isWord("reduce") && p.peek(1).Kind == LPAREN &&
		p.peek(2).Kind == IDENT && p.peek(3).Kind == EQ
}

func (p *parser) parseReduce() Expr {
	start := p.next()
	p.expect(LPAREN)
	acc, _ := p.name("accumulator variable")
	p.expect(EQ)
	r := &Reduce{Loc: Loc{start.Pos}, Acc: acc, Init: p.parseExpr()}
	p.expect(COMMA)
	r.Var, _ = p.name("variable")
	p.expectKw(KwIn)
	r.In = p.parseExpr()
	p.expect(PIPE)
	r.Expr = p.parseExpr()
	p.expect(RPAREN)
	return r
}
