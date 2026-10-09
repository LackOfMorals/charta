package syntax

import (
	"errors"
	"strings"
	"testing"
)

func sxTypes(n Node) (string, bool) {
	switch n := n.(type) {
	case *TypePredicate:
		op := "::"
		if n.Negated {
			op = "!::"
		}
		return "(" + op + " " + sx(n.X) + " " + n.Type + ")", true
	case *Normalized:
		s := "(normalized " + sx(n.X)
		if n.Form != "" {
			s += " " + n.Form
		}
		if n.Negated {
			s += " NOT"
		}
		return s + ")", true
	case *MapProjection:
		parts := []string{sx(n.Subject)}
		for _, it := range n.Items {
			switch it.Kind {
			case ProjProperty:
				parts = append(parts, "."+it.Key)
			case ProjAll:
				parts = append(parts, ".*")
			case ProjVariable:
				parts = append(parts, it.Key)
			case ProjLiteral:
				parts = append(parts, it.Key+":"+sx(it.Value))
			}
		}
		return "(proj " + strings.Join(parts, " ") + ")", true
	}
	return "", false
}

func TestParseExpr_TypePredicates(t *testing.T) {
	tests := []struct{ src, want string }{
		{"x IS :: INTEGER", "(:: x INTEGER)"},
		{"x IS NOT :: STRING", "(!:: x STRING)"},
		{"x IS TYPED BOOLEAN", "(:: x BOOLEAN)"},
		{"x IS NOT TYPED FLOAT", "(!:: x FLOAT)"},
		{"x :: DATE", "(:: x DATE)"},
		{"x is :: integer", "(:: x INTEGER)"},
		{"x IS :: INTEGER NOT NULL", "(:: x INTEGER NOT NULL)"},
		{"x IS :: LIST<STRING NOT NULL>", "(:: x LIST<STRING NOT NULL>)"},
		{"x IS :: LIST<LIST<INTEGER>>", "(:: x LIST<LIST<INTEGER>>)"},
		{"x IS :: INTEGER | FLOAT", "(:: x INTEGER | FLOAT)"},
		{"x IS :: STRING | LIST<STRING NOT NULL>", "(:: x STRING | LIST<STRING NOT NULL>)"},
		{"x IS :: VECTOR<FLOAT32>(1024)", "(:: x VECTOR<FLOAT32>(1024))"},
		{"x IS :: VECTOR(3)", "(:: x VECTOR(3))"},
		{"x IS :: VECTOR", "(:: x VECTOR)"},
		{"x IS :: ZONED DATETIME", "(:: x ZONED DATETIME)"},
		{"x IS :: LOCAL TIME", "(:: x LOCAL TIME)"},
		{"x IS :: TIMESTAMP WITH TIME ZONE", "(:: x TIMESTAMP WITH TIME ZONE)"},
		{"x IS :: TIME WITHOUT TIMEZONE", "(:: x TIME WITHOUT TIMEZONE)"},
		{"x IS :: PROPERTY VALUE", "(:: x PROPERTY VALUE)"},
		{"x IS :: ANY", "(:: x ANY)"},
		{"x IS :: NOTHING", "(:: x NOTHING)"},
		{"x IS :: NULL", "(:: x NULL)"},
		{"x IS :: SIGNED INTEGER", "(:: x SIGNED INTEGER)"},
		// the predicate stops where the type ends
		{"n.a IS :: INTEGER AND n.b IS NOT :: STRING", "(AND (:: (. n a) INTEGER) (!:: (. n b) STRING))"},
		{"x IS :: INTEGER OR y", "(OR (:: x INTEGER) y)"},
		{"NOT x IS :: INTEGER", "(NOT (:: x INTEGER))"},
		{"[x IN l WHERE x :: INTEGER | x]", "(listcomp x IN l WHERE (:: x INTEGER) | x)"},
		// unaffected forms
		{"x IS NULL", "(isnull x)"},
		{"x IS NOT NULL", "(isnotnull x)"},
		// IS NORMALIZED
		{"s IS NORMALIZED", "(normalized s)"},
		{"s IS NOT NORMALIZED", "(normalized s NOT)"},
		{"s IS NFC NORMALIZED", "(normalized s NFC)"},
		{"s IS NOT NFKD NORMALIZED", "(normalized s NFKD NOT)"},
	}
	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			e, err := ParseExpr(tc.src)
			if err != nil {
				t.Fatalf("ParseExpr(%q): %v", tc.src, err)
			}
			if got := sx(e); got != tc.want {
				t.Errorf("ParseExpr(%q)\n got %s\nwant %s", tc.src, got, tc.want)
			}
		})
	}
}

func TestParseExpr_MapProjection(t *testing.T) {
	tests := []struct{ src, want string }{
		{"n{.name, .age}", "(proj n .name .age)"},
		{"n {.name}", "(proj n .name)"},
		{"n{.*}", "(proj n .*)"},
		{"n{}", "(proj n)"},
		{"n{.name, key: n.x + 1, other}", "(proj n .name key:(+ (. n x) 1) other)"},
		{"n{.a, p: m{.b}}", "(proj n .a p:(proj m .b))"},
		{"n{.a}.a", "(. (proj n .a) a)"},
		{"n{.a}[0]", "(idx (proj n .a) 0)"},
		{"n{.match, .set}", "(proj n .match .set)"},
		{"keanu{.name, dob, birthPlace}", "(proj keanu .name dob birthPlace)"},
		{"n{.a, k: $p}", "(proj n .a k:$p)"},
		// an ordinary map literal and a node pattern are unaffected
		{"{a: 1}", "{a:1}"},
		{"[n{.a}, m{.b}]", "[(proj n .a) (proj m .b)]"},
	}
	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			e, err := ParseExpr(tc.src)
			if err != nil {
				t.Fatalf("ParseExpr(%q): %v", tc.src, err)
			}
			if got := sx(e); got != tc.want {
				t.Errorf("ParseExpr(%q)\n got %s\nwant %s", tc.src, got, tc.want)
			}
		})
	}
}

func TestParse_DynamicLabelsAndProperties(t *testing.T) {
	tests := []struct{ src, want string }{
		{"MATCH (n) SET n[k + 'Copy'] = n[k]", `MATCH (n) SET (idx n (+ k "Copy"))=(idx n k)`},
		{"MATCH (n) SET n[$k] = 1", "MATCH (n) SET (idx n $k)=1"},
		{"MATCH (n) SET n:$(n.name)", "MATCH (n) SET n:$((. n name))"},
		{"MATCH (n) SET n:$($label)", "MATCH (n) SET n:$($label)"},
		{"MATCH (n) SET n:$(labels)", "MATCH (n) SET n:$(labels)"},
		{"MATCH (n) SET n:A:$(x)", "MATCH (n) SET n:(& A $(x))"},
		{"MATCH (n) REMOVE n:$(x)", "MATCH (n) REMOVE n:$(x)"},
		{"MATCH (n) REMOVE n[$k]", "MATCH (n) REMOVE (idx n $k)"},
		{"CREATE (n:$(x) {a: 1})", "CREATE (n:$(x) {a:1})"},
		{"MATCH (a)-[r:$(t)]->(b) RETURN a", "MATCH (a)-[r:$(t)]->(b) RETURN a"},
		{"MATCH (n) WHERE n:$(x) RETURN n", "MATCH (n) WHERE (: n $(x)) RETURN n"},
		{"MATCH (n) RETURN n[$k] AS v, n{.a} AS m", "MATCH (n) RETURN (idx n $k) AS v, (proj n .a) AS m"},
	}
	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			st := parseOK(t, tc.src)
			if got := sx(st); got != tc.want {
				t.Errorf("Parse(%q)\n got %s\nwant %s", tc.src, got, tc.want)
			}
		})
	}
}

func TestParseExpr_TypeAndProjectionErrors(t *testing.T) {
	tests := []struct {
		name, src string
		line, col int
		msg       string
	}{
		{"type missing", "x IS ::", 1, 8, "end of input"},
		{"unknown type", "x IS :: FOO", 1, 9, "unexpected token"},
		{"empty list type", "x IS :: LIST<>", 1, 15, "end of input"}, // <> lexes as one token
		{"unclosed list type", "x IS :: LIST<INTEGER", 1, 21, "end of input"},
		{"is followed by junk", "x IS 1", 1, 6, "unexpected token"},
		{"typed missing type", "x IS TYPED", 1, 11, "end of input"},
		{"projection trailing comma", "n{.a,}", 1, 6, "unexpected token"},
		{"projection bare dot", "n{.}", 1, 4, "unexpected token"},
		{"projection literal element", "n{1}", 1, 3, "unexpected token"},
		{"projection unclosed", "n{.a", 1, 5, "end of input"},
		{"projection on property", "a.b{.x}", 1, 4, "unexpected token"},
		{"projection missing value", "n{k:}", 1, 5, "unexpected token"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseExpr(tc.src)
			var se *SyntaxError
			if !errors.As(err, &se) {
				t.Fatalf("error = %v, want *SyntaxError", err)
			}
			if se.Pos.Line != tc.line || se.Pos.Col != tc.col {
				t.Errorf("error at %d:%d, want %d:%d: %v", se.Pos.Line, se.Pos.Col, tc.line, tc.col, err)
			}
			if !strings.Contains(se.Error(), tc.msg) {
				t.Errorf("error %q does not contain %q", se.Error(), tc.msg)
			}
		})
	}
}
