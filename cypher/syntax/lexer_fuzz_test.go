package syntax

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

// lineCol independently recomputes the 1-based line and code-point column of
// byte offset off, to cross-check the lexer's incremental bookkeeping.
func lineCol(src string, off int) (line, col int) {
	line, col = 1, 1
	for i := 0; i < off; {
		if src[i] == '\n' {
			line++
			col = 1
			i++
			continue
		}
		_, w := utf8.DecodeRuneInString(src[i:])
		col++
		i += w
	}
	return line, col
}

var fuzzSeeds = []string{
	"", " ", "MATCH (n) RETURN n", "a // c", "a /* c */ b", "/*", "'", `"`, "`", "$", "$(",
	"1..3", "1.", ".5", "1e", "0x", "0o", "\xff", "'\\u", "'\\uD83D\\uDE00'", "'\\U0001F600'",
	"[*1..3]", "(a)<-[:R]-(b)", "x::INTEGER", "a||b", "n += {a: 1}", "`a``b`", "$`a b`",
	"RETURN 'it''s'", "\u00a0\ufeff", "a\r\nb", "é", "€",
}

func FuzzLex(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		toks, err := Tokenize(src)

		if err != nil {
			var se *SyntaxError
			if !errors.As(err, &se) {
				t.Fatalf("error is %T, want *SyntaxError", err)
			}
			if se.Pos.Offset < 0 || se.Pos.Offset > len(src) {
				t.Fatalf("error offset %d out of range [0,%d]", se.Pos.Offset, len(src))
			}
			if l, c := lineCol(src, se.Pos.Offset); l != se.Pos.Line || c != se.Pos.Col {
				t.Fatalf("error pos %d:%d does not match offset %d (%d:%d)",
					se.Pos.Line, se.Pos.Col, se.Pos.Offset, l, c)
			}
			if se.Msg == "" {
				t.Fatal("empty error message")
			}
			return
		}

		if len(toks) == 0 || toks[len(toks)-1].Kind != EOF {
			t.Fatalf("tokens do not end with EOF: %v", toks)
		}
		prevEnd := 0
		for i, tok := range toks {
			if tok.Pos.Offset < prevEnd {
				t.Fatalf("token %d overlaps or is out of order (offset %d < %d)", i, tok.Pos.Offset, prevEnd)
			}
			if tok.End() > len(src) || src[tok.Pos.Offset:tok.End()] != tok.Text {
				t.Fatalf("token %d text %q does not match source at offset %d", i, tok.Text, tok.Pos.Offset)
			}
			if l, c := lineCol(src, tok.Pos.Offset); l != tok.Pos.Line || c != tok.Pos.Col {
				t.Fatalf("token %d pos %d:%d, recomputed %d:%d", i, tok.Pos.Line, tok.Pos.Col, l, c)
			}
			if tok.Kind != EOF && tok.Text == "" {
				t.Fatalf("token %d (%v) has empty text", i, tok.Kind)
			}
			if tok.Kind == EOF && i != len(toks)-1 {
				t.Fatalf("EOF at index %d of %d", i, len(toks))
			}
			// Everything between tokens must be whitespace or comments.
			gap := src[prevEnd:tok.Pos.Offset]
			if strings.TrimSpace(stripComments(gap)) != "" && !onlyUnicodeSpace(stripComments(gap)) {
				t.Fatalf("non-trivia %q skipped before token %d", gap, i)
			}
			prevEnd = tok.End()
		}
		if toks[len(toks)-1].Pos.Offset != len(src) {
			t.Fatalf("EOF offset %d, want %d", toks[len(toks)-1].Pos.Offset, len(src))
		}
	})
}

// stripComments removes // and /* */ comments from a gap between tokens.
func stripComments(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "//"):
			j := strings.IndexByte(s[i:], '\n')
			if j < 0 {
				return b.String()
			}
			i += j
		case strings.HasPrefix(s[i:], "/*"):
			j := strings.Index(s[i+2:], "*/")
			if j < 0 {
				return b.String()
			}
			i += 2 + j + 2
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}

func onlyUnicodeSpace(s string) bool {
	for _, r := range s {
		if r != 0xFEFF && !strings.ContainsRune(" \t\n\r\v\f\u00a0\u0085\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000", r) {
			return false
		}
	}
	return true
}

const benchQuery = `MATCH (a:Person {name: $n})-[:KNOWS]->(b) WHERE b.age > 30 AND a.x = 1 RETURN b.name, count(*) ORDER BY b.name LIMIT 10`

func BenchmarkTokenize(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchQuery)))
	for i := 0; i < b.N; i++ {
		if _, err := Tokenize(benchQuery); err != nil {
			b.Fatal(err)
		}
	}
}
