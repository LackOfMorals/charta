package syntax

// Subqueries.
//
//	EXISTS { pattern [WHERE expr] }  |  EXISTS { query }        (RETURN optional)
//	COUNT  { pattern [WHERE expr] }  |  COUNT  { query }        (RETURN optional)
//	COLLECT { query }                                           (RETURN required)
//	[OPTIONAL] CALL [(a, b) | (*) | ()] { query } [IN TRANSACTIONS …]
//	body := query | WHEN expr THEN { body } (WHEN expr THEN { body })* [ELSE { body }]
//
// The old `CALL { WITH a … }` importing form needs no special handling: the
// leading WITH is an ordinary clause of the body.

// subqueryExprAhead reports whether the cursor is at EXISTS/COUNT/COLLECT
// followed by '{'.
func (p *parser) subqueryExprAhead() bool {
	t := p.cur()
	if t.Kind != IDENT || p.peek(1).Kind != LBRACE {
		return false
	}
	switch t.Keyword {
	case KwExists, KwCount, KwCollect:
		return true
	}
	return false
}

var subqueryKinds = map[Keyword]SubqueryKind{
	KwExists: SubqueryExists, KwCount: SubqueryCount, KwCollect: SubqueryCollect,
}

// patternStartAhead reports whether the cursor starts a pattern (the pattern
// shorthand of EXISTS/COUNT) rather than a clause.
func (p *parser) patternStartAhead() bool {
	t := p.cur()
	switch {
	case t.Kind == LPAREN:
		return true
	case t.Kind != IDENT:
		return false
	case p.peek(1).Kind == EQ: // p = (a)-->(b)
		return true
	case t.Is(KwAny), t.Is(KwAll), t.isWord("shortest"):
		return true
	}
	return p.shortestFuncAhead() != FuncNone
}

func (p *parser) parseSubqueryExpr() Expr {
	t := p.next()
	kind := subqueryKinds[t.Keyword]
	p.expect(LBRACE)
	p.enter()
	defer p.leave()
	s := &SubqueryExpr{Loc: Loc{t.Pos}, Kind: kind}
	switch {
	case kind != SubqueryCollect && p.patternStartAhead():
		s.Patterns = p.parsePatternParts()
		if p.acceptKw(KwWhere) {
			s.Where = p.parseExpr()
		}
	case kind == SubqueryCollect:
		s.Query = p.parseSubqueryBody(ctxCollect)
	default:
		s.Query = p.parseSubqueryBody(ctxExists)
	}
	p.expect(RBRACE)
	return s
}

// parseSubqueryBody parses the inside of a subquery's braces: a query, or a
// conditional WHEN … THEN … ELSE … body.
func (p *parser) parseSubqueryBody(ctx queryCtx) Body {
	if p.atKw(KwWhen) {
		return p.parseConditional(ctx)
	}
	return p.parseBody(ctx)
}

// parseConditional parses `WHEN cond THEN { body } … [ELSE { body }]`.
func (p *parser) parseConditional(ctx queryCtx) Body {
	c := &Conditional{Loc: Loc{p.cur().Pos}}
	for p.atKw(KwWhen) {
		w := p.next()
		cond := p.parseExpr()
		p.expectKw(KwThen)
		p.expect(LBRACE)
		body := p.parseBody(ctx)
		p.expect(RBRACE)
		c.Branches = append(c.Branches, WhenBranch{Loc{w.Pos}, cond, body})
	}
	if p.acceptKw(KwElse) {
		p.expect(LBRACE)
		c.Else = p.parseBody(ctx)
		p.expect(RBRACE)
	}
	return c
}

// parseCallSubquery parses the rest of `[OPTIONAL] CALL` once the cursor is at
// '{' or the '(' of a scope clause.
func (p *parser) parseCallSubquery(start Pos, optional bool) Clause {
	c := &CallSubquery{Loc: Loc{start}, Optional: optional}
	if p.accept(LPAREN) {
		c.Scoped = true
		switch {
		case p.accept(STAR):
			c.ImportAll = true
		case !p.at(RPAREN):
			for {
				name, _ := p.name("variable")
				c.Imports = append(c.Imports, name)
				if !p.accept(COMMA) {
					break
				}
			}
		}
		p.expect(RPAREN)
	}
	p.expect(LBRACE)
	p.enter()
	c.Body = p.parseSubqueryBody(ctxTop)
	p.leave()
	p.expect(RBRACE)
	if p.atKw(KwIn) {
		c.InTx = p.parseInTransactions()
	}
	return c
}

// parseInTransactions parses
//
//	IN [[n] CONCURRENT] TRANSACTIONS [OF n ROW[S]] [DISJOINT BY {NONE|AUTO|(expr,…)}]
//	   [REPORT STATUS AS var] [ON ERROR {CONTINUE|BREAK|FAIL|RETRY [FOR n SECONDS] [THEN {CONTINUE|BREAK|FAIL}]}]
func (p *parser) parseInTransactions() *InTransactions {
	in := &InTransactions{Loc: Loc{p.expectKw(KwIn).Pos}}
	switch {
	case p.cur().isWord("concurrent"):
		p.next()
		in.Concurrent = true
	case !p.atKw(KwTransactions):
		in.Concurrency = p.parsePostfix()
		if !p.cur().isWord("concurrent") {
			p.unexpected("CONCURRENT")
		}
		p.next()
		in.Concurrent = true
	}
	p.expectKw(KwTransactions)
	for {
		switch t := p.cur(); {
		case t.Is(KwOf):
			p.next()
			in.BatchSize = p.parsePostfix()
			if !p.acceptKw(KwRows) && !p.cur().isWord("row") {
				p.unexpected("ROWS")
			}
			if p.cur().isWord("row") {
				p.next()
			}
		case t.isWord("disjoint"):
			p.next()
			p.expectKw(KwBy)
			switch {
			case p.acceptKw(KwNone):
				in.Disjoint = "NONE"
			case p.cur().isWord("auto"):
				p.next()
				in.Disjoint = "AUTO"
			default:
				p.expect(LPAREN)
				for {
					p.parseExpr()
					if !p.accept(COMMA) {
						break
					}
				}
				p.expect(RPAREN)
			}
		case t.isWord("report"):
			p.next()
			if !p.cur().isWord("status") {
				p.unexpected("STATUS")
			}
			p.next()
			p.expectKw(KwAs)
			in.ReportAs, _ = p.name("variable")
		case t.Is(KwOn):
			p.next()
			p.expectKw(KwError)
			in.OnError = p.errorAction(true, in)
		default:
			return in
		}
	}
}

// errorAction parses CONTINUE/BREAK/FAIL, or (when allowRetry) RETRY with its
// options.
func (p *parser) errorAction(allowRetry bool, in *InTransactions) string {
	t := p.cur()
	switch {
	case t.isWord("continue"), t.isWord("break"), t.Is(KwFail):
		p.next()
		return upper(t.Text)
	case allowRetry && t.isWord("retry"):
		p.next()
		if p.acceptKw(KwFor) || p.at(INT) || p.at(PARAM) {
			in.RetryFor = p.parsePostfix()
			if w := p.cur(); w.isWord("sec") || w.isWord("second") || w.isWord("seconds") {
				p.next()
			}
		}
		if p.acceptKw(KwThen) {
			in.RetryThen = p.errorAction(false, in)
		}
		return "RETRY"
	}
	p.unexpected("CONTINUE", "BREAK", "FAIL", "RETRY")
	return ""
}

func upper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'a' <= c && c <= 'z' {
			b[i] = c - ('a' - 'A')
		}
	}
	return string(b)
}
