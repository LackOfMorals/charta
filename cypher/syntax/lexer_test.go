package syntax

import (
	"errors"
	"strings"
	"testing"
)

const emptyValue = "\x00"

type tk struct {
	kind  Kind
	text  string
	value string // empty means "same as text"; emptyValue means ""
}

func lexAll(t *testing.T, src string) []Token {
	t.Helper()
	toks, err := Tokenize(src)
	if err != nil {
		t.Fatalf("Tokenize(%q) error: %v", src, err)
	}
	if n := len(toks); n == 0 || toks[n-1].Kind != EOF {
		t.Fatalf("Tokenize(%q): missing trailing EOF: %v", src, toks)
	}
	return toks[:len(toks)-1]
}

func TestLexer_Tokens(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []tk
	}{
		{"punctuation", "()[]{},.:;|&!%^*/+-=", []tk{
			{LPAREN, "(", ""}, {RPAREN, ")", ""}, {LBRACK, "[", ""}, {RBRACK, "]", ""},
			{LBRACE, "{", ""}, {RBRACE, "}", ""}, {COMMA, ",", ""}, {DOT, ".", ""},
			{COLON, ":", ""}, {SEMI, ";", ""}, {PIPE, "|", ""}, {AMP, "&", ""},
			{BANG, "!", ""}, {PERCENT, "%", ""}, {CARET, "^", ""}, {STAR, "*", ""},
			{SLASH, "/", ""}, {PLUS, "+", ""}, {MINUS, "-", ""}, {EQ, "=", ""},
		}},
		{"multi-char operators", ".. :: || <> <= >= =~ +=", []tk{
			{DOTDOT, "..", ""}, {DOUBLECOLON, "::", ""}, {PIPEPIPE, "||", ""},
			{NEQ, "<>", ""}, {LTE, "<=", ""}, {GTE, ">=", ""}, {EQTILDE, "=~", ""},
			{PLUSEQ, "+=", ""},
		}},
		{"arrows are separate tokens", "(a)<-[r]->(b)<--(c)", []tk{
			{LPAREN, "(", ""}, {IDENT, "a", ""}, {RPAREN, ")", ""}, {LT, "<", ""},
			{MINUS, "-", ""}, {LBRACK, "[", ""}, {IDENT, "r", ""}, {RBRACK, "]", ""},
			{MINUS, "-", ""}, {GT, ">", ""}, {LPAREN, "(", ""}, {IDENT, "b", ""},
			{RPAREN, ")", ""}, {LT, "<", ""}, {MINUS, "-", ""}, {MINUS, "-", ""},
			{LPAREN, "(", ""}, {IDENT, "c", ""}, {RPAREN, ")", ""},
		}},
		{"identifiers", "foo _bar baz9 Ünï `a b` `x``y`", []tk{
			{IDENT, "foo", ""}, {IDENT, "_bar", ""}, {IDENT, "baz9", ""}, {IDENT, "Ünï", ""},
			{IDENT, "`a b`", "a b"}, {IDENT, "`x``y`", "x`y"},
		}},
		{"parameters", "$name $0 $`odd name` $_x", []tk{
			{PARAM, "$name", "name"}, {PARAM, "$0", "0"},
			{PARAM, "$`odd name`", "odd name"}, {PARAM, "$_x", "_x"},
		}},
		{"dynamic dollar", "$(x)", []tk{
			{DOLLAR, "$", ""}, {LPAREN, "(", ""}, {IDENT, "x", ""}, {RPAREN, ")", ""},
		}},
		{"integers", "0 7 42 0x1F 0XaB 0o17 017", []tk{
			{INT, "0", ""}, {INT, "7", ""}, {INT, "42", ""}, {INT, "0x1F", ""},
			{INT, "0XaB", ""}, {INT, "0o17", ""}, {INT, "017", ""},
		}},
		{"floats", "1.5 .5 1e3 1E-3 2.5e+10 .5e2", []tk{
			{FLOAT, "1.5", ""}, {FLOAT, ".5", ""}, {FLOAT, "1e3", ""}, {FLOAT, "1E-3", ""},
			{FLOAT, "2.5e+10", ""}, {FLOAT, ".5e2", ""},
		}},
		{"trailing dot is not a float", "1.", []tk{{INT, "1", ""}, {DOT, ".", ""}}},
		{"var-length range", "[*1..3]", []tk{
			{LBRACK, "[", ""}, {STAR, "*", ""}, {INT, "1", ""}, {DOTDOT, "..", ""},
			{INT, "3", ""}, {RBRACK, "]", ""},
		}},
		{"open-ended ranges", "[*..3] [*2..]", []tk{
			{LBRACK, "[", ""}, {STAR, "*", ""}, {DOTDOT, "..", ""}, {INT, "3", ""}, {RBRACK, "]", ""},
			{LBRACK, "[", ""}, {STAR, "*", ""}, {INT, "2", ""}, {DOTDOT, "..", ""}, {RBRACK, "]", ""},
		}},
		{"property after integer slice", "x[1..2]", []tk{
			{IDENT, "x", ""}, {LBRACK, "[", ""}, {INT, "1", ""}, {DOTDOT, "..", ""},
			{INT, "2", ""}, {RBRACK, "]", ""},
		}},
		{"strings", `'a' "b" '' 'it''s'`, []tk{
			{STRING, `'a'`, "a"}, {STRING, `"b"`, "b"}, {STRING, `''`, emptyValue},
			{STRING, `'it'`, "it"}, {STRING, `'s'`, "s"}, // '' is not an escape in Cypher
		}},
		{"string escapes", `'\n\t\\\'\"\b\f\ré\U0001F600😀'`, []tk{
			{STRING, `'\n\t\\\'\"\b\f\ré\U0001F600😀'`,
				"\n\t\\'\"\b\f\ré\U0001F600\U0001F600"},
		}},
		{"string with newline and unicode", "'a\nü'", []tk{{STRING, "'a\nü'", "a\nü"}}},
		{"line comment", "a // c\nb", []tk{{IDENT, "a", ""}, {IDENT, "b", ""}}},
		{"block comment", "a /* c\n * d */ b", []tk{{IDENT, "a", ""}, {IDENT, "b", ""}}},
		{"division is not a comment", "a / b", []tk{{IDENT, "a", ""}, {SLASH, "/", ""}, {IDENT, "b", ""}}},
		{"unicode whitespace", "a b c", []tk{{IDENT, "a", ""}, {IDENT, "b", ""}, {IDENT, "c", ""}}},
		{"empty", "", nil},
		{"only whitespace and comments", "  // x\n /* y */ ", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := lexAll(t, tc.src)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d tokens, want %d: %+v", len(got), len(tc.want), got)
			}
			for i, w := range tc.want {
				g := got[i]
				wantValue := w.value
				switch wantValue {
				case "":
					wantValue = w.text
				case emptyValue:
					wantValue = ""
				}
				if g.Kind != w.kind || g.Text != w.text || g.Value != wantValue {
					t.Errorf("token %d: got {%v %q %q}, want {%v %q %q}",
						i, g.Kind, g.Text, g.Value, w.kind, w.text, wantValue)
				}
			}
		})
	}
}

func TestLexer_Keywords(t *testing.T) {
	toks := lexAll(t, "match MATCH Match `match` foo returnx")
	wantKw := []Keyword{KwMatch, KwMatch, KwMatch, "", "", ""}
	for i, kw := range wantKw {
		if toks[i].Keyword != kw {
			t.Errorf("token %d (%q): keyword %q, want %q", i, toks[i].Text, toks[i].Keyword, kw)
		}
	}
	if !toks[0].Is(KwMatch) || toks[3].Is(KwMatch) {
		t.Error("Token.Is should be true for unquoted keywords only")
	}
}

func TestLookupKeyword(t *testing.T) {
	for _, kw := range allKeywords {
		for _, spelling := range []string{string(kw), strings.ToLower(string(kw))} {
			got, ok := LookupKeyword(spelling)
			if !ok || got != kw {
				t.Errorf("LookupKeyword(%q) = %q, %v", spelling, got, ok)
			}
		}
	}
	for _, w := range []string{"", "x", "notakeyword", "MATCHES", "ÄLL", strings.Repeat("A", 40)} {
		if _, ok := LookupKeyword(w); ok {
			t.Errorf("LookupKeyword(%q) unexpectedly succeeded", w)
		}
	}
	if n := testing.AllocsPerRun(100, func() { LookupKeyword("optional") }); n != 0 {
		t.Errorf("LookupKeyword allocated %v times", n)
	}
}

func TestLexer_Positions(t *testing.T) {
	src := "MATCH (n)\n  RETURN 'é' + n.nämé\n\t/* x\ny */ z"
	toks := lexAll(t, src)
	type p struct {
		text      string
		line, col int
	}
	want := []p{
		{"MATCH", 1, 1}, {"(", 1, 7}, {"n", 1, 8}, {")", 1, 9},
		{"RETURN", 2, 3}, {"'é'", 2, 10}, {"+", 2, 14}, {"n", 2, 16}, {".", 2, 17}, {"nämé", 2, 18},
		{"z", 4, 6},
	}
	if len(toks) != len(want) {
		t.Fatalf("got %d tokens, want %d: %+v", len(toks), len(want), toks)
	}
	for i, w := range want {
		g := toks[i]
		if g.Text != w.text || g.Pos.Line != w.line || g.Pos.Col != w.col {
			t.Errorf("token %d: got %q at %d:%d, want %q at %d:%d",
				i, g.Text, g.Pos.Line, g.Pos.Col, w.text, w.line, w.col)
		}
		if src[g.Pos.Offset:g.End()] != g.Text {
			t.Errorf("token %d: offset %d does not index %q", i, g.Pos.Offset, g.Text)
		}
	}
}

func TestLexer_EOFPosition(t *testing.T) {
	toks, err := Tokenize("a\nbc")
	if err != nil {
		t.Fatal(err)
	}
	eof := toks[len(toks)-1]
	if eof.Kind != EOF || eof.Pos != (Pos{Offset: 4, Line: 2, Col: 3}) {
		t.Errorf("EOF token = %+v", eof)
	}
}

func TestLexer_Errors(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		line, col int
		msg       string
	}{
		{"unterminated string", "RETURN 'abc", 1, 8, "unterminated string"},
		{"unterminated double-quoted", `"abc`, 1, 1, "unterminated string"},
		{"unterminated multi-line string", "RETURN\n  'abc\ndef", 2, 3, "unterminated string"},
		{"unterminated quoted ident", "MATCH (`a", 1, 8, "unterminated quoted identifier"},
		{"unterminated block comment", "a\n  /* never", 2, 3, "unterminated block comment"},
		{"bad escape", `'ab\q'`, 1, 4, "invalid escape"},
		{"bad escape after newline", "'a\n\\q'", 2, 1, "invalid escape"},
		{"short \\u", `'\u12'`, 1, 2, "\\u escape"},
		{"non-hex \\u", `'\u12G4'`, 1, 2, "\\u escape"},
		{"lone high surrogate", `'\uD83D'`, 1, 2, "surrogate"},
		{"lone low surrogate", `'\uDE00'`, 1, 2, "surrogate"},
		{"\\U out of range", `'\U00110000'`, 1, 2, "code point"},
		{"short \\U", `'\U0001'`, 1, 2, "\\U escape"},
		{"trailing backslash", `'abc\`, 1, 5, "unterminated string"},
		{"bad hex", "0x", 1, 1, "hexadecimal"},
		{"bad octal", "0o9", 1, 1, "octal"},
		{"bad exponent", "1e", 1, 1, "exponent"},
		{"bad exponent sign", "1e+", 1, 1, "exponent"},
		{"identifier glued to number", "12abc", 1, 1, "invalid numeric"},
		{"hex glued to letter", "0x1G", 1, 1, "invalid numeric"},
		{"unexpected char", "a @ b", 1, 3, "unexpected character"},
		{"unexpected unicode char", "a € b", 1, 3, "unexpected character"},
		{"invalid utf8", "a \xff b", 1, 3, "UTF-8"},
		{"invalid utf8 in string", "'a\xffb'", 1, 3, "UTF-8"},
		{"invalid utf8 in quoted ident", "`a\xffb`", 1, 1, "UTF-8"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Tokenize(tc.src)
			var se *SyntaxError
			if !errors.As(err, &se) {
				t.Fatalf("Tokenize(%q) error = %v, want *SyntaxError", tc.src, err)
			}
			if se.Pos.Line != tc.line || se.Pos.Col != tc.col {
				t.Errorf("error at %d:%d, want %d:%d (%v)", se.Pos.Line, se.Pos.Col, tc.line, tc.col, err)
			}
			if !strings.Contains(se.Msg, tc.msg) {
				t.Errorf("message %q does not contain %q", se.Msg, tc.msg)
			}
		})
	}
}

func TestSyntaxError_Error(t *testing.T) {
	e := &SyntaxError{Pos: Pos{Line: 3, Col: 7}, Found: "RETURN", Expected: []string{"')'", "','"}, Msg: "unexpected token"}
	want := `syntax error at line 3, column 7: unexpected token (found "RETURN", expected ')', ',')`
	if got := e.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if got := (&SyntaxError{Pos: Pos{Line: 1, Col: 1}, Msg: "boom"}).Error(); got != "syntax error at line 1, column 1: boom" {
		t.Errorf("Error() = %q", got)
	}
}

func TestLexer_EOFIsSticky(t *testing.T) {
	l := NewLexer("a")
	for i := 0; i < 4; i++ {
		tok, err := l.Next()
		if err != nil {
			t.Fatal(err)
		}
		if i >= 1 && tok.Kind != EOF {
			t.Fatalf("call %d: got %v, want EOF", i, tok.Kind)
		}
	}
}
