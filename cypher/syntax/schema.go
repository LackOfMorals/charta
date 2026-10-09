package syntax

// Statement prefixes, schema commands and server-only commands.
//
//	statement   := prefix* (schema | server | query)
//	prefix      := EXPLAIN | PROFILE | CYPHER [25] (key '=' value)*
//	CREATE [RANGE|TEXT|POINT|LOOKUP|FULLTEXT|VECTOR] INDEX [name] [IF NOT EXISTS]
//	    FOR target ON ['EACH'] (props | [props] | labels(n) | type(r)) [OPTIONS map]
//	CREATE CONSTRAINT [name] [IF NOT EXISTS] FOR target REQUIRE props
//	    IS (UNIQUE | NOT NULL | NODE KEY | RELATIONSHIP KEY | :: type) [OPTIONS map]
//	DROP (INDEX | CONSTRAINT) name [IF EXISTS]
//	SHOW words… [YIELD …] [WHERE expr]

// parsePrefixes consumes EXPLAIN/PROFILE and the CYPHER version/options prefix,
// in any order.
func (p *parser) parsePrefixes(st *Statement) {
	for {
		t := p.cur()
		switch {
		case t.Is(KwExplain), t.Is(KwProfile):
			if st.Mode != ModeNone {
				p.fail(t.Pos, "only one of EXPLAIN and PROFILE may be given")
			}
			p.next()
			st.Mode = ModeExplain
			if t.Is(KwProfile) {
				st.Mode = ModeProfile
			}
		case t.Is(KwCypher):
			if st.Version != "" || len(st.Options) > 0 {
				p.fail(t.Pos, "duplicate CYPHER prefix")
			}
			p.next()
			p.parseCypherPrefix(st)
		default:
			return
		}
	}
}

// parseCypherPrefix parses what follows CYPHER: an optional language version
// and `key=value` options. Only Cypher 25 is supported.
func (p *parser) parseCypherPrefix(st *Statement) {
	if v := p.cur(); v.Kind == INT || v.Kind == FLOAT {
		p.next()
		if v.Text != "25" {
			p.fail(v.Pos, "graphlite implements Cypher 25; CYPHER %s is not supported", v.Text)
		}
		st.Version = v.Text
	}
	for p.at(IDENT) && p.peek(1).Kind == EQ {
		key := p.next()
		p.next() // '='
		val := p.cur()
		switch val.Kind {
		case IDENT, INT, FLOAT, STRING:
			p.next()
		default:
			p.unexpected("option value")
		}
		st.Options = append(st.Options, QueryOption{Loc{key.Pos}, key.Value, val.Value})
	}
	if st.Version == "" && len(st.Options) == 0 {
		p.unexpected("version", "option")
	}
}

// ─── server-only commands ────────────────────────────────────────────────────

var serverVerbs = map[string]bool{
	"use": true, "grant": true, "deny": true, "revoke": true, "alter": true, "rename": true,
	"start": true, "stop": true, "enable": true, "terminate": true, "deallocate": true,
}

var serverNouns = map[string]bool{
	"user": true, "role": true, "database": true, "alias": true, "composite": true,
	"server": true, "or": true,
}

// serverCommandAhead reports whether the statement is a Neo4j server command
// that has no meaning for an embedded store.
func (p *parser) serverCommandAhead() bool {
	t := p.cur()
	if t.Kind != IDENT || t.Text != t.Value {
		return false
	}
	w := lower(t.Text)
	if serverVerbs[w] && p.peek(1).Kind != EQ {
		return true
	}
	if t.Is(KwCreate) || t.Is(KwDrop) {
		n := p.peek(1)
		return n.Kind == IDENT && serverNouns[lower(n.Text)]
	}
	return false
}

// parseServerCommand consumes a server-only command. Its arguments are not
// interpreted, so the rest of the input is skipped.
func (p *parser) parseServerCommand() Body {
	start := p.cur()
	cmd := upper(start.Text)
	p.next()
	if start.Is(KwCreate) || start.Is(KwDrop) {
		cmd += " " + upper(p.next().Text)
	}
	for !p.at(EOF) && !p.at(SEMI) {
		p.next()
	}
	return &ServerCommand{Loc{start.Pos}, cmd}
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// ─── schema commands ─────────────────────────────────────────────────────────

var indexKinds = map[string]bool{
	"range": true, "text": true, "point": true, "lookup": true, "fulltext": true, "vector": true,
}

// schemaCommandAhead reports whether the statement is CREATE/DROP INDEX or
// CONSTRAINT, or SHOW.
func (p *parser) schemaCommandAhead() bool {
	t := p.cur()
	switch {
	case t.Is(KwCreate):
		i := 1
		if n := p.peek(1); n.Kind == IDENT && indexKinds[lower(n.Text)] {
			i = 2
		}
		n := p.peek(i)
		return n.Is(KwIndex) || (i == 1 && n.Is(KwConstraint))
	case t.Is(KwDrop):
		n := p.peek(1)
		return n.Is(KwIndex) || n.Is(KwConstraint)
	case t.Is(KwShow):
		return true
	}
	return false
}

func (p *parser) parseSchemaCommand() Body {
	t := p.cur()
	switch {
	case t.Is(KwShow):
		return p.parseShow()
	case t.Is(KwDrop):
		return p.parseDropSchema()
	}
	if n := p.peek(1); n.Is(KwConstraint) {
		return p.parseCreateConstraint()
	}
	return p.parseCreateIndex()
}

// parseIfNotExists parses an optional IF NOT EXISTS (or IF EXISTS).
func (p *parser) parseIfClause(not bool) bool {
	if !p.atKw(KwIf) {
		return false
	}
	p.next()
	if not {
		p.expectKw(KwNot)
	}
	p.expectKw(KwExists)
	return true
}

// parseSchemaName parses an optional index/constraint name.
func (p *parser) parseSchemaName() string {
	if p.at(IDENT) && !p.atKw(KwIf) && !p.atKw(KwFor) {
		name, _ := p.name("name")
		return name
	}
	return ""
}

// parseSchemaTarget parses `(n:Label)` or `()-[r:TYPE]-()`.
func (p *parser) parseSchemaTarget() PatternElem {
	pos := p.cur().Pos
	elems := p.continueElems([]PatternElem{p.parseNodePattern()})
	switch {
	case len(elems) == 1:
		return elems[0]
	case len(elems) == 3:
		if _, ok := elems[1].(*RelPattern); ok {
			return elems[1]
		}
	}
	p.fail(pos, "expected a node pattern (n:Label) or a relationship pattern ()-[r:TYPE]-()")
	return nil
}

// parseOptionsOpt parses `OPTIONS {…}`.
func (p *parser) parseOptionsOpt() Expr {
	if p.cur().isWord("options") {
		p.next()
		return p.parseMapLiteral()
	}
	return nil
}

func (p *parser) parseCreateIndex() Body {
	start := p.expectKw(KwCreate)
	c := &CreateIndex{Loc: Loc{start.Pos}}
	if p.cur().Kind == IDENT && indexKinds[lower(p.cur().Text)] {
		c.Kind = upper(p.next().Text)
	}
	p.expectKw(KwIndex)
	c.Name = p.parseSchemaName()
	c.IfNotExists = p.parseIfClause(true)
	p.expectKw(KwFor)
	c.Target = p.parseSchemaTarget()
	p.expectKw(KwOn)
	if p.cur().isWord("each") {
		p.next()
		c.Each = true
		if p.at(LBRACK) {
			list := p.parseListLiteral().(*ListLit)
			c.Properties = list.Elems
		} else {
			c.Properties = []Expr{p.parsePostfix()}
		}
	} else {
		c.Properties = p.parsePropertyList()
	}
	c.Options = p.parseOptionsOpt()
	return c
}

// parsePropertyList parses `n.prop` or `(n.prop, m.other, …)`.
func (p *parser) parsePropertyList() []Expr {
	if !p.at(LPAREN) {
		return []Expr{p.parsePostfix()}
	}
	p.next()
	var props []Expr
	for {
		props = append(props, p.parsePostfix())
		if !p.accept(COMMA) {
			break
		}
	}
	p.expect(RPAREN)
	return props
}

func (p *parser) parseCreateConstraint() Body {
	start := p.expectKw(KwCreate)
	p.expectKw(KwConstraint)
	c := &CreateConstraint{Loc: Loc{start.Pos}}
	c.Name = p.parseSchemaName()
	c.IfNotExists = p.parseIfClause(true)
	p.expectKw(KwFor)
	c.Target = p.parseSchemaTarget()
	p.expectKw(KwRequire)
	c.Properties = p.parsePropertyList()
	p.expectKw(KwIs)
	switch t := p.cur(); {
	case t.Is(KwUnique):
		p.next()
		c.Kind = ConstraintUnique
	case t.Is(KwNot):
		p.next()
		p.expectKw(KwNull)
		c.Kind = ConstraintNotNull
	case t.Is(KwNode) || t.isWord("relationship"):
		p.next()
		if !p.cur().isWord("key") {
			p.unexpected("KEY")
		}
		p.next()
		c.Kind = ConstraintKey
	case t.Kind == DOUBLECOLON || t.Is(KwTyped):
		p.next()
		c.Kind = ConstraintType
		c.ValueType = p.parseTypeText()
	default:
		p.unexpected("UNIQUE", "NOT NULL", "NODE KEY", "RELATIONSHIP KEY", ":: type")
	}
	c.Options = p.parseOptionsOpt()
	return c
}

func (p *parser) parseDropSchema() Body {
	start := p.expectKw(KwDrop)
	d := &DropSchema{Loc: Loc{start.Pos}}
	if p.acceptKw(KwConstraint) {
		d.Constraint = true
	} else {
		p.expectKw(KwIndex)
	}
	d.Name, _ = p.name("name")
	d.IfExists = p.parseIfClause(false)
	return d
}

// parseShow parses `SHOW words… [YIELD …] [WHERE expr]`. What is the upper-cased
// words, e.g. "INDEXES" or "ALL CONSTRAINTS".
func (p *parser) parseShow() Body {
	start := p.expectKw(KwShow)
	s := &Show{Loc: Loc{start.Pos}}
	for p.at(IDENT) && !p.atKw(KwYield) && !p.atKw(KwWhere) {
		if s.What != "" {
			s.What += " "
		}
		s.What += upper(p.next().Value)
	}
	if s.What == "" {
		p.unexpected("what to show")
	}
	switch {
	case p.atKw(KwYield):
		s.Yield = p.parseYield()
	case p.acceptKw(KwWhere):
		s.Where = p.parseExpr()
	}
	if p.atKw(KwReturn) {
		s.Return = p.parseReturn().(*Return)
	}
	return s
}
