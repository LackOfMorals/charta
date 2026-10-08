package syntax

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// sxMore renders pattern and comprehension nodes for tests.
func sxMore(n Node) (string, bool) {
	switch n := n.(type) {
	case *PatternExpr:
		return "(pattern " + sx(n.Part) + ")", true
	case *PatternPart:
		var b strings.Builder
		if n.Var != "" {
			b.WriteString(n.Var + "=")
		}
		switch n.Func {
		case FuncShortestPath:
			b.WriteString("shortestPath[")
		case FuncAllShortestPaths:
			b.WriteString("allShortestPaths[")
		}
		for _, e := range n.Elems {
			b.WriteString(sx(e))
		}
		if n.Func != FuncNone {
			b.WriteString("]")
		}
		return b.String(), true
	case *NodePattern:
		s := "(" + n.Var
		if n.Labels != nil {
			s += ":" + sx(n.Labels)
		}
		if n.Props != nil {
			s += " " + sx(n.Props)
		}
		if n.Where != nil {
			s += " WHERE " + sx(n.Where)
		}
		return s + ")", true
	case *RelPattern:
		var d string
		if n.Var != "" {
			d += n.Var
		}
		if n.Types != nil {
			d += ":" + sx(n.Types)
		}
		if n.Range != nil {
			d += "*"
			bound := func(b *int64) string {
				if b == nil {
					return ""
				}
				return strconv.FormatInt(*b, 10)
			}
			switch {
			case n.Range.Min != nil && n.Range.Max != nil && *n.Range.Min == *n.Range.Max:
				d += bound(n.Range.Min)
			case n.Range.Min != nil || n.Range.Max != nil:
				d += bound(n.Range.Min) + ".." + bound(n.Range.Max)
			}
		}
		if n.Props != nil {
			d += " " + sx(n.Props)
		}
		if n.Where != nil {
			d += " WHERE " + sx(n.Where)
		}
		body := "-"
		if d != "" {
			body = "-[" + d + "]-"
		} else {
			body = "--"
		}
		switch n.Dir {
		case DirRight:
			body += ">"
		case DirLeft:
			body = "<" + body
		case DirBoth:
			body = "<" + body + ">"
		}
		return body, true
	case *ListComp:
		return "(listcomp " + n.Var + " IN " + sx(n.In) + opt(" WHERE ", n.Where) + opt(" | ", n.Proj) + ")", true
	case *PatternComp:
		return "(patcomp " + sx(n.Pattern) + opt(" WHERE ", n.Where) + " | " + sx(n.Proj) + ")", true
	case *Quantifier:
		return "(" + string(n.Kind) + " " + n.Var + " IN " + sx(n.In) + opt(" WHERE ", n.Where) + ")", true
	case *Reduce:
		return "(reduce " + n.Acc + " " + sx(n.Init) + " " + n.Var + " IN " + sx(n.In) + " | " + sx(n.Expr) + ")", true
	}
	return "", false
}

func opt(prefix string, e Expr) string {
	if e == nil {
		return ""
	}
	return prefix + sx(e)
}

func TestParseExpr_Patterns(t *testing.T) {
	tests := []struct{ src, want string }{
		// directions
		{"(a)-->(b)", "(pattern (a)-->(b))"},
		{"(a)<--(b)", "(pattern (a)<--(b))"},
		{"(a)--(b)", "(pattern (a)--(b))"},
		{"(a)<-->(b)", "(pattern (a)<-->(b))"},
		{"(a)-[r]->(b)", "(pattern (a)-[r]->(b))"},
		{"(a)<-[r:T]-(b)", "(pattern (a)<-[r:T]-(b))"},
		{"(a)-[]-(b)", "(pattern (a)--(b))"},
		{"(a)<-[:T]->(b)", "(pattern (a)<-[:T]->(b))"},
		// relationship detail
		{"(a)-[:T|U]-(b)", "(pattern (a)-[:(| T U)]-(b))"},
		{"(a)-[:T|:U]-(b)", "(pattern (a)-[:(| T U)]-(b))"},
		{"(a)-[r:T {x: 1}]->(b)", "(pattern (a)-[r:T {x:1}]->(b))"},
		{"(a)-[r $props]->(b)", "(pattern (a)-[r $props]->(b))"},
		{"(a)-[r WHERE r.x > 1]->(b)", "(pattern (a)-[r WHERE (chain (. r x) > 1)]->(b))"},
		{"(a)-[match]->(b)", "(pattern (a)-[match]->(b))"},
		// variable-length
		{"(a)-[*]->(b)", "(pattern (a)-[*]->(b))"},
		{"(a)-[*2]->(b)", "(pattern (a)-[*2]->(b))"},
		{"(a)-[*2..]->(b)", "(pattern (a)-[*2..]->(b))"},
		{"(a)-[*..3]->(b)", "(pattern (a)-[*..3]->(b))"},
		{"(a)-[*1..3]->(b)", "(pattern (a)-[*1..3]->(b))"},
		{"(a)-[*0..]->(b)", "(pattern (a)-[*0..]->(b))"},
		{"(a)-[r:T*1..3 {x: 1}]->(b)", "(pattern (a)-[r:T*1..3 {x:1}]->(b))"},
		{"(a)-[:T*2]->(b)", "(pattern (a)-[:T*2]->(b))"},
		// chains
		{"(a)-->(b)<--(c)--(d)", "(pattern (a)-->(b)<--(c)--(d))"},
		{"(a)-[:R]->(b)-[:S]->(c)", "(pattern (a)-[:R]->(b)-[:S]->(c))"},
		// node detail
		{"()-->()", "(pattern ()-->())"},
		{"(:L)-->({x: 1})", "(pattern (:L)-->( {x:1}))"},
		{"(n:A:B)-->(m:A&B)", "(pattern (n:(& A B))-->(m:(& A B)))"},
		{"(n:A|B)-->(m:!A)", "(pattern (n:(| A B))-->(m:(! A)))"},
		{"(n $p)-->()", "(pattern (n $p)-->())"},
		{"(n WHERE n.x > 1)-->()", "(pattern (n WHERE (chain (. n x) > 1))-->())"},
		{"(n:L {a: 1} WHERE n.b)-->()", "(pattern (n:L {a:1} WHERE (. n b))-->())"},
		// shortest path functions
		{"shortestPath((a)-[*]-(b))", "(pattern shortestPath[(a)-[*]-(b)])"},
		{"allShortestPaths((a)-[*..5]-(b))", "(pattern allShortestPaths[(a)-[*..5]-(b)])"},
		{"SHORTESTPATH((a)-->(b))", "(pattern shortestPath[(a)-->(b)])"},
		// patterns inside larger expressions
		{"(a)-->(b) AND x", "(AND (pattern (a)-->(b)) x)"},
		{"NOT (a)-->(b)", "(NOT (pattern (a)-->(b)))"},
		{"f((a)-->(b))", "(call f (pattern (a)-->(b)))"},
		{"(a)-->(b) IS NOT NULL", "(isnotnull (pattern (a)-->(b)))"},

		// parentheses that are NOT patterns
		{"(a)", "a"},
		{"(a.b)", "(. a b)"},
		{"(a) - 1", "(- a 1)"},
		{"(a) - (b)", "(- a b)"},
		{"(a) < -1", "(chain a < -1)"},
		{"(a)-1", "(- a 1)"},
		{"(a + b) * c", "(* (+ a b) c)"},
		{"(n:A)", "(: n A)"}, // label predicate, not a pattern
		{"shortestPath(x)", "(call shortestPath x)"},
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

func TestParsePatternPart(t *testing.T) {
	tests := []struct{ src, want string }{
		{"(a)", "(a)"},
		{"p = (a)-->(b)", "p=(a)-->(b)"},
		{"p = shortestPath((a)-[*]-(b))", "p=shortestPath[(a)-[*]-(b)]"},
		{"allShortestPaths((a)-[*]-(b))", "allShortestPaths[(a)-[*]-(b)]"},
		{"(a:Person {name: 'x'})-[r:KNOWS*1..3]->(b)", `(a:Person {name:"x"})-[r:KNOWS*1..3]->(b)`},
	}
	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			p, err := newParser(tc.src)
			if err != nil {
				t.Fatal(err)
			}
			var part *PatternPart
			err = p.run(func() {
				part = p.parsePatternPart()
				if !p.at(EOF) {
					p.unexpected("end of input")
				}
			})
			if err != nil {
				t.Fatalf("parse %q: %v", tc.src, err)
			}
			if got := sx(part); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestParseExpr_PatternRanges(t *testing.T) {
	e, err := ParseExpr("(a)-[*..3]->(b)")
	if err != nil {
		t.Fatal(err)
	}
	r := e.(*PatternExpr).Part.Elems[1].(*RelPattern).Range
	if r.Min != nil || r.Max == nil || *r.Max != 3 {
		t.Errorf("*..3: Min=%v Max=%v", r.Min, r.Max)
	}
	e, _ = ParseExpr("(a)-[*2]->(b)")
	r = e.(*PatternExpr).Part.Elems[1].(*RelPattern).Range
	if r.Min == nil || r.Max == nil || *r.Min != 2 || *r.Max != 2 {
		t.Errorf("*2: Min=%v Max=%v", r.Min, r.Max)
	}
	if r.Min == r.Max {
		t.Error("Min and Max must not alias the same variable")
	}
}

func TestParseExpr_Comprehensions(t *testing.T) {
	tests := []struct{ src, want string }{
		{"[x IN l]", "(listcomp x IN l)"},
		{"[x IN l WHERE x > 1]", "(listcomp x IN l WHERE (chain x > 1))"},
		{"[x IN l | x * 2]", "(listcomp x IN l | (* x 2))"},
		{"[x IN l WHERE x > 1 | x * 2]", "(listcomp x IN l WHERE (chain x > 1) | (* x 2))"},
		{"[x IN range(1, 3)]", "(listcomp x IN (call range 1 3))"},
		{"[x IN [1, 2, 3] | x]", "(listcomp x IN [1 2 3] | x)"},
		{"[x IN [y IN l | y] | x]", "(listcomp x IN (listcomp y IN l | y) | x)"},
		{"[(a)-->(b) | b.name]", "(patcomp (a)-->(b) | (. b name))"},
		{"[(a)-[:R]->(b) WHERE b.x > 1 | b.x]", "(patcomp (a)-[:R]->(b) WHERE (chain (. b x) > 1) | (. b x))"},
		{"[p = (a)-->(b) | p]", "(patcomp p=(a)-->(b) | p)"},
		{"[(a)-->(b) | [(b)-->(c) | c]]", "(patcomp (a)-->(b) | (patcomp (b)-->(c) | c))"},
		// brackets that are plain lists
		{"[(a)]", "[a]"},
		{"[(a)-->(b)]", "[(pattern (a)-->(b))]"},
		{"[(a)-->(b), 1]", "[(pattern (a)-->(b)) 1]"},
		{"[(a), (b)]", "[a b]"},
		{"[x, y]", "[x y]"},
		{"[x.in]", "[(. x in)]"},
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

func TestParseExpr_QuantifiersAndReduce(t *testing.T) {
	tests := []struct{ src, want string }{
		{"all(x IN l WHERE x > 1)", "(all x IN l WHERE (chain x > 1))"},
		{"ANY(x IN l WHERE x)", "(any x IN l WHERE x)"},
		{"none(x IN l WHERE x)", "(none x IN l WHERE x)"},
		{"single(x IN l WHERE x = 1)", "(single x IN l WHERE (chain x = 1))"},
		{"any(x IN l)", "(any x IN l)"},
		{"NOT all(x IN l WHERE x) AND y", "(AND (NOT (all x IN l WHERE x)) y)"},
		{"reduce(acc = 0, x IN l | acc + x)", "(reduce acc 0 x IN l | (+ acc x))"},
		{"REDUCE(s = '', x IN [1,2] | s || x)", `(reduce s "" x IN [1 2] | (|| s x))`},
		// not quantifiers/reduce: ordinary calls
		{"any(x)", "(call any x)"},
		{"all(a, b)", "(call all a b)"},
		{"reduce(a, b)", "(call reduce a b)"},
		{"`all`(x IN l)", "(call all (IN x l))"},
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

func TestParseExpr_PatternErrors(t *testing.T) {
	tests := []struct {
		name, src string
		line, col int
		msg       string
	}{
		{"unclosed relationship detail", "(a)-[", 1, 6, "end of input"},
		{"missing closing dash", "(a)-[r]>(b)", 1, 8, "unexpected token"},
		{"missing node after rel", "(a)-->", 1, 7, "end of input"},
		{"node expected after rel", "(a)-->b", 1, 7, "unexpected token"},
		{"empty type after colon", "(a)-[r:]->(b)", 1, 8, "unexpected token"},
		{"bad range", "(a)-[*1..2..3]->(b)", 1, 11, "unexpected token"},
		{"range bound too large", "(a)-[*99999999999999999999]->(b)", 1, 7, "too large"},
		{"unterminated node", "(a)-->(b", 1, 9, "end of input"},
		{"shortestPath missing close", "shortestPath((a)-[*]-(b)", 1, 25, "end of input"},
		{"list comp missing source", "[x IN]", 1, 6, "unexpected token"},
		{"list comp unterminated", "[x IN l WHERE x > 1", 1, 20, "end of input"},
		{"quantifier empty where", "all(x IN l WHERE)", 1, 17, "unexpected token"},
		{"reduce missing pipe", "reduce(a = 0, x IN l)", 1, 21, "unexpected token"},
		{"reduce missing comma", "reduce(a = 0 x IN l | a)", 1, 14, "unexpected token"},
		{"pattern comp missing projection", "[(a)-->(b) |]", 1, 13, "unexpected token"},
		{"pattern comp where without pipe", "[(a)-->(b) WHERE b.x]", 1, 21, "unexpected token"},
		{"deeply nested patterns", strings.Repeat("(a)-[r WHERE ", 500) + "1", 1, 0, "nested too deeply"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseExpr(tc.src)
			var se *SyntaxError
			if !errors.As(err, &se) {
				t.Fatalf("error = %v, want *SyntaxError", err)
			}
			if tc.col != 0 && (se.Pos.Line != tc.line || se.Pos.Col != tc.col) {
				t.Errorf("error at %d:%d, want %d:%d: %v", se.Pos.Line, se.Pos.Col, tc.line, tc.col, err)
			}
			if !strings.Contains(se.Error(), tc.msg) {
				t.Errorf("error %q does not contain %q", se.Error(), tc.msg)
			}
		})
	}
}

// Nested parentheses must not cause exponential re-parsing from the
// speculative pattern attempt at each '('.
func TestParseExpr_NestedParensAreLinear(t *testing.T) {
	src := strings.Repeat("(", 300) + "a" + strings.Repeat(")", 300)
	if _, err := ParseExpr(src); err != nil {
		t.Fatal(err)
	}
	src = strings.Repeat("(a WHERE ", 60) + "1" + strings.Repeat(")", 60)
	_, _ = ParseExpr(src) // must terminate promptly; result is an error or a value
	src = strings.Repeat("[(a)-->(b) | ", 40) + "1" + strings.Repeat("]", 40)
	if _, err := ParseExpr(src); err != nil {
		t.Fatal(err)
	}
}
