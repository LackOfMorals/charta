package syntax

// Pattern grammar:
//
//	part     := [var '='] [selector] [shortestPath|allShortestPaths '('] element+ [')']
//	element  := node | rel | group        (no two node patterns may be adjacent)
//	node     := '(' [var] [':' labels] [props] [WHERE expr] ')'
//	rel      := ['<'] '-' ['[' [var] [':' types] ['*' range] [props] [WHERE expr] ']'] '-' ['>'] [quant]
//	group    := '(' [var '='] element+ [WHERE expr] ')' [quant]
//	quant    := '+' | '*' | '{' n '}' | '{' [n] ',' [m] '}'
//	selector := ANY [SHORTEST | k] | ALL [SHORTEST] | SHORTEST [k] [GROUP|GROUPS]
//	range    := [INT] ['..' [INT]]
//	props    := map-literal | $param
//
// Arrows lex as separate tokens ('<', '-', '>'), so "-->" is MINUS MINUS GT.

// try runs fn speculatively. If fn raises a syntax error, try restores the
// cursor and reports false; any other panic propagates.
func (p *parser) try(fn func()) (ok bool) {
	i, d := p.i, p.depth
	defer func() {
		if r := recover(); r != nil {
			if _, isBailout := r.(bailout); !isBailout {
				panic(r)
			}
			p.i, p.depth = i, d
			ok = false
		}
	}()
	fn()
	return true
}

// isWord reports whether t is the unquoted identifier w (case-insensitively).
func (t Token) isWord(w string) bool {
	return t.Kind == IDENT && t.Text == t.Value && equalFold(t.Text, w)
}

// atVariable reports whether the cursor is at a variable name inside a node or
// relationship pattern. WHERE is not accepted as a bare variable so that
// `(WHERE …)` / `[WHERE …]` read as a predicate.
func (p *parser) atVariable() bool {
	return p.at(IDENT) && !p.atKw(KwWhere)
}

// atRelStart reports whether the cursor is at the start of a relationship
// pattern: `-[`, `--`, `<-[` or `<--`. Requiring the second token keeps
// arithmetic such as `(a) - 1` and comparisons such as `(a) < -1` unambiguous.
func (p *parser) atRelStart() bool {
	i := 0
	switch p.cur().Kind {
	case LT:
		if p.peek(1).Kind != MINUS {
			return false
		}
		i = 2
	case MINUS:
		i = 1
	default:
		return false
	}
	k := p.peek(i).Kind
	return k == LBRACK || k == MINUS
}

func (p *parser) shortestFuncAhead() ShortestFunc {
	if p.peek(1).Kind != LPAREN {
		return FuncNone
	}
	switch t := p.cur(); {
	case t.isWord("shortestPath"):
		return FuncShortestPath
	case t.isWord("allShortestPaths"):
		return FuncAllShortestPaths
	}
	return FuncNone
}

// patternPrefix consumes an optional `var =` and an optional shortestPath( /
// allShortestPaths( wrapper opener.
func (p *parser) patternPrefix(part *PatternPart, allowVar bool) {
	if allowVar && p.at(IDENT) && p.peek(1).Kind == EQ {
		part.Var, _ = p.name("path variable")
		p.next() // '='
	}
	part.Selector = p.parseSelectorOpt()
	if fn := p.shortestFuncAhead(); fn != FuncNone {
		p.next()
		p.next()
		part.Func = fn
	}
}

// parsePatternPart parses one pattern part, committing to it: errors are
// reported, never retried.
func (p *parser) parsePatternPart() *PatternPart {
	part := &PatternPart{Loc: Loc{p.cur().Pos}}
	p.patternPrefix(part, true)
	part.Elems = p.continueElems([]PatternElem{p.parsePathElement()})
	if part.Func != FuncNone {
		p.expect(RPAREN)
	}
	return part
}

// tryPatternPart speculatively parses a pattern part that must contain at least
// one relationship, as in a pattern predicate or pattern comprehension. It
// returns nil, with the cursor restored, if the input does not start with a
// node pattern followed by a relationship arrow. Once a node and an arrow have
// been seen the parse is committed and later errors propagate.
func (p *parser) tryPatternPart(allowVar bool) *PatternPart {
	start, depth := p.i, p.depth
	part := &PatternPart{Loc: Loc{p.cur().Pos}}
	p.patternPrefix(part, allowVar)
	var first *NodePattern
	if !p.try(func() { first = p.parseNodePattern() }) || !p.atRelStart() {
		p.i, p.depth = start, depth
		return nil
	}
	part.Elems = p.continueElems([]PatternElem{first})
	if part.Func != FuncNone {
		p.expect(RPAREN)
	}
	return part
}

// continueElems extends a path pattern whose first element is already parsed
// with further relationships, nodes and parenthesised groups.
func (p *parser) continueElems(elems []PatternElem) []PatternElem {
	for {
		switch {
		case p.atRelStart():
			elems = append(elems, p.parseRelPattern(), p.parsePathElement())
		case p.at(LPAREN):
			el := p.parsePathElement()
			if _, isNode := el.(*NodePattern); isNode {
				if _, prevNode := elems[len(elems)-1].(*NodePattern); prevNode {
					p.fail(el.Pos(), "two node patterns must be separated by a relationship (or by a quantified path pattern)")
				}
			}
			elems = append(elems, el)
		default:
			return elems
		}
	}
}

// parsePathElement parses a node pattern or a parenthesised path pattern.
func (p *parser) parsePathElement() PatternElem {
	if p.groupAhead() {
		return p.parseGroupPattern()
	}
	return p.parseNodePattern()
}

// groupAhead reports whether the '(' at the cursor opens a parenthesised path
// pattern rather than a node pattern: `((…` or `(var = …`.
func (p *parser) groupAhead() bool {
	if !p.at(LPAREN) {
		return false
	}
	return p.peek(1).Kind == LPAREN || (p.peek(1).Kind == IDENT && p.peek(2).Kind == EQ)
}

func (p *parser) parseGroupPattern() PatternElem {
	open := p.expect(LPAREN)
	p.enter()
	defer p.leave()
	g := &GroupPattern{Loc: Loc{open.Pos}}
	if p.at(IDENT) && p.peek(1).Kind == EQ {
		g.Var, _ = p.name("path variable")
		p.next() // '='
	}
	g.Elems = p.continueElems([]PatternElem{p.parsePathElement()})
	if p.acceptKw(KwWhere) {
		g.Where = p.parseExpr()
	}
	p.expect(RPAREN)
	g.Quant = p.parseQuantOpt()
	return g
}

// parseQuantOpt parses an optional path quantifier.
func (p *parser) parseQuantOpt() *PathQuant {
	t := p.cur()
	switch t.Kind {
	case PLUS:
		p.next()
		return &PathQuant{Loc: Loc{t.Pos}, Min: 1}
	case STAR:
		p.next()
		return &PathQuant{Loc: Loc{t.Pos}}
	case LBRACE:
		p.next()
		q := &PathQuant{Loc: Loc{t.Pos}}
		hasMin := p.at(INT)
		if hasMin {
			q.Min = p.rangeBound()
		}
		if p.accept(COMMA) {
			if p.at(INT) {
				m := p.rangeBound()
				q.Max = &m
			} else if !hasMin {
				p.unexpected("quantifier bound")
			}
		} else if hasMin {
			m := q.Min
			q.Max = &m
		} else {
			p.unexpected("quantifier bound")
		}
		p.expect(RBRACE)
		return q
	}
	return nil
}

// parseSelectorOpt parses an optional Neo4j path selector prefix.
func (p *parser) parseSelectorOpt() *PathSelector {
	t := p.cur()
	if t.Kind != IDENT || p.peek(1).Kind == EQ {
		return nil
	}
	sel := &PathSelector{Loc: Loc{t.Pos}}
	k := func() {
		if p.at(INT) || p.at(PARAM) {
			sel.K = p.parseAtom()
		}
	}
	switch {
	case t.Is(KwAny):
		p.next()
		if p.acceptKw(KwShortest) {
			sel.Kind = SelectorAnyShortest
		} else {
			sel.Kind = SelectorAny
			k()
		}
	case t.Is(KwAll):
		p.next()
		if p.acceptKw(KwShortest) {
			sel.Kind = SelectorAllShortest
		} else {
			sel.Kind = SelectorAll
		}
	case t.Is(KwShortest):
		p.next()
		sel.Kind = SelectorShortest
		k()
		if p.acceptKw(KwGroups) || p.cur().isWord("group") {
			if p.cur().isWord("group") {
				p.next()
			}
			sel.Kind = SelectorShortestGroups
		}
	default:
		return nil
	}
	return sel
}

func (p *parser) parseNodePattern() *NodePattern {
	open := p.expect(LPAREN)
	n := &NodePattern{Loc: Loc{open.Pos}}
	if p.atVariable() {
		n.Var, _ = p.name("variable")
	}
	if p.accept(COLON) {
		n.Labels = p.parseLabelExpr()
	}
	n.Props = p.parseProps()
	if p.acceptKw(KwWhere) {
		n.Where = p.parseExpr()
	}
	p.expect(RPAREN)
	return n
}

// parseProps parses an optional `{…}` map literal or `$param`.
func (p *parser) parseProps() Expr {
	switch p.cur().Kind {
	case LBRACE:
		return p.parseMapLiteral()
	case PARAM:
		t := p.next()
		return &Param{Loc{t.Pos}, t.Value}
	}
	return nil
}

func (p *parser) parseRelPattern() *RelPattern {
	start := p.cur().Pos
	left := p.accept(LT)
	p.expect(MINUS)
	r := &RelPattern{Loc: Loc{start}}
	if p.at(LBRACK) {
		p.parseRelDetail(r)
	}
	p.expect(MINUS)
	right := p.accept(GT)
	if q := p.parseQuantOpt(); q != nil {
		if r.Range != nil {
			p.fail(q.Pos(), "a relationship cannot have both a *range and a quantifier")
		}
		r.Quant = q
	}
	switch {
	case left && right:
		r.Dir = DirBoth
	case left:
		r.Dir = DirLeft
	case right:
		r.Dir = DirRight
	}
	return r
}

func (p *parser) parseRelDetail(r *RelPattern) {
	p.expect(LBRACK)
	if p.atVariable() {
		r.Var, _ = p.name("variable")
	}
	if p.accept(COLON) {
		r.Types = p.parseLabelExpr()
	}
	if p.at(STAR) {
		r.Range = p.parseRange()
	}
	r.Props = p.parseProps()
	if p.acceptKw(KwWhere) {
		r.Where = p.parseExpr()
	}
	p.expect(RBRACK)
}

// parseRange parses `*`, `*n`, `*n..`, `*..m` or `*n..m`.
func (p *parser) parseRange() *Range {
	star := p.expect(STAR)
	rng := &Range{Loc: Loc{star.Pos}}
	if p.at(INT) {
		v := p.rangeBound()
		rng.Min = &v
	}
	if p.accept(DOTDOT) {
		if p.at(INT) {
			v := p.rangeBound()
			rng.Max = &v
		}
	} else if rng.Min != nil {
		v := *rng.Min
		rng.Max = &v
	}
	return rng
}

func (p *parser) rangeBound() int64 {
	t := p.expect(INT)
	v, err := parseIntLiteral(t.Text, false)
	if err != nil {
		p.fail(t.Pos, "%s", err.Error())
	}
	return v
}
