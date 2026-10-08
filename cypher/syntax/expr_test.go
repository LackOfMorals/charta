package syntax

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// sx renders an expression as a compact s-expression so precedence tests read
// like the tree they expect.
func sx(n Node) string {
	switch n := n.(type) {
	case nil:
		return "_"
	case *Ident:
		return n.Name
	case *IntLit:
		return n.Text
	case *FloatLit:
		return n.Text
	case *StringLit:
		return strconv.Quote(n.Value)
	case *BoolLit:
		return strconv.FormatBool(n.Value)
	case *NullLit:
		return "null"
	case *Param:
		return "$" + n.Name
	case *ListLit:
		return "[" + sxList(exprNodes(n.Elems)) + "]"
	case *MapLit:
		parts := make([]string, len(n.Entries))
		for i, e := range n.Entries {
			parts[i] = e.Key + ":" + sx(e.Value)
		}
		return "{" + strings.Join(parts, " ") + "}"
	case *Property:
		return "(. " + sx(n.Subject) + " " + n.Key + ")"
	case *Subscript:
		return "(idx " + sx(n.Subject) + " " + sx(n.Index) + ")"
	case *Slice:
		return "(slice " + sx(n.Subject) + " " + sx(n.From) + " " + sx(n.To) + ")"
	case *Unary:
		return "(" + map[UnaryOp]string{OpNot: "NOT", OpMinus: "neg", OpPlus: "pos"}[n.Op] + " " + sx(n.X) + ")"
	case *Binary:
		return "(" + string(n.Op) + " " + sx(n.L) + " " + sx(n.R) + ")"
	case *Comparison:
		var b strings.Builder
		b.WriteString("(chain " + sx(n.Operands[0]))
		for i, op := range n.Ops {
			b.WriteString(" " + string(op) + " " + sx(n.Operands[i+1]))
		}
		return b.String() + ")"
	case *IsNull:
		if n.Negated {
			return "(isnotnull " + sx(n.X) + ")"
		}
		return "(isnull " + sx(n.X) + ")"
	case *HasLabels:
		return "(: " + sx(n.X) + " " + sx(n.Labels) + ")"
	case *LabelName:
		if n.Dynamic != nil {
			return "$(" + sx(n.Dynamic) + ")"
		}
		return n.Name
	case *LabelWildcard:
		return "%"
	case *LabelNot:
		return "(! " + sx(n.X) + ")"
	case *LabelAnd:
		return "(& " + sx(n.L) + " " + sx(n.R) + ")"
	case *LabelOr:
		return "(| " + sx(n.L) + " " + sx(n.R) + ")"
	case *FuncCall:
		name := strings.Join(append(append([]string{}, n.Namespace...), n.Name), ".")
		parts := []string{"call", name}
		if n.Distinct {
			parts = append(parts, "distinct")
		}
		if n.Star {
			parts = append(parts, "*")
		}
		for _, a := range n.Args {
			parts = append(parts, sx(a))
		}
		return "(" + strings.Join(parts, " ") + ")"
	case *Case:
		parts := []string{"case"}
		if n.Subject != nil {
			parts = append(parts, sx(n.Subject))
		}
		for _, w := range n.Whens {
			parts = append(parts, "(when "+sx(w.Cond)+" "+sx(w.Then)+")")
		}
		if n.Else != nil {
			parts = append(parts, "(else "+sx(n.Else)+")")
		}
		return "(" + strings.Join(parts, " ") + ")"
	}
	if out, ok := sxMore(n); ok {
		return out
	}
	return Dump(n)
}

func exprNodes(es []Expr) []Node {
	ns := make([]Node, len(es))
	for i, e := range es {
		ns[i] = e
	}
	return ns
}

func sxList(ns []Node) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = sx(n)
	}
	return strings.Join(parts, " ")
}

func TestParseExpr_Golden(t *testing.T) {
	tests := []struct{ src, want string }{
		// literals and atoms
		{"1", "1"},
		{"0x1F", "0x1F"},
		{"1.5e3", "1.5e3"},
		{"'a\\nb'", `"a\nb"`},
		{"true", "true"},
		{"FALSE", "false"},
		{"Null", "null"},
		{"`true`", "true"}, // a quoted name is a variable, not the literal (checked below)
		{"$p", "$p"},
		{"$0", "$0"},
		{"[]", "[]"},
		{"[1, 'a', [2]]", `[1 "a" [2]]`},
		{"{}", "{}"},
		{"{a: 1, `b c`: $x, match: 2}", "{a:1 b c:$x match:2}"},
		{"(a)", "a"},
		{"((a))", "a"},

		// precedence, lowest to highest
		{"a OR b AND c", "(OR a (AND b c))"},
		{"a AND b OR c", "(OR (AND a b) c)"},
		{"a XOR b OR c", "(OR (XOR a b) c)"},
		{"a OR b XOR c", "(OR a (XOR b c))"},
		{"a XOR b AND c", "(XOR a (AND b c))"},
		{"a OR b OR c", "(OR (OR a b) c)"},
		{"NOT a AND b", "(AND (NOT a) b)"},
		{"NOT a = b", "(NOT (chain a = b))"},
		{"NOT NOT a", "(NOT (NOT a))"},
		{"a = b AND c <> d", "(AND (chain a = b) (chain c <> d))"},
		{"a < b <= c", "(chain a < b <= c)"},
		{"a < b + 1", "(chain a < (+ b 1))"},
		{"1 + 2 * 3", "(+ 1 (* 2 3))"},
		{"1 * 2 + 3", "(+ (* 1 2) 3)"},
		{"1 - 2 - 3", "(- (- 1 2) 3)"},
		{"8 / 4 % 3", "(% (/ 8 4) 3)"},
		{"a || b + c", "(+ (|| a b) c)"},
		{"2 * 3 ^ 2", "(* 2 (^ 3 2))"},
		{"2 ^ 3 ^ 2", "(^ (^ 2 3) 2)"}, // left-associative, per the grammar's flat list
		{"-x ^ 2", "(^ (neg x) 2)"},    // unary binds tighter than ^
		{"-2 ^ 2", "(^ -2 2)"},         // sign folded into the literal
		{"- 2", "- 2"},                 // literal text keeps the source spelling
		{"1 - -1", "(- 1 -1)"},
		{"1 -1", "(- 1 1)"},
		{"-a", "(neg a)"},
		{"+a", "(pos a)"},
		{"- -a", "(neg (neg a))"},
		{"-(1)", "(neg 1)"},
		{"-1.5", "-1.5"},
		{"-9223372036854775808", "-9223372036854775808"},
		{"a + b IN c", "(+ a (IN b c))"}, // string/list/null operators bind tighter than +
		{"a IN b IN c", "(IN (IN a b) c)"},

		// predicates
		{"a IS NULL", "(isnull a)"},
		{"a IS NOT NULL", "(isnotnull a)"},
		{"a.b IS NULL AND c", "(AND (isnull (. a b)) c)"},
		{"a STARTS WITH 'x'", `(STARTS WITH a "x")`},
		{"a ENDS WITH b", "(ENDS WITH a b)"},
		{"a CONTAINS b", "(CONTAINS a b)"},
		{"a =~ 'x.*'", `(=~ a "x.*")`},
		{"a IN [1, 2]", "(IN a [1 2])"},
		{"a IS NULL IS NOT NULL", "(isnotnull (isnull a))"},

		// postfix
		{"n.a", "(. n a)"},
		{"n.a.b", "(. (. n a) b)"},
		{"n.match.`x y`", "(. (. n match) x y)"},
		{"l[0]", "(idx l 0)"},
		{"l[1..2]", "(slice l 1 2)"},
		{"l[..2]", "(slice l _ 2)"},
		{"l[1..]", "(slice l 1 _)"},
		{"l[..]", "(slice l _ _)"},
		{"l[0].name", "(. (idx l 0) name)"},
		{"n.a[1][2]", "(idx (idx (. n a) 1) 2)"},
		{"n[$k]", "(idx n $k)"},
		{"{a: 1}.a", "(. {a:1} a)"},
		{"n:A", "(: n A)"},
		{"n:A:B", "(: n (& A B))"},
		{"n:A&B", "(: n (& A B))"},
		{"n:A|B", "(: n (| A B))"},
		{"n:A|:B", "(: n (| A B))"},
		{"n:A&B|C", "(: n (| (& A B) C))"},
		{"n:A&(B|C)", "(: n (& A (| B C)))"},
		{"n:!A", "(: n (! A))"},
		{"n:!!A", "(: n (! (! A)))"},
		{"n:%", "(: n %)"},
		{"n:$(x)", "(: n $(x))"},
		{"n:A AND m:B", "(AND (: n A) (: m B))"},

		// function calls
		{"f()", "(call f)"},
		{"f(1, 2)", "(call f 1 2)"},
		{"count(*)", "(call count *)"},
		{"COUNT(*)", "(call COUNT *)"},
		{"count(DISTINCT x)", "(call count distinct x)"},
		{"apoc.text.join(a, ',')", `(call apoc.text.join a ",")`},
		{"a.b.f(x).c", "(. (call a.b.f x) c)"},
		{"size(n.items) > 0", "(chain (call size (. n items)) > 0)"},
		{"exists(n.name)", "(call exists (. n name))"},
		{"f(g(h(1)))", "(call f (call g (call h 1)))"},
		{"match", "match"}, // keywords are valid variable names
		{"count", "count"},

		// CASE
		{"CASE WHEN a THEN 1 END", "(case (when a 1))"},
		{"CASE WHEN a THEN 1 WHEN b THEN 2 ELSE 3 END", "(case (when a 1) (when b 2) (else 3))"},
		{"CASE x WHEN 1 THEN 'a' ELSE 'b' END", `(case x (when 1 "a") (else "b"))`},
		{"CASE WHEN CASE WHEN a THEN b END THEN 1 END", "(case (when (case (when a b)) 1))"},
		{"CASE n.x WHEN 1 THEN 2 END + 1", "(+ (case (. n x) (when 1 2)) 1)"},
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

func TestParseExpr_QuotedKeywordIsIdent(t *testing.T) {
	e, err := ParseExpr("`true`")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := e.(*Ident); !ok {
		t.Errorf("`true` parsed as %T, want *Ident", e)
	}
}

func TestParseExpr_LiteralValues(t *testing.T) {
	tests := []struct {
		src  string
		want int64
	}{
		{"42", 42}, {"0x7FFFFFFFFFFFFFFF", 9223372036854775807}, {"0o17", 15}, {"017", 15}, {"0", 0},
		{"-9223372036854775808", -9223372036854775808}, {"-0x10", -16}, {"9223372036854775807", 9223372036854775807},
	}
	for _, tc := range tests {
		e, err := ParseExpr(tc.src)
		if err != nil {
			t.Errorf("%q: %v", tc.src, err)
			continue
		}
		if n, ok := e.(*IntLit); !ok || n.Value != tc.want {
			t.Errorf("%q = %s, want IntLit %d", tc.src, Dump(e), tc.want)
		}
	}
	f, err := ParseExpr("-1.5e2")
	if err != nil || f.(*FloatLit).Value != -150 {
		t.Errorf("-1.5e2 = %v, %v", f, err)
	}
}

func TestParseExpr_Positions(t *testing.T) {
	e, err := ParseExpr("a +\n  b * c")
	if err != nil {
		t.Fatal(err)
	}
	bin := e.(*Binary)
	if got := bin.Pos(); got.Line != 1 || got.Col != 1 {
		t.Errorf("Binary pos = %v, want 1:1", got)
	}
	if got := bin.R.Pos(); got.Line != 2 || got.Col != 3 {
		t.Errorf("rhs pos = %v, want 2:3", got)
	}
}

func TestParseExpr_Errors(t *testing.T) {
	deepParens := strings.Repeat("(", 5000) + "1" + strings.Repeat(")", 5000)
	tests := []struct {
		name, src string
		line, col int
		msg       string
	}{
		{"empty", "", 1, 1, "end of input"},
		{"dangling operator", "1 +", 1, 4, "end of input"},
		{"missing operand", "1 + * 2", 1, 5, "unexpected token"},
		{"unclosed paren", "(1", 1, 3, "end of input"},
		{"unclosed list", "[1, 2", 1, 6, "end of input"},
		{"trailing comma in list", "[1,]", 1, 4, "unexpected token"},
		{"trailing comma in call", "f(1,)", 1, 5, "unexpected token"},
		{"property without name", "a.", 1, 3, "end of input"},
		{"property on number", "a.1", 1, 2, "unexpected token"},
		{"trailing tokens", "a b", 1, 3, "unexpected token"},
		{"map without colon", "{a 1}", 1, 4, "unexpected token"},
		{"map string key", "{'a': 1}", 1, 2, "unexpected token"},
		{"star outside count", "sum(*)", 1, 5, "only allowed"},
		{"star in namespaced count", "a.count(*)", 1, 9, "only allowed"},
		{"distinct with no args", "f(DISTINCT)", 1, 11, "unexpected token"},
		{"case without when", "CASE END", 1, 9, "end of input"}, // END is read as the subject
		{"case unterminated", "CASE WHEN a THEN b", 1, 19, "end of input"},
		{"case when without then", "CASE WHEN a END", 1, 13, "unexpected token"},
		{"is without null", "a IS 1", 1, 6, "unexpected token"},
		{"starts without with", "a STARTS b", 1, 10, "unexpected token"},
		{"int too large", "9223372036854775808", 1, 1, "too large"},
		{"negative int too large", "-9223372036854775809", 1, 2, "too large"},
		{"hex too large", "0xFFFFFFFFFFFFFFFF", 1, 1, "too large"},
		{"bad octal", "09", 1, 1, "invalid integer"},
		{"float too large", "1e999", 1, 1, "too large"},
		{"lexer error surfaces", "'abc", 1, 1, "unterminated"},
		{"label expr missing name", "n:", 1, 3, "end of input"},
		{"deep parens", deepParens, 1, 401, "nested too deeply"},
		{"deep unary", strings.Repeat("-", 5000) + "a", 1, 402, "nested too deeply"},
		{"deep not", strings.Repeat("NOT ", 5000) + "a", 1, 1605, "nested too deeply"},
		{"deep list", strings.Repeat("[", 5000) + strings.Repeat("]", 5000), 1, 401, "nested too deeply"},
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

func TestParseExpr_ErrorListsExpectation(t *testing.T) {
	_, err := ParseExpr("(1")
	var se *SyntaxError
	if !errors.As(err, &se) || len(se.Expected) != 1 || se.Expected[0] != ")" {
		t.Errorf("Expected = %v, want [\")\"] (%v)", se, err)
	}
}

func BenchmarkParseExpr(b *testing.B) {
	const src = "a.age > 30 AND b.name STARTS WITH $prefix OR count(DISTINCT c.x) IN [1, 2, 3]"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := ParseExpr(src); err != nil {
			b.Fatal(err)
		}
	}
}
