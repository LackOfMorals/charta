package syntax

import (
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// Lexer scans Cypher source into tokens. Use Tokenize for the common case of
// lexing a whole query up front (the parser needs unbounded lookahead).
type Lexer struct {
	src  string
	off  int
	line int
	col  int
}

// NewLexer returns a lexer positioned at the start of src.
func NewLexer(src string) *Lexer {
	return &Lexer{src: src, line: 1, col: 1}
}

// Tokenize lexes all of src. The returned slice always ends with an EOF token
// when err is nil. On error it returns the tokens scanned so far and a
// *SyntaxError.
func Tokenize(src string) ([]Token, error) {
	l := NewLexer(src)
	toks := make([]Token, 0, len(src)/3+2)
	for {
		t, err := l.Next()
		if err != nil {
			return toks, err
		}
		toks = append(toks, t)
		if t.Kind == EOF {
			return toks, nil
		}
	}
}

func (l *Lexer) pos() Pos { return Pos{Offset: l.off, Line: l.line, Col: l.col} }

// advanceTo moves the lexer to byte offset to, maintaining line and column.
func (l *Lexer) advanceTo(to int) {
	s := l.src
	for l.off < to {
		c := s[l.off]
		switch {
		case c == '\n':
			l.line++
			l.col = 1
			l.off++
		case c < utf8.RuneSelf:
			l.col++
			l.off++
		default:
			_, w := utf8.DecodeRuneInString(s[l.off:])
			l.col++
			l.off += w
		}
	}
}

// posAt returns the position of byte offset off, which must lie at or after
// start's offset within the same source. Used on error paths only.
func (l *Lexer) posAt(start Pos, off int) Pos {
	p := start
	s := l.src
	for p.Offset < off {
		c := s[p.Offset]
		switch {
		case c == '\n':
			p.Line++
			p.Col = 1
			p.Offset++
		case c < utf8.RuneSelf:
			p.Col++
			p.Offset++
		default:
			_, w := utf8.DecodeRuneInString(s[p.Offset:])
			p.Col++
			p.Offset += w
		}
	}
	return p
}

func (l *Lexer) errorf(p Pos, found, msg string) error {
	return &SyntaxError{Pos: p, Found: found, Msg: msg}
}

func isIdentStartByte(c byte) bool {
	return c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

func isHexDigit(c byte) bool {
	return isDigit(c) || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func isIdentStartRune(r rune) bool { return r == '_' || unicode.IsLetter(r) }

func isIdentPartRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) ||
		unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Pc, r) ||
		unicode.Is(unicode.Sc, r)
}

// skipTrivia skips whitespace and comments.
func (l *Lexer) skipTrivia() error {
	s := l.src
	for l.off < len(s) {
		c := s[l.off]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f':
			l.advanceTo(l.off + 1)
		case c == '/' && l.off+1 < len(s) && s[l.off+1] == '/':
			end := strings.IndexByte(s[l.off:], '\n')
			if end < 0 {
				l.advanceTo(len(s))
			} else {
				l.advanceTo(l.off + end)
			}
		case c == '/' && l.off+1 < len(s) && s[l.off+1] == '*':
			start := l.pos()
			end := strings.Index(s[l.off+2:], "*/")
			if end < 0 {
				return l.errorf(start, "/*", "unterminated block comment")
			}
			l.advanceTo(l.off + 2 + end + 2)
		case c >= utf8.RuneSelf:
			r, w := utf8.DecodeRuneInString(s[l.off:])
			if r != utf8.RuneError && (unicode.IsSpace(r) || r == 0xFEFF) {
				l.advanceTo(l.off + w)
				continue
			}
			return nil
		default:
			return nil
		}
	}
	return nil
}

// Next returns the next token. After the input is exhausted it keeps
// returning EOF.
func (l *Lexer) Next() (Token, error) {
	if err := l.skipTrivia(); err != nil {
		return Token{}, err
	}
	s := l.src
	start := l.pos()
	if l.off >= len(s) {
		return Token{Kind: EOF, Pos: start}, nil
	}
	c := s[l.off]
	switch {
	case isIdentStartByte(c):
		return l.identifier(start), nil
	case isDigit(c):
		return l.number(start)
	case c == '\'' || c == '"':
		return l.str(start, c)
	case c == '`':
		return l.quotedIdent(start)
	case c == '$':
		return l.param(start)
	case c >= utf8.RuneSelf:
		r, w := utf8.DecodeRuneInString(s[l.off:])
		if r == utf8.RuneError && w == 1 {
			return Token{}, l.errorf(start, "", "invalid UTF-8 encoding")
		}
		if isIdentStartRune(r) {
			return l.identifier(start), nil
		}
		return Token{}, l.errorf(start, string(r), "unexpected character")
	}
	return l.operator(start, c)
}

func (l *Lexer) emit(start Pos, k Kind, n int) Token {
	text := l.src[l.off : l.off+n]
	l.advanceTo(l.off + n)
	return Token{Kind: k, Pos: start, Text: text, Value: text}
}

func (l *Lexer) operator(start Pos, c byte) (Token, error) {
	s := l.src
	var next byte
	if l.off+1 < len(s) {
		next = s[l.off+1]
	}
	switch c {
	case '(':
		return l.emit(start, LPAREN, 1), nil
	case ')':
		return l.emit(start, RPAREN, 1), nil
	case '[':
		return l.emit(start, LBRACK, 1), nil
	case ']':
		return l.emit(start, RBRACK, 1), nil
	case '{':
		return l.emit(start, LBRACE, 1), nil
	case '}':
		return l.emit(start, RBRACE, 1), nil
	case ',':
		return l.emit(start, COMMA, 1), nil
	case ';':
		return l.emit(start, SEMI, 1), nil
	case '&':
		return l.emit(start, AMP, 1), nil
	case '!':
		return l.emit(start, BANG, 1), nil
	case '%':
		return l.emit(start, PERCENT, 1), nil
	case '^':
		return l.emit(start, CARET, 1), nil
	case '*':
		return l.emit(start, STAR, 1), nil
	case '-':
		return l.emit(start, MINUS, 1), nil
	case '/':
		return l.emit(start, SLASH, 1), nil
	case '.':
		if isDigit(next) {
			return l.number(start)
		}
		if next == '.' {
			return l.emit(start, DOTDOT, 2), nil
		}
		return l.emit(start, DOT, 1), nil
	case ':':
		if next == ':' {
			return l.emit(start, DOUBLECOLON, 2), nil
		}
		return l.emit(start, COLON, 1), nil
	case '|':
		if next == '|' {
			return l.emit(start, PIPEPIPE, 2), nil
		}
		return l.emit(start, PIPE, 1), nil
	case '+':
		if next == '=' {
			return l.emit(start, PLUSEQ, 2), nil
		}
		return l.emit(start, PLUS, 1), nil
	case '=':
		if next == '~' {
			return l.emit(start, EQTILDE, 2), nil
		}
		return l.emit(start, EQ, 1), nil
	case '<':
		switch next {
		case '>':
			return l.emit(start, NEQ, 2), nil
		case '=':
			return l.emit(start, LTE, 2), nil
		}
		return l.emit(start, LT, 1), nil
	case '>':
		if next == '=' {
			return l.emit(start, GTE, 2), nil
		}
		return l.emit(start, GT, 1), nil
	}
	return Token{}, l.errorf(start, string(rune(c)), "unexpected character")
}

// identifier scans an unquoted identifier starting at l.off.
func (l *Lexer) identifier(start Pos) Token {
	s := l.src
	i := l.off
	for i < len(s) {
		c := s[i]
		if isIdentStartByte(c) || isDigit(c) {
			i++
			continue
		}
		if c < utf8.RuneSelf {
			break
		}
		r, w := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError || !isIdentPartRune(r) {
			break
		}
		i += w
	}
	text := s[l.off:i]
	l.advanceTo(i)
	kw, _ := LookupKeyword(text)
	return Token{Kind: IDENT, Pos: start, Text: text, Value: text, Keyword: kw}
}

// quotedIdent scans a backtick-quoted identifier; “ inside is an escaped backtick.
func (l *Lexer) quotedIdent(start Pos) (Token, error) {
	s := l.src
	i := l.off + 1
	escaped := false
	for {
		j := strings.IndexByte(s[i:], '`')
		if j < 0 {
			return Token{}, l.errorf(start, "`", "unterminated quoted identifier")
		}
		i += j
		if i+1 < len(s) && s[i+1] == '`' {
			escaped = true
			i += 2
			continue
		}
		break
	}
	text := s[l.off : i+1]
	value := text[1 : len(text)-1]
	if escaped {
		value = strings.ReplaceAll(value, "``", "`")
	}
	if !utf8.ValidString(value) {
		return Token{}, l.errorf(start, "", "invalid UTF-8 encoding")
	}
	l.advanceTo(i + 1)
	return Token{Kind: IDENT, Pos: start, Text: text, Value: value}, nil
}

// param scans $name, $0 or $`name`; a bare $ yields DOLLAR (for $(expr)).
func (l *Lexer) param(start Pos) (Token, error) {
	s := l.src
	i := l.off + 1
	if i >= len(s) {
		return l.emit(start, DOLLAR, 1), nil
	}
	c := s[i]
	switch {
	case isDigit(c):
		for i < len(s) && isDigit(s[i]) {
			i++
		}
	case isIdentStartByte(c):
		for i < len(s) && (isIdentStartByte(s[i]) || isDigit(s[i])) {
			i++
		}
		for i < len(s) && s[i] >= utf8.RuneSelf {
			r, w := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError || !isIdentPartRune(r) {
				break
			}
			i += w
			for i < len(s) && (isIdentStartByte(s[i]) || isDigit(s[i])) {
				i++
			}
		}
	case c == '`':
		l.advanceTo(l.off + 1)
		inner, err := l.quotedIdent(l.pos())
		if err != nil {
			return Token{}, err
		}
		return Token{
			Kind: PARAM, Pos: start,
			Text: s[start.Offset:inner.End()], Value: inner.Value,
		}, nil
	case c >= utf8.RuneSelf:
		r, _ := utf8.DecodeRuneInString(s[i:])
		if !isIdentStartRune(r) {
			return l.emit(start, DOLLAR, 1), nil
		}
		for i < len(s) {
			r, w := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError || !isIdentPartRune(r) {
				break
			}
			i += w
		}
	default:
		return l.emit(start, DOLLAR, 1), nil
	}
	text := s[l.off:i]
	l.advanceTo(i)
	return Token{Kind: PARAM, Pos: start, Text: text, Value: text[1:]}, nil
}

// number scans an integer or float literal beginning at l.off (a digit, or a
// '.' followed by a digit).
//
// Following the openCypher grammar, "1." is not a float, so "1..3" lexes as
// INT DOTDOT INT. Leading-zero octal such as 017 is lexed as INT; interpreting
// its value is the parser's job.
func (l *Lexer) number(start Pos) (Token, error) {
	s := l.src
	i := l.off
	kind := INT

	scanDigits := func() {
		for i < len(s) && isDigit(s[i]) {
			i++
		}
	}

	switch {
	case s[i] == '0' && i+1 < len(s) && (s[i+1] == 'x' || s[i+1] == 'X'):
		i += 2
		d := i
		for i < len(s) && isHexDigit(s[i]) {
			i++
		}
		if i == d {
			return Token{}, l.errorf(start, s[l.off:i], "malformed hexadecimal literal")
		}
	case s[i] == '0' && i+1 < len(s) && (s[i+1] == 'o' || s[i+1] == 'O'):
		i += 2
		d := i
		for i < len(s) && '0' <= s[i] && s[i] <= '7' {
			i++
		}
		if i == d {
			return Token{}, l.errorf(start, s[l.off:i], "malformed octal literal")
		}
	default:
		scanDigits()
		if i+1 < len(s) && s[i] == '.' && isDigit(s[i+1]) {
			kind = FLOAT
			i++
			scanDigits()
		}
		if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
			j := i + 1
			if j < len(s) && (s[j] == '+' || s[j] == '-') {
				j++
			}
			if j >= len(s) || !isDigit(s[j]) {
				return Token{}, l.errorf(start, s[l.off:j], "malformed exponent in numeric literal")
			}
			kind = FLOAT
			i = j
			scanDigits()
		}
	}

	if i < len(s) {
		r, _ := utf8.DecodeRuneInString(s[i:])
		if isIdentStartRune(r) || isDigit(s[i]) {
			end := i + utf8.RuneLen(r)
			return Token{}, l.errorf(start, s[l.off:end], "invalid numeric literal")
		}
	}
	text := s[l.off:i]
	l.advanceTo(i)
	return Token{Kind: kind, Pos: start, Text: text, Value: text}, nil
}

// str scans a single- or double-quoted string literal and decodes its escapes.
func (l *Lexer) str(start Pos, quote byte) (Token, error) {
	s := l.src
	i := l.off + 1
	segStart := i
	var sb *strings.Builder

	for {
		if i >= len(s) {
			return Token{}, l.errorf(start, string(quote), "unterminated string literal")
		}
		c := s[i]
		switch {
		case c == quote:
			var value string
			if sb == nil {
				value = s[segStart:i]
			} else {
				sb.WriteString(s[segStart:i])
				value = sb.String()
			}
			text := s[l.off : i+1]
			l.advanceTo(i + 1)
			return Token{Kind: STRING, Pos: start, Text: text, Value: value}, nil
		case c == '\\':
			if sb == nil {
				sb = &strings.Builder{}
			}
			sb.WriteString(s[segStart:i])
			escPos := l.posAt(start, i)
			n, err := l.escape(sb, escPos, i)
			if err != nil {
				return Token{}, err
			}
			i += n
			segStart = i
		case c >= utf8.RuneSelf:
			r, w := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && w == 1 {
				return Token{}, l.errorf(l.posAt(start, i), "", "invalid UTF-8 encoding")
			}
			i += w
		default:
			i++
		}
	}
}

// escape decodes the escape sequence starting at s[i] == '\\', writes it to sb
// and returns the number of source bytes consumed.
func (l *Lexer) escape(sb *strings.Builder, p Pos, i int) (int, error) {
	s := l.src
	if i+1 >= len(s) {
		return 0, l.errorf(p, "\\", "unterminated string literal")
	}
	switch c := s[i+1]; c {
	case '\\', '\'', '"', '`':
		sb.WriteByte(c)
	case 'b', 'B':
		sb.WriteByte('\b')
	case 'f', 'F':
		sb.WriteByte('\f')
	case 'n', 'N':
		sb.WriteByte('\n')
	case 'r', 'R':
		sb.WriteByte('\r')
	case 't', 'T':
		sb.WriteByte('\t')
	case 'u', 'U':
		// Like the openCypher grammar, take eight hex digits when they are
		// present, otherwise four; the letter's case does not matter.
		if r, ok := hexRune(s, i+2, 8); ok {
			if r > unicode.MaxRune || utf16.IsSurrogate(r) {
				return 0, l.errorf(p, s[i:i+10], "invalid escape: not a Unicode code point")
			}
			sb.WriteRune(r)
			return 10, nil
		}
		r, ok := hexRune(s, i+2, 4)
		if !ok {
			return 0, l.errorf(p, s[i:min(i+6, len(s))], "invalid \\u escape: expected 4 or 8 hex digits")
		}
		if utf16.IsSurrogate(r) {
			// A high surrogate must be followed by \uXXXX holding the low half.
			if r < 0xDC00 && i+7 < len(s) && s[i+6] == '\\' && (s[i+7] == 'u' || s[i+7] == 'U') {
				if lo, ok := hexRune(s, i+8, 4); ok {
					if pair := utf16.DecodeRune(r, lo); pair != utf8.RuneError {
						sb.WriteRune(pair)
						return 12, nil
					}
				}
			}
			return 0, l.errorf(p, s[i:i+6], "invalid \\u escape: unpaired UTF-16 surrogate")
		}
		sb.WriteRune(r)
		return 6, nil
	default:
		r, _ := utf8.DecodeRuneInString(s[i+1:])
		return 0, l.errorf(p, "\\"+string(r), "invalid escape sequence in string literal")
	}
	return 2, nil
}

// hexRune parses exactly n hex digits at s[i:].
func hexRune(s string, i, n int) (rune, bool) {
	if i+n > len(s) {
		return 0, false
	}
	var r rune
	for j := i; j < i+n; j++ {
		c := s[j]
		var d byte
		switch {
		case '0' <= c && c <= '9':
			d = c - '0'
		case 'a' <= c && c <= 'f':
			d = c - 'a' + 10
		case 'A' <= c && c <= 'F':
			d = c - 'A' + 10
		default:
			return 0, false
		}
		r = r<<4 | rune(d)
	}
	return r, true
}
