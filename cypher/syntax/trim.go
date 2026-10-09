package syntax

// parseTrimForm parses the SQL-style trim(BOTH|LEADING|TRAILING [chars] FROM s)
// and trim(chars FROM s). The opening parenthesis has been consumed. It returns
// nil, consuming nothing, for the plain trim(s) form.
func (p *parser) parseTrimForm(f *FuncCall) Expr {
	mode := "btrim"
	hasSpec := false
	if t := p.cur(); t.Kind == IDENT && (t.isWord("both") || t.isWord("leading") || t.isWord("trailing")) {
		if n := p.peek(1); n.Kind != RPAREN && n.Kind != COMMA && n.Kind != DOT {
			switch {
			case t.isWord("leading"):
				mode = "ltrim"
			case t.isWord("trailing"):
				mode = "rtrim"
			}
			p.next()
			hasSpec = true
		}
	}
	var chars Expr
	if !p.atKw(KwFrom) {
		first := p.parseExpr()
		if !p.atKw(KwFrom) {
			if hasSpec {
				p.unexpected("FROM")
			}
			// Plain trim(expr[, …]): hand back to the ordinary call parser.
			f.Args = append(f.Args, first)
			for p.accept(COMMA) {
				f.Args = append(f.Args, p.parseExpr())
			}
			p.expect(RPAREN)
			return f
		}
		chars = first
	}
	p.expectKw(KwFrom)
	source := p.parseExpr()
	p.expect(RPAREN)
	call := &FuncCall{Loc: f.Loc, Name: mode, Args: []Expr{source}}
	if chars != nil {
		call.Args = append(call.Args, chars)
	}
	return call
}
