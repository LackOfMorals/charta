package syntax

// Parse parses a complete Cypher statement.
//
// It is safe for concurrent use: each call lexes and parses independently and
// shares no mutable state.
func Parse(src string) (*Statement, error) {
	p, err := newParser(src)
	if err != nil {
		return nil, err
	}
	var st *Statement
	err = p.run(func() { st = p.parseStatement() })
	if err != nil {
		return nil, err
	}
	return st, nil
}

func (p *parser) parseStatement() *Statement {
	st := &Statement{Loc: Loc{p.cur().Pos}, Src: p.src}
	st.Body = p.parseBody()
	p.accept(SEMI)
	if !p.at(EOF) {
		p.unexpected("end of input")
	}
	return st
}

// parseBody parses a query optionally combined with UNION [ALL].
func (p *parser) parseBody() Body {
	first := p.parseSingleQuery()
	if !p.atKw(KwUnion) {
		return first
	}
	u := &UnionQuery{Loc: Loc{first.Pos()}, Queries: []*SingleQuery{first}}
	p.requireReturn(first, "UNION")
	for p.atKw(KwUnion) {
		t := p.next()
		all := p.acceptKw(KwAll)
		if len(u.Queries) == 1 {
			u.All = all
		} else if all != u.All {
			p.fail(t.Pos, "cannot mix UNION and UNION ALL in one statement")
		}
		q := p.parseSingleQuery()
		p.requireReturn(q, "UNION")
		u.Queries = append(u.Queries, q)
	}
	return u
}

// requireReturn checks that every query in a UNION ends with RETURN.
func (p *parser) requireReturn(q *SingleQuery, ctx string) {
	last := q.Clauses[len(q.Clauses)-1]
	if _, ok := last.(*Return); !ok {
		p.fail(last.Pos(), "every query in a %s must end with RETURN", ctx)
	}
}

// parseSingleQuery parses clauses until the next token cannot start one, then
// checks the clause sequence.
func (p *parser) parseSingleQuery() *SingleQuery {
	q := &SingleQuery{Loc: Loc{p.cur().Pos}}
	for {
		c := p.parseClauseOpt()
		if c == nil {
			break
		}
		q.Clauses = append(q.Clauses, c)
	}
	if len(q.Clauses) == 0 {
		p.unexpected("clause")
	}
	p.checkClauseOrder(q.Clauses)
	return q
}

// checkClauseOrder enforces the openCypher clause-composition rules:
//
//   - after an updating clause a reading clause (MATCH, UNWIND, LOAD CSV)
//     needs an intervening WITH;
//   - RETURN, if present, is the last clause;
//   - a query ends with RETURN, an updating clause, FINISH or a CALL (a CALL
//     with YIELD may only end a query if it is the only clause).
func (p *parser) checkClauseOrder(cs []Clause) {
	updated := false
	for i, c := range cs {
		switch {
		case isReading(c):
			if updated {
				p.fail(c.Pos(), "WITH is required between an updating clause and %s", clauseName(c))
			}
		case isUpdating(c):
			updated = true
		}
		switch c.(type) {
		case *With:
			updated = false
		case *Return:
			if i != len(cs)-1 {
				p.fail(cs[i+1].Pos(), "RETURN must be the last clause of a query, found %s after it", clauseName(cs[i+1]))
			}
		}
	}
	last := cs[len(cs)-1]
	switch last := last.(type) {
	case *Return, *Finish:
		return
	case *Call:
		if last.Yield != nil && len(cs) > 1 {
			p.fail(last.Pos(), "query cannot conclude with CALL … YIELD; add a RETURN")
		}
		return
	}
	if isUpdating(last) {
		return
	}
	p.fail(last.Pos(), "query cannot conclude with %s (it must end with RETURN or an updating clause)", clauseName(last))
}
