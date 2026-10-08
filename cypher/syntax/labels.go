package syntax

// parseLabelExpr parses a label/type expression after its leading ':' has been
// consumed:
//
//	or    := and ('|' [':'] and)*        // ':A|:B' is the legacy spelling of 'A|B'
//	and   := not (('&' | ':') not)*      // ':A:B' is the legacy spelling of 'A&B'
//	not   := '!' not | '(' or ')' | '%' | name | '$' '(' expr ')'
//
// Precedence is ! > & > |, as in Neo4j's label expressions.
func (p *parser) parseLabelExpr() LabelExpr {
	p.enter()
	defer p.leave()
	l := p.parseLabelAnd()
	for p.at(PIPE) {
		p.next()
		p.accept(COLON)
		r := p.parseLabelAnd()
		l = &LabelOr{Loc{l.Pos()}, l, r}
	}
	return l
}

func (p *parser) parseLabelAnd() LabelExpr {
	l := p.parseLabelNot()
	for p.at(AMP) || p.at(COLON) {
		p.next()
		r := p.parseLabelNot()
		l = &LabelAnd{Loc{l.Pos()}, l, r}
	}
	return l
}

func (p *parser) parseLabelNot() LabelExpr {
	t := p.cur()
	switch t.Kind {
	case BANG:
		p.next()
		return &LabelNot{Loc{t.Pos}, p.parseLabelNot()}
	case PERCENT:
		p.next()
		return &LabelWildcard{Loc{t.Pos}}
	case LPAREN:
		p.next()
		e := p.parseLabelExpr()
		p.expect(RPAREN)
		return e
	case DOLLAR:
		p.next()
		p.expect(LPAREN)
		x := p.parseExpr()
		p.expect(RPAREN)
		return &LabelName{Loc: Loc{t.Pos}, Dynamic: x}
	case IDENT:
		p.next()
		return &LabelName{Loc: Loc{t.Pos}, Name: t.Value}
	}
	p.unexpected("label name")
	return nil
}
