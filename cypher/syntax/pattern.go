package syntax

// Pattern grammar:
//
//	part   := [var '='] [shortestPath|allShortestPaths '('] node (rel node)* [')']
//	node   := '(' [var] [':' labels] [props] [WHERE expr] ')'
//	rel    := ['<'] '-' ['[' [var] [':' types] ['*' range] [props] [WHERE expr] ']'] '-' ['>']
//	range  := [INT] ['..' [INT]]
//	props  := map-literal | $param
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
	part.Elems = p.parseChainFrom(p.parseNodePattern())
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
	part.Elems = p.parseChainFrom(first)
	if part.Func != FuncNone {
		p.expect(RPAREN)
	}
	return part
}

// parseChainFrom parses `(rel node)*` after an already-parsed first node.
func (p *parser) parseChainFrom(first *NodePattern) []PatternElem {
	elems := []PatternElem{first}
	for p.atRelStart() {
		elems = append(elems, p.parseRelPattern(), p.parseNodePattern())
	}
	return elems
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
