package syntax

import (
	"math"
	"strconv"
	"strings"
)

// Expression grammar and precedence.
//
// The levels below follow the openCypher 9 grammar (the one the former
// ANTLR-based parser used), lowest binding first:
//
//	OR
//	XOR
//	AND
//	NOT                       (prefix, repeatable)
//	comparison                = <> < > <= >=  (chained: a < b < c)
//	additive                  + - ||
//	multiplicative            * / %
//	power                     ^               (left-associative, like the grammar's flat list)
//	unary                     prefix + -
//	string/list/null          IN, STARTS WITH, ENDS WITH, CONTAINS, =~, IS [NOT] NULL
//	property/label/subscript  .key  [i]  [i..j]  :Label
//	atom
//
// Consequences worth knowing: the string/list/null operators bind tighter than
// arithmetic, so `a + b IN c` is `a + (b IN c)`; and unary minus binds tighter
// than ^, so `-x^2` is `(-x)^2`. A '-' directly before a numeric literal is
// folded into the literal, so `-2^2` is `(-2)^2` and -9223372036854775808 is
// representable. These choices mirror the grammar and are cross-checked against
// the TCK precedence scenarios in task-017; adjust here if Neo4j differs.
//
// Deliberate leniency over the grammar: subscripts, slices and property
// lookups may be freely interleaved in any order (`list[0].name`, `n.a[1]`).

// parseExpr parses a full expression.
func (p *parser) parseExpr() Expr {
	p.enter()
	defer p.leave()
	return p.parseOr()
}

func (p *parser) parseOr() Expr {
	l := p.parseXor()
	for p.atKw(KwOr) {
		p.next()
		r := p.parseXor()
		l = &Binary{Loc{l.Pos()}, OpOr, l, r}
	}
	return l
}

func (p *parser) parseXor() Expr {
	l := p.parseAnd()
	for p.atKw(KwXor) {
		p.next()
		r := p.parseAnd()
		l = &Binary{Loc{l.Pos()}, OpXor, l, r}
	}
	return l
}

func (p *parser) parseAnd() Expr {
	l := p.parseNot()
	for p.atKw(KwAnd) {
		p.next()
		r := p.parseNot()
		l = &Binary{Loc{l.Pos()}, OpAnd, l, r}
	}
	return l
}

func (p *parser) parseNot() Expr {
	var nots []Pos
	for p.atKw(KwNot) {
		nots = append(nots, p.next().Pos)
		if len(nots) > maxDepth {
			p.fail(p.cur().Pos, "query is nested too deeply (limit %d)", maxDepth)
		}
	}
	x := p.parseComparison()
	for i := len(nots) - 1; i >= 0; i-- {
		x = &Unary{Loc{nots[i]}, OpNot, x}
	}
	return x
}

func compareOp(k Kind) (CompareOp, bool) {
	switch k {
	case EQ:
		return CmpEq, true
	case NEQ:
		return CmpNeq, true
	case LT:
		return CmpLt, true
	case GT:
		return CmpGt, true
	case LTE:
		return CmpLte, true
	case GTE:
		return CmpGte, true
	}
	return "", false
}

func (p *parser) parseComparison() Expr {
	first := p.parseAdditive()
	op, ok := compareOp(p.cur().Kind)
	if !ok {
		return first
	}
	c := &Comparison{Loc: Loc{first.Pos()}, Operands: []Expr{first}}
	for ok {
		p.next()
		c.Ops = append(c.Ops, op)
		c.Operands = append(c.Operands, p.parseAdditive())
		op, ok = compareOp(p.cur().Kind)
	}
	return c
}

func (p *parser) parseAdditive() Expr {
	l := p.parseMultiplicative()
	for {
		var op BinaryOp
		switch p.cur().Kind {
		case PLUS:
			op = OpAdd
		case MINUS:
			op = OpSub
		case PIPEPIPE:
			op = OpConcat
		default:
			return l
		}
		p.next()
		r := p.parseMultiplicative()
		l = &Binary{Loc{l.Pos()}, op, l, r}
	}
}

func (p *parser) parseMultiplicative() Expr {
	l := p.parsePower()
	for {
		var op BinaryOp
		switch p.cur().Kind {
		case STAR:
			op = OpMul
		case SLASH:
			op = OpDiv
		case PERCENT:
			op = OpMod
		default:
			return l
		}
		p.next()
		r := p.parsePower()
		l = &Binary{Loc{l.Pos()}, op, l, r}
	}
}

func (p *parser) parsePower() Expr {
	l := p.parseUnary()
	for p.at(CARET) {
		p.next()
		r := p.parseUnary()
		l = &Binary{Loc{l.Pos()}, OpPow, l, r}
	}
	return l
}

func (p *parser) parseUnary() Expr {
	var ops []Token
	for p.at(PLUS) || p.at(MINUS) {
		ops = append(ops, p.next())
		if len(ops) > maxDepth {
			p.fail(p.cur().Pos, "query is nested too deeply (limit %d)", maxDepth)
		}
	}
	var x Expr
	if n := len(ops); n > 0 && ops[n-1].Kind == MINUS && (p.at(INT) || p.at(FLOAT)) {
		// Fold the sign into the literal.
		minus := ops[n-1]
		ops = ops[:n-1]
		x = p.parsePredicateTail(p.numberLiteral(minus))
	} else {
		x = p.parsePredicates()
	}
	for i := len(ops) - 1; i >= 0; i-- {
		op := OpPlus
		if ops[i].Kind == MINUS {
			op = OpMinus
		}
		x = &Unary{Loc{ops[i].Pos}, op, x}
	}
	return x
}

// parsePredicates parses a postfix expression followed by any string/list/null
// predicate operators.
func (p *parser) parsePredicates() Expr {
	return p.parsePredicateTail(p.parsePostfix())
}

func (p *parser) parsePredicateTail(l Expr) Expr {
	for {
		t := p.cur()
		switch {
		case t.Is(KwIn):
			p.next()
			l = &Binary{Loc{l.Pos()}, OpIn, l, p.parsePostfix()}
		case t.Is(KwStarts):
			p.next()
			p.expectKw(KwWith)
			l = &Binary{Loc{l.Pos()}, OpStartsWith, l, p.parsePostfix()}
		case t.Is(KwEnds):
			p.next()
			p.expectKw(KwWith)
			l = &Binary{Loc{l.Pos()}, OpEndsWith, l, p.parsePostfix()}
		case t.Is(KwContains):
			p.next()
			l = &Binary{Loc{l.Pos()}, OpContains, l, p.parsePostfix()}
		case t.Kind == EQTILDE:
			p.next()
			l = &Binary{Loc{l.Pos()}, OpRegexMatch, l, p.parsePostfix()}
		case t.Is(KwIs):
			p.next()
			negated := p.acceptKw(KwNot)
			switch c := p.cur(); {
			case c.Kind == DOUBLECOLON || c.Is(KwTyped):
				p.next()
				l = &TypePredicate{Loc{l.Pos()}, l, p.parseTypeText(), negated}
			case c.Is(KwNormalized):
				p.next()
				l = &Normalized{Loc: Loc{l.Pos()}, X: l, Negated: negated}
			case isNormalForm(c) && p.peek(1).Is(KwNormalized):
				p.next()
				p.next()
				l = &Normalized{Loc: Loc{l.Pos()}, X: l, Form: upper(c.Text), Negated: negated}
			default:
				p.expectKw(KwNull)
				l = &IsNull{Loc{l.Pos()}, l, negated}
			}
		case t.Kind == DOUBLECOLON:
			p.next()
			l = &TypePredicate{Loc{l.Pos()}, l, p.parseTypeText(), false}
		default:
			return l
		}
	}
}

// parsePostfix parses an atom followed by property lookups, subscripts, slices
// and label predicates.
func (p *parser) parsePostfix() Expr {
	x := p.parseAtom()
	for {
		switch p.cur().Kind {
		case DOT:
			p.next()
			key, _ := p.name("property key name")
			x = &Property{Loc{x.Pos()}, x, key}
		case LBRACK:
			x = p.parseSubscript(x)
		case COLON:
			p.next()
			x = &HasLabels{Loc{x.Pos()}, x, p.parseLabelExpr()}
		case LBRACE:
			// `var{.key, …}` is a map projection; a brace after anything
			// else is not part of the expression.
			id, ok := x.(*Ident)
			if !ok {
				return x
			}
			x = p.parseMapProjection(id)
		default:
			return x
		}
	}
}

// parseSubscript parses `[i]`, `[i..j]`, `[..j]`, `[i..]` or `[..]` applied to x.
func (p *parser) parseSubscript(x Expr) Expr {
	p.expect(LBRACK)
	var from Expr
	if !p.at(DOTDOT) {
		from = p.parseExpr()
		if p.at(RBRACK) {
			p.next()
			return &Subscript{Loc{x.Pos()}, x, from}
		}
	}
	p.expect(DOTDOT)
	var to Expr
	if !p.at(RBRACK) {
		to = p.parseExpr()
	}
	p.expect(RBRACK)
	return &Slice{Loc{x.Pos()}, x, from, to}
}

// ─── atoms ───────────────────────────────────────────────────────────────────

func (p *parser) parseAtom() Expr {
	t := p.cur()
	switch t.Kind {
	case INT, FLOAT:
		return p.numberLiteral(Token{})
	case STRING:
		p.next()
		return &StringLit{Loc{t.Pos}, t.Value}
	case PARAM:
		p.next()
		return &Param{Loc{t.Pos}, t.Value}
	case LBRACK:
		return p.parseListLiteral()
	case LBRACE:
		return p.parseMapLiteral()
	case LPAREN:
		if part := p.tryPatternPart(false); part != nil {
			return &PatternExpr{Loc{t.Pos}, part}
		}
		p.next()
		x := p.parseExpr()
		p.expect(RPAREN)
		return x
	case IDENT:
		return p.parseIdentAtom()
	}
	p.unexpected("expression")
	return nil
}

// numberLiteral consumes an INT or FLOAT token. If sign is a MINUS token (its
// Kind set) it has already been consumed and is folded into the literal.
func (p *parser) numberLiteral(sign Token) Expr {
	t := p.next()
	if n := p.cur(); n.Kind == IDENT && n.Pos.Offset == t.End() {
		p.failc(CodeInvalidNumberLiteral, t.Pos, "invalid numeric literal %s%s", t.Text, n.Text)
	}
	neg := sign.Kind == MINUS
	start := t.Pos
	text := t.Text
	if neg {
		start = sign.Pos
		text = p.src[sign.Pos.Offset:t.End()]
	}
	if t.Kind == FLOAT {
		v, err := strconv.ParseFloat(t.Text, 64)
		if err != nil || math.IsInf(v, 0) {
			p.failc(CodeFloatingPointOverflow, t.Pos, "floating point number %s is too large", t.Text)
		}
		if neg {
			v = -v
		}
		return &FloatLit{Loc{start}, v, text}
	}
	v, err := parseIntLiteral(t.Text, neg)
	if err != nil {
		p.failInt(t.Pos, err)
	}
	return &IntLit{Loc{start}, v, text}
}

// parseIntLiteral converts an INT token's text. A leading 0x is hexadecimal, a
// leading 0o or a leading 0 followed by digits is octal.
func parseIntLiteral(text string, neg bool) (int64, error) {
	digits, base := text, 10
	switch {
	case len(text) > 2 && text[0] == '0' && (text[1] == 'x' || text[1] == 'X'):
		digits, base = text[2:], 16
	case len(text) > 2 && text[0] == '0' && (text[1] == 'o' || text[1] == 'O'):
		digits, base = text[2:], 8
	case len(text) > 1 && text[0] == '0':
		digits, base = text[1:], 8
	}
	u, err := strconv.ParseUint(digits, base, 64)
	if err != nil {
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			return 0, errIntTooLarge(text)
		}
		return 0, &intError{"invalid integer literal " + text, CodeInvalidNumberLiteral}
	}
	limit := uint64(math.MaxInt64)
	if neg {
		limit++
	}
	if u > limit {
		return 0, errIntTooLarge(text)
	}
	if neg {
		return -int64(u), nil
	}
	return int64(u), nil
}

type intError struct {
	msg  string
	code string
}

func (e *intError) Error() string { return e.msg }

func errIntTooLarge(text string) error {
	return &intError{"integer literal " + text + " is too large", CodeIntegerOverflow}
}

// failInt reports an integer-literal error with its TCK code.
func (p *parser) failInt(pos Pos, err error) {
	code := CodeInvalidNumberLiteral
	if ie, ok := err.(*intError); ok {
		code = ie.code
	}
	p.failc(code, pos, "%s", err.Error())
}

func (p *parser) parseListLiteral() Expr {
	open := p.expect(LBRACK)
	if p.listCompAhead() {
		return p.parseListComp(open)
	}
	if c := p.tryPatternComp(open); c != nil {
		return c
	}
	l := &ListLit{Loc: Loc{open.Pos}}
	if !p.at(RBRACK) {
		for {
			l.Elems = append(l.Elems, p.parseExpr())
			if !p.accept(COMMA) {
				break
			}
		}
	}
	p.expect(RBRACK)
	return l
}

func (p *parser) parseMapLiteral() *MapLit {
	open := p.expect(LBRACE)
	m := &MapLit{Loc: Loc{open.Pos}}
	if !p.at(RBRACE) {
		for {
			key, pos := p.name("property key name")
			p.expect(COLON)
			m.Entries = append(m.Entries, MapEntry{Loc{pos}, key, p.parseExpr()})
			if !p.accept(COMMA) {
				break
			}
		}
	}
	p.expect(RBRACE)
	return m
}

// parseIdentAtom handles an atom that starts with an identifier: keyword
// literals, CASE, function calls (possibly namespaced) and plain variables.
func (p *parser) parseIdentAtom() Expr {
	t := p.cur()
	if nonAtomKeywords[t.Keyword] {
		p.unexpected("expression")
	}
	switch t.Keyword {
	case KwTrue:
		p.next()
		return &BoolLit{Loc{t.Pos}, true}
	case KwFalse:
		p.next()
		return &BoolLit{Loc{t.Pos}, false}
	case KwNull:
		p.next()
		return &NullLit{Loc{t.Pos}}
	case KwCase:
		return p.parseCase()
	}
	if p.subqueryExprAhead() {
		return p.parseSubqueryExpr()
	}
	if (t.isWord("extract") || t.isWord("filter")) && p.peek(1).Kind == LPAREN &&
		p.peek(2).Kind == IDENT && p.peek(3).Is(KwIn) {
		p.fail(t.Pos, "%s() is not supported; use a list comprehension instead", strings.ToLower(t.Text))
	}
	if kind, ok := p.quantifierAhead(); ok {
		return p.parseQuantifier(kind)
	}
	if p.reduceAhead() {
		return p.parseReduce()
	}
	if p.shortestFuncAhead() != FuncNone {
		if part := p.tryPatternPart(false); part != nil {
			return &PatternExpr{Loc{t.Pos}, part}
		}
	}
	if p.isCallAhead() {
		return p.parseFuncCall()
	}
	p.next()
	return &Ident{Loc{t.Pos}, t.Value}
}

// isCallAhead reports whether the tokens at the cursor are
// `name ( ` or `ns.name ( ` (any number of dotted namespace parts).
func (p *parser) isCallAhead() bool {
	i := 0
	for p.peek(i).Kind == IDENT && p.peek(i+1).Kind == DOT {
		i += 2
	}
	return p.peek(i).Kind == IDENT && p.peek(i+1).Kind == LPAREN
}

func (p *parser) parseFuncCall() Expr {
	start := p.cur().Pos
	var parts []string
	for {
		name, _ := p.name("function name")
		parts = append(parts, name)
		if !p.accept(DOT) {
			break
		}
	}
	f := &FuncCall{Loc: Loc{start}, Name: parts[len(parts)-1]}
	if len(parts) > 1 {
		f.Namespace = parts[:len(parts)-1]
	}
	p.expect(LPAREN)
	switch {
	case p.at(STAR):
		star := p.next()
		if len(f.Namespace) != 0 || !equalFold(f.Name, "count") {
			p.fail(star.Pos, "'*' is only allowed as the argument of count()")
		}
		f.Star = true
	case p.at(RPAREN):
	default:
		f.Distinct = p.acceptKw(KwDistinct)
		for {
			f.Args = append(f.Args, p.parseExpr())
			if !p.accept(COMMA) {
				break
			}
		}
	}
	p.expect(RPAREN)
	return f
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

// parseCase parses `CASE [subject] WHEN … THEN … [ELSE …] END`.
func (p *parser) parseCase() Expr {
	start := p.expectKw(KwCase)
	c := &Case{Loc: Loc{start.Pos}}
	if !p.atKw(KwWhen) {
		c.Subject = p.parseExpr()
	}
	if !p.atKw(KwWhen) {
		p.unexpected("WHEN")
	}
	for p.atKw(KwWhen) {
		w := p.next()
		cond := p.parseExpr()
		p.expectKw(KwThen)
		c.Whens = append(c.Whens, CaseWhen{Loc{w.Pos}, cond, p.parseExpr()})
	}
	if p.acceptKw(KwElse) {
		c.Else = p.parseExpr()
	}
	p.expectKw(KwEnd)
	return c
}

// nonAtomKeywords are operator and clause-structure words that can never start
// an expression. Rejecting them stops `a = NOT (b)` from being read as a call
// to a function named NOT, and `CASE END` from treating END as the subject.
// Other keywords stay usable as variable and function names.
var nonAtomKeywords = map[Keyword]bool{
	KwNot: true, KwAnd: true, KwOr: true, KwXor: true, KwIn: true, KwIs: true,
	KwAs: true, KwStarts: true, KwEnds: true, KwContains: true,
	KwWhen: true, KwThen: true, KwElse: true, KwEnd: true,
}
