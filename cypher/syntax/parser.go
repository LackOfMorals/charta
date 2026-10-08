package syntax

import (
	"fmt"
	"strings"
)

// maxDepth bounds expression/pattern nesting so adversarial input such as
// "((((…" produces a syntax error instead of exhausting the goroutine stack.
const maxDepth = 400

// parser is a recursive-descent parser over a pre-lexed token slice. Syntax
// errors are raised with panic(bailout{}) and recovered in run, which keeps the
// grammar functions free of error plumbing. A parser is single-use and not
// safe for concurrent use; the package-level entry points create one per call.
type parser struct {
	src   string
	toks  []Token
	i     int
	depth int
}

// bailout carries a syntax error out of the recursive descent.
type bailout struct{ err *SyntaxError }

func newParser(src string) (*parser, error) {
	toks, err := Tokenize(src)
	if err != nil {
		return nil, err
	}
	return &parser{src: src, toks: toks}, nil
}

// run executes fn, converting a bailout panic into an error.
func (p *parser) run(fn func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			b, ok := r.(bailout)
			if !ok {
				panic(r)
			}
			err = b.err
		}
	}()
	fn()
	return nil
}

// ParseExpr parses src as a single expression. It exists mainly for tests and
// tooling; queries are parsed with Parse.
func ParseExpr(src string) (Expr, error) {
	p, err := newParser(src)
	if err != nil {
		return nil, err
	}
	var e Expr
	err = p.run(func() {
		e = p.parseExpr()
		if !p.at(EOF) {
			p.unexpected("end of input")
		}
	})
	if err != nil {
		return nil, err
	}
	return e, nil
}

// ─── token helpers ───────────────────────────────────────────────────────────

func (p *parser) cur() Token { return p.toks[p.i] }

// peek returns the token n positions ahead (EOF past the end).
func (p *parser) peek(n int) Token {
	if j := p.i + n; j < len(p.toks) {
		return p.toks[j]
	}
	return p.toks[len(p.toks)-1]
}

func (p *parser) next() Token {
	t := p.toks[p.i]
	if t.Kind != EOF {
		p.i++
	}
	return t
}

func (p *parser) at(k Kind) bool { return p.toks[p.i].Kind == k }

func (p *parser) atKw(kw Keyword) bool { return p.toks[p.i].Is(kw) }

func (p *parser) accept(k Kind) bool {
	if p.at(k) {
		p.next()
		return true
	}
	return false
}

func (p *parser) acceptKw(kw Keyword) bool {
	if p.atKw(kw) {
		p.next()
		return true
	}
	return false
}

func (p *parser) expect(k Kind) Token {
	if !p.at(k) {
		p.unexpected(k.String())
	}
	return p.next()
}

func (p *parser) expectKw(kw Keyword) Token {
	if !p.atKw(kw) {
		p.unexpected(string(kw))
	}
	return p.next()
}

// name consumes any identifier, including keywords and backtick-quoted names,
// and returns its decoded value. Cypher keywords are valid labels, property
// keys and variable names.
func (p *parser) name(what string) (string, Pos) {
	t := p.cur()
	if t.Kind != IDENT {
		p.unexpected(what)
	}
	p.next()
	return t.Value, t.Pos
}

// unexpected aborts parsing with an error describing the current token.
func (p *parser) unexpected(expected ...string) {
	t := p.cur()
	e := &SyntaxError{Pos: t.Pos, Expected: expected}
	if t.Kind == EOF {
		e.Msg = "unexpected end of input"
	} else {
		e.Msg = "unexpected token"
		e.Found = t.Text
	}
	panic(bailout{e})
}

// fail aborts parsing with a custom message at pos.
func (p *parser) fail(pos Pos, format string, args ...any) {
	panic(bailout{&SyntaxError{Pos: pos, Msg: fmt.Sprintf(format, args...)}})
}

// failc is fail with a TCK error code.
func (p *parser) failc(code string, pos Pos, format string, args ...any) {
	panic(bailout{&SyntaxError{Pos: pos, Msg: fmt.Sprintf(format, args...), Code: code}})
}

// enter/leave bound recursion depth.
func (p *parser) enter() {
	p.depth++
	if p.depth > maxDepth {
		p.fail(p.cur().Pos, "query is nested too deeply (limit %d)", maxDepth)
	}
}

func (p *parser) leave() { p.depth-- }

// sourceBetween returns the verbatim source from the start of the token at
// index from up to the end of the token before index to (exclusive).
func (p *parser) sourceBetween(from, to int) string {
	if to <= from {
		return ""
	}
	return strings.TrimSpace(p.src[p.toks[from].Pos.Offset:p.toks[to-1].End()])
}
