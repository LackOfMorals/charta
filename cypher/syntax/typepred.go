package syntax

// Map projections and value types.

// parseMapProjection parses `subject{.key, key: expr, var, .*}`.
func (p *parser) parseMapProjection(subject *Ident) Expr {
	p.expect(LBRACE)
	m := &MapProjection{Loc: Loc{subject.Pos()}, Subject: subject}
	if !p.at(RBRACE) {
		for {
			t := p.cur()
			item := MapProjItem{Loc: Loc{t.Pos}}
			switch {
			case t.Kind == DOT && p.peek(1).Kind == STAR:
				p.next()
				p.next()
				item.Kind = ProjAll
			case t.Kind == DOT:
				p.next()
				item.Kind = ProjProperty
				item.Key, _ = p.name("property key name")
			case t.Kind == IDENT && p.peek(1).Kind == COLON:
				p.next()
				p.next()
				item.Kind, item.Key, item.Value = ProjLiteral, t.Value, p.parseExpr()
			case t.Kind == IDENT:
				p.next()
				item.Kind, item.Key = ProjVariable, t.Value
			default:
				p.unexpected("map projection element")
			}
			m.Items = append(m.Items, item)
			if !p.accept(COMMA) {
				break
			}
		}
	}
	p.expect(RBRACE)
	return m
}

func isNormalForm(t Token) bool {
	return t.isWord("nfc") || t.isWord("nfd") || t.isWord("nfkc") || t.isWord("nfkd")
}

// typeWords are the words that can make up a value-type name. A closed list
// keeps `x IS :: INTEGER AND y` and `[… WHERE x :: INTEGER | x]` unambiguous.
var typeWords = map[string]bool{
	"BOOLEAN": true, "BOOL": true, "STRING": true, "VARCHAR": true,
	"INTEGER": true, "INT": true, "SIGNED": true, "INT8": true, "INT16": true, "INT32": true, "INT64": true,
	"INTEGER8": true, "INTEGER16": true, "INTEGER32": true, "INTEGER64": true,
	"FLOAT": true, "FLOAT32": true, "FLOAT64": true, "REAL": true, "DOUBLE": true, "PRECISION": true,
	"DATE": true, "LOCAL": true, "ZONED": true, "TIME": true, "DATETIME": true, "TIMESTAMP": true,
	"DURATION": true, "POINT": true, "NODE": true, "VERTEX": true, "RELATIONSHIP": true, "EDGE": true,
	"MAP": true, "PATH": true, "LIST": true, "ARRAY": true, "ANY": true, "VALUE": true, "PROPERTY": true,
	"NOTHING": true, "NULL": true, "VECTOR": true,
}

func (p *parser) atTypeWord() bool {
	t := p.cur()
	return t.Kind == IDENT && t.Text == t.Value && typeWords[upper(t.Text)]
}

// parseTypeText parses a value type, e.g. `INTEGER`, `LIST<STRING NOT NULL>`,
// `ZONED DATETIME`, `VECTOR<FLOAT32>(4)` or `INTEGER | FLOAT`, and returns it
// normalised to upper case with single spaces.
func (p *parser) parseTypeText() string {
	p.enter()
	defer p.leave()
	s := p.parseTypeAtom()
	for p.at(PIPE) && p.peek(1).Kind == IDENT && typeWords[upper(p.peek(1).Text)] {
		p.next()
		s += " | " + p.parseTypeAtom()
	}
	return s
}

func (p *parser) parseTypeAtom() string {
	if !p.atTypeWord() {
		p.unexpected("type name")
	}
	var s string
	for p.atTypeWord() {
		w := upper(p.next().Text)
		if s != "" {
			s += " "
		}
		s += w
		// TIME/TIMESTAMP WITH[OUT] TIME ZONE / TIMEZONE
		if (w == "TIME" || w == "TIMESTAMP") && (p.atKw(KwWith) || p.cur().isWord("without")) {
			if n := p.peek(1); n.isWord("time") || n.isWord("timezone") {
				s += " " + upper(p.next().Text)
				s += " " + upper(p.next().Text)
				if p.cur().isWord("zone") {
					s += " " + upper(p.next().Text)
				}
			}
		}
	}
	if p.at(LT) {
		p.next()
		s += "<" + p.parseTypeText() + ">"
		p.expect(GT)
	}
	if p.at(LPAREN) && p.peek(1).Kind == INT && p.peek(2).Kind == RPAREN {
		p.next()
		n := p.next()
		p.next()
		s += "(" + n.Text + ")"
	}
	if p.atKw(KwNot) && p.peek(1).Is(KwNull) {
		p.next()
		p.next()
		s += " NOT NULL"
	}
	return s
}
