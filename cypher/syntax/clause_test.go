package syntax

import (
	"errors"
	"strings"
	"testing"
)

func sxExprs(es []Expr) string {
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = sx(e)
	}
	return strings.Join(parts, ", ")
}

func sxParts(ps []*PatternPart) string {
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = sx(p)
	}
	return strings.Join(parts, ", ")
}

func sxProjection(p Projection) string {
	var b strings.Builder
	if p.Distinct {
		b.WriteString(" DISTINCT")
	}
	var items []string
	if p.Star {
		items = append(items, "*")
	}
	for _, it := range p.Items {
		s := sx(it.Expr)
		if it.Alias != "" {
			s += " AS " + it.Alias
		}
		items = append(items, s)
	}
	b.WriteString(" " + strings.Join(items, ", "))
	if len(p.Order) > 0 {
		var o []string
		for _, s := range p.Order {
			x := sx(s.Expr)
			if s.Desc {
				x += " DESC"
			}
			o = append(o, x)
		}
		b.WriteString(" ORDER BY " + strings.Join(o, ", "))
	}
	b.WriteString(opt(" SKIP ", p.Skip))
	b.WriteString(opt(" LIMIT ", p.Limit))
	return b.String()
}

func sxSetItem(i SetItem) string {
	switch i.Kind {
	case SetProperty:
		return sx(i.Target) + "=" + sx(i.Value)
	case SetReplace:
		return sx(i.Target) + "=" + sx(i.Value)
	case SetMerge:
		return sx(i.Target) + "+=" + sx(i.Value)
	}
	return sx(i.Target) + ":" + sx(i.Labels)
}

func sxSetItems(is []SetItem) string {
	parts := make([]string, len(is))
	for i, it := range is {
		parts[i] = sxSetItem(it)
	}
	return strings.Join(parts, ", ")
}

func sxYield(y *Yield) string {
	if y == nil {
		return ""
	}
	s := " YIELD"
	if y.Star {
		s += " *"
	}
	for i, it := range y.Items {
		if i > 0 {
			s += ","
		}
		s += " " + it.Name
		if it.Alias != "" {
			s += " AS " + it.Alias
		}
	}
	return s + opt(" WHERE ", y.Where)
}

// sxClauses renders statements and clauses for tests.
func sxClauses(n Node) (string, bool) {
	switch n := n.(type) {
	case *Statement:
		var prefix string
		switch n.Mode {
		case ModeExplain:
			prefix += "EXPLAIN "
		case ModeProfile:
			prefix += "PROFILE "
		}
		if n.Version != "" || len(n.Options) > 0 {
			prefix += "CYPHER"
			if n.Version != "" {
				prefix += " " + n.Version
			}
			for _, o := range n.Options {
				prefix += " " + o.Key + "=" + o.Value
			}
			prefix += " "
		}
		return prefix + sx(n.Body), true
	case *SingleQuery:
		parts := make([]string, len(n.Clauses))
		for i, c := range n.Clauses {
			parts[i] = sx(c)
		}
		return strings.Join(parts, " "), true
	case *UnionQuery:
		sep := " UNION "
		if n.All {
			sep = " UNION ALL "
		}
		parts := make([]string, len(n.Queries))
		for i, q := range n.Queries {
			parts[i] = sx(q)
		}
		return strings.Join(parts, sep), true
	case *Match:
		s := "MATCH "
		if n.Optional {
			s = "OPTIONAL MATCH "
		}
		hints := ""
		for _, h := range n.Hints {
			hints += " USING<" + h.Kind + ">(" + h.Text + ")"
		}
		return s + sxParts(n.Patterns) + hints + opt(" WHERE ", n.Where), true
	case *Unwind:
		return "UNWIND " + sx(n.Expr) + " AS " + n.Var, true
	case *With:
		return "WITH" + sxProjection(n.Projection) + opt(" WHERE ", n.Where), true
	case *Return:
		return "RETURN" + sxProjection(n.Projection), true
	case *Create:
		return "CREATE " + sxParts(n.Patterns), true
	case *Merge:
		s := "MERGE " + sx(n.Pattern)
		for _, a := range n.Actions {
			if a.OnCreate {
				s += " ON CREATE SET " + sxSetItems(a.Items)
			} else {
				s += " ON MATCH SET " + sxSetItems(a.Items)
			}
		}
		return s, true
	case *Set:
		return "SET " + sxSetItems(n.Items), true
	case *Remove:
		parts := make([]string, len(n.Items))
		for i, it := range n.Items {
			parts[i] = sx(it.Target)
			if it.Labels != nil {
				parts[i] += ":" + sx(it.Labels)
			}
		}
		return "REMOVE " + strings.Join(parts, ", "), true
	case *Delete:
		s := "DELETE "
		if n.Detach {
			s = "DETACH DELETE "
		}
		return s + sxExprs(n.Exprs), true
	case *Foreach:
		parts := make([]string, len(n.Body))
		for i, c := range n.Body {
			parts[i] = sx(c)
		}
		return "FOREACH (" + n.Var + " IN " + sx(n.In) + " | " + strings.Join(parts, " ") + ")", true
	case *Call:
		s := "CALL " + strings.Join(n.Name, ".")
		if n.Optional {
			s = "OPTIONAL " + s
		}
		if !n.ArgsOmitted {
			s += "(" + sxExprs(n.Args) + ")"
		}
		return s + sxYield(n.Yield), true
	}
	return "", false
}

func parseOK(t *testing.T, src string) *Statement {
	t.Helper()
	st, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	return st
}

func TestParse_Clauses(t *testing.T) {
	tests := []struct{ src, want string }{
		// MATCH / OPTIONAL MATCH / WHERE
		{"MATCH (n) RETURN n", "MATCH (n) RETURN n"},
		{"match (n:Person) return n.name", "MATCH (n:Person) RETURN (. n name)"},
		{"MATCH (a), (b) RETURN a, b", "MATCH (a), (b) RETURN a, b"},
		{"MATCH (a)-[r:KNOWS]->(b) WHERE b.age > 30 RETURN a", "MATCH (a)-[r:KNOWS]->(b) WHERE (chain (. b age) > 30) RETURN a"},
		{"MATCH p = (a)-->(b) RETURN p", "MATCH p=(a)-->(b) RETURN p"},
		{"MATCH p = shortestPath((a)-[*]-(b)) RETURN p", "MATCH p=shortestPath[(a)-[*]-(b)] RETURN p"},
		{"MATCH (a) OPTIONAL MATCH (a)-->(b) RETURN b", "MATCH (a) OPTIONAL MATCH (a)-->(b) RETURN b"},
		{"MATCH (a) MATCH (b) RETURN a", "MATCH (a) MATCH (b) RETURN a"},

		// UNWIND
		{"UNWIND [1, 2] AS x RETURN x", "UNWIND [1 2] AS x RETURN x"},
		{"UNWIND $list AS x RETURN x", "UNWIND $list AS x RETURN x"},

		// projections
		{"MATCH (n) RETURN n AS m", "MATCH (n) RETURN n AS m"},
		{"MATCH (n) RETURN DISTINCT n.x", "MATCH (n) RETURN DISTINCT (. n x)"},
		{"MATCH (n) RETURN *", "MATCH (n) RETURN *"},
		{"MATCH (n) RETURN *, n.x AS x", "MATCH (n) RETURN *, (. n x) AS x"},
		{"MATCH (n) RETURN n ORDER BY n.x", "MATCH (n) RETURN n ORDER BY (. n x)"},
		{"MATCH (n) RETURN n ORDER BY n.x DESC, n.y ASC, n.z DESCENDING", "MATCH (n) RETURN n ORDER BY (. n x) DESC, (. n y), (. n z) DESC"},
		{"MATCH (n) RETURN n SKIP 5 LIMIT 10", "MATCH (n) RETURN n SKIP 5 LIMIT 10"},
		{"MATCH (n) RETURN n LIMIT 10 SKIP 5", "MATCH (n) RETURN n SKIP 5 LIMIT 10"},
		{"MATCH (n) RETURN n OFFSET 5", "MATCH (n) RETURN n SKIP 5"},
		{"MATCH (n) RETURN n SKIP $s LIMIT $l", "MATCH (n) RETURN n SKIP $s LIMIT $l"},
		{"MATCH (n) RETURN n ORDER BY n.x SKIP 1 LIMIT 2", "MATCH (n) RETURN n ORDER BY (. n x) SKIP 1 LIMIT 2"},
		{"MATCH (n) RETURN count(*) AS c, sum(n.x)", "MATCH (n) RETURN (call count *) AS c, (call sum (. n x))"},

		// WITH
		{"MATCH (n) WITH n RETURN n", "MATCH (n) WITH n RETURN n"},
		{"MATCH (n) WITH n.x AS x WHERE x > 1 RETURN x", "MATCH (n) WITH (. n x) AS x WHERE (chain x > 1) RETURN x"},
		{"MATCH (n) WITH DISTINCT n ORDER BY n.x LIMIT 3 WHERE n.y RETURN n", "MATCH (n) WITH DISTINCT n ORDER BY (. n x) LIMIT 3 WHERE (. n y) RETURN n"},
		{"MATCH (n) WITH * RETURN *", "MATCH (n) WITH * RETURN *"},
		{"MATCH (n) WITH n MATCH (m) RETURN m", "MATCH (n) WITH n MATCH (m) RETURN m"},

		// CREATE
		{"CREATE (n:Person {name: 'a'})", `CREATE (n:Person {name:"a"})`},
		{"CREATE (a)-[:R]->(b), (c)", "CREATE (a)-[:R]->(b), (c)"},
		{"CREATE (n) RETURN n", "CREATE (n) RETURN n"},
		{"MATCH (a) CREATE (a)-[:R]->(b:L)", "MATCH (a) CREATE (a)-[:R]->(b:L)"},

		// MERGE
		{"MERGE (n:Person {id: 1})", "MERGE (n:Person {id:1})"},
		{"MERGE (n:P) ON CREATE SET n.c = 1", "MERGE (n:P) ON CREATE SET (. n c)=1"},
		{"MERGE (n:P) ON MATCH SET n.m = 2, n.k = 3", "MERGE (n:P) ON MATCH SET (. n m)=2, (. n k)=3"},
		{"MERGE (n:P) ON CREATE SET n.c = 1 ON MATCH SET n.m = 2 RETURN n", "MERGE (n:P) ON CREATE SET (. n c)=1 ON MATCH SET (. n m)=2 RETURN n"},
		{"MERGE (a)-[r:R]->(b)", "MERGE (a)-[r:R]->(b)"},

		// SET
		{"MATCH (n) SET n.a = 1", "MATCH (n) SET (. n a)=1"},
		{"MATCH (n) SET n.a = 1, n.b = n.a + 1", "MATCH (n) SET (. n a)=1, (. n b)=(+ (. n a) 1)"},
		{"MATCH (n) SET n = {a: 1}", "MATCH (n) SET n={a:1}"},
		{"MATCH (n) SET n += $props", "MATCH (n) SET n+=$props"},
		{"MATCH (n) SET n:Label", "MATCH (n) SET n:Label"},
		{"MATCH (n) SET n:A:B", "MATCH (n) SET n:(& A B)"},
		{"MATCH (n) SET n[$k] = 1", "MATCH (n) SET (idx n $k)=1"},
		{"MATCH (n) SET n.a.b = 1", "MATCH (n) SET (. (. n a) b)=1"},
		{"MATCH (n) SET n.a = 1, n:L, n += {b: 2}", "MATCH (n) SET (. n a)=1, n:L, n+={b:2}"},

		// REMOVE
		{"MATCH (n) REMOVE n.a", "MATCH (n) REMOVE (. n a)"},
		{"MATCH (n) REMOVE n:Label", "MATCH (n) REMOVE n:Label"},
		{"MATCH (n) REMOVE n.a, n:L:M", "MATCH (n) REMOVE (. n a), n:(& L M)"},
		{"MATCH (n) REMOVE n[$k]", "MATCH (n) REMOVE (idx n $k)"},

		// DELETE
		{"MATCH (n) DELETE n", "MATCH (n) DELETE n"},
		{"MATCH (n) DETACH DELETE n", "MATCH (n) DETACH DELETE n"},
		{"MATCH (n)-[r]->(m) DELETE r, m", "MATCH (n)-[r]->(m) DELETE r, m"},
		{"MATCH (n) NODETACH DELETE n", "MATCH (n) DELETE n"},

		// FOREACH
		{"MATCH p = (a)-->(b) FOREACH (n IN nodes(p) | SET n.v = 1)", "MATCH p=(a)-->(b) FOREACH (n IN (call nodes p) | SET (. n v)=1)"},
		{"FOREACH (x IN [1, 2] | CREATE (:N {v: x}) SET x.a = 1)", "FOREACH (x IN [1 2] | CREATE (:N {v:x}) SET (. x a)=1)"},
		{"FOREACH (x IN l | FOREACH (y IN x | CREATE (:N)))", "FOREACH (x IN l | FOREACH (y IN x | CREATE (:N)))"},

		// CALL
		{"CALL db.labels()", "CALL db.labels()"},
		{"CALL db.labels() YIELD label", "CALL db.labels() YIELD label"},
		{"CALL db.labels() YIELD label AS l, x WHERE l = 'A'", `CALL db.labels() YIELD label AS l, x WHERE (chain l = "A")`},
		{"CALL db.labels YIELD *", "CALL db.labels YIELD *"},
		{"CALL test.proc", "CALL test.proc"},
		{"CALL proc(1, $p)", "CALL proc(1, $p)"},
		{"MATCH (n) CALL proc(n) YIELD x RETURN x", "MATCH (n) CALL proc(n) YIELD x RETURN x"},
		{"MATCH (n) CALL proc(n)", "MATCH (n) CALL proc(n)"},
		{"OPTIONAL CALL proc() YIELD x RETURN x", "OPTIONAL CALL proc() YIELD x RETURN x"},

		// UNION
		{"MATCH (a) RETURN a UNION MATCH (b) RETURN b", "MATCH (a) RETURN a UNION MATCH (b) RETURN b"},
		{"RETURN 1 AS x UNION ALL RETURN 2 AS x UNION ALL RETURN 3 AS x", "RETURN 1 AS x UNION ALL RETURN 2 AS x UNION ALL RETURN 3 AS x"},

		// misc
		{"MATCH (n) RETURN n;", "MATCH (n) RETURN n"},
		{"RETURN 1", "RETURN 1"},
		{"  MATCH (n) // c\n RETURN n /* x */", "MATCH (n) RETURN n"},
		{"MATCH (n) RETURN n.set, n.match", "MATCH (n) RETURN (. n set), (. n match)"},
		{"MATCH (match) RETURN match", "MATCH (match) RETURN match"},
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

func TestParse_ProjectionSource(t *testing.T) {
	st := parseOK(t, "RETURN a.x  +  1, count( * ),\n  n.name AS nm")
	ret := st.Body.(*SingleQuery).Clauses[0].(*Return)
	want := []string{"a.x  +  1", "count( * )", "n.name"}
	for i, it := range ret.Items {
		if it.Source != want[i] {
			t.Errorf("item %d Source = %q, want %q", i, it.Source, want[i])
		}
	}
}

func TestParse_Positions(t *testing.T) {
	st := parseOK(t, "MATCH (n)\n  WITH n\n  RETURN n")
	if st.Pos().Line != 1 || st.Pos().Col != 1 {
		t.Errorf("statement pos = %v", st.Pos())
	}
	cs := st.Body.(*SingleQuery).Clauses
	wantLines := []int{1, 2, 3}
	for i, c := range cs {
		if c.Pos().Line != wantLines[i] {
			t.Errorf("clause %d (%s) at line %d, want %d", i, clauseName(c), c.Pos().Line, wantLines[i])
		}
	}
}

func TestParse_Errors(t *testing.T) {
	tests := []struct {
		name, src string
		line, col int
		msg       string
	}{
		{"empty", "", 1, 1, "end of input"},
		{"only comment", "// nothing", 1, 11, "end of input"},
		{"garbage", "FOO BAR", 1, 1, "unexpected token"},
		{"trailing garbage", "RETURN 1 BAR", 1, 10, "unexpected token"},
		{"trailing second statement", "RETURN 1; RETURN 2", 1, 11, "unexpected token"},
		{"match without pattern", "MATCH RETURN 1", 1, 7, "unexpected token"},
		{"match unterminated pattern", "MATCH (n RETURN n", 1, 10, "unexpected token"},
		{"query ends with match", "MATCH (n)", 1, 1, "cannot conclude with MATCH"},
		{"query ends with with", "MATCH (n) WITH n", 1, 11, "cannot conclude with WITH"},
		{"query ends with unwind", "UNWIND [1] AS x", 1, 1, "cannot conclude with UNWIND"},
		{"return not last", "RETURN 1 MATCH (n) RETURN n", 1, 10, "RETURN must be the last clause"},
		{"return then create", "RETURN 1 CREATE (n)", 1, 10, "RETURN must be the last clause"},
		{"match after create", "CREATE (a) MATCH (b) RETURN b", 1, 12, "WITH is required"},
		{"unwind after set", "MATCH (a) SET a.x = 1 UNWIND [1] AS y RETURN y", 1, 23, "WITH is required"},
		{"match after merge", "MERGE (a) MATCH (b) RETURN b", 1, 11, "WITH is required"},
		{"match after delete", "MATCH (a) DELETE a MATCH (b) RETURN b", 1, 20, "WITH is required"},
		{"match after foreach", "FOREACH (x IN [1] | CREATE (:N)) MATCH (b) RETURN b", 1, 34, "WITH is required"},
		{"with after create is fine but bad end", "CREATE (a) WITH a", 1, 12, "cannot conclude with WITH"},
		{"unwind without as", "UNWIND [1] x RETURN x", 1, 12, "unexpected token"},
		{"return without items", "RETURN", 1, 7, "end of input"},
		{"return trailing comma", "RETURN 1,", 1, 10, "end of input"},
		{"order without by", "RETURN n ORDER n", 1, 16, "unexpected token"},
		{"duplicate limit", "RETURN n LIMIT 1 LIMIT 2", 1, 18, "duplicate LIMIT"},
		{"duplicate skip", "RETURN n SKIP 1 OFFSET 2", 1, 17, "duplicate SKIP"},
		{"set without value", "MATCH (n) SET n.a RETURN n", 1, 19, "unexpected token"},
		{"set bare property target", "MATCH (n) SET n", 1, 16, "end of input"},
		{"set bad target", "MATCH (n) SET 1 = 2", 1, 15, "invalid SET target"},
		{"set label on property", "MATCH (n) SET n.a:L", 1, 15, "variable"},
		{"remove bad target", "MATCH (n) REMOVE n", 1, 18, "REMOVE expects"},
		{"detach without delete", "MATCH (n) DETACH n", 1, 18, "unexpected token"},
		{"delete without target", "MATCH (n) DELETE", 1, 17, "end of input"},
		{"merge on bad", "MERGE (n) ON FOO SET n.a = 1", 1, 14, "unexpected token"},
		{"merge on without set", "MERGE (n) ON CREATE n.a = 1", 1, 21, "unexpected token"},
		{"foreach non-updating", "FOREACH (x IN l | MATCH (n))", 1, 19, "not allowed inside FOREACH"},
		{"foreach empty body", "FOREACH (x IN l | )", 1, 19, "unexpected token"},
		{"foreach missing pipe", "FOREACH (x IN l CREATE (n))", 1, 17, "unexpected token"},
		{"optional alone", "OPTIONAL RETURN 1", 1, 10, "unexpected token"},
		{"call with yield then more", "MATCH (n) CALL p() YIELD x", 1, 11, "cannot conclude with CALL"},
		{"call scope without body", "CALL ()", 1, 8, "end of input"},
		{"call yield trailing comma", "CALL p() YIELD a,", 1, 18, "end of input"},
		{"union mixed", "RETURN 1 UNION RETURN 2 UNION ALL RETURN 3", 1, 25, "cannot mix"},
		{"union part without return", "MATCH (n) RETURN n UNION CREATE (m)", 1, 26, "must end with RETURN"},
		{"union first part without return", "CREATE (n) UNION RETURN 1", 1, 1, "must end with RETURN"},
		{"union dangling", "RETURN 1 UNION", 1, 15, "end of input"},
		{"lexer error", "RETURN 'abc", 1, 8, "unterminated"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.src)
			var se *SyntaxError
			if !errors.As(err, &se) {
				t.Fatalf("Parse(%q) error = %v, want *SyntaxError", tc.src, err)
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

func TestParse_ConcurrentSafe(t *testing.T) {
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			var err error
			for j := 0; j < 200 && err == nil; j++ {
				_, err = Parse("MATCH (a:P {x: $x})-[:R*1..3]->(b) WHERE b.y > 1 WITH a, count(*) AS c ORDER BY c DESC LIMIT 5 RETURN a, c")
			}
			done <- err
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func BenchmarkParse(b *testing.B) {
	const q = `MATCH (a:Person {name: $n})-[:KNOWS]->(b) WHERE b.age > 30 AND a.x = 1 RETURN b.name, count(*) ORDER BY b.name LIMIT 10`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Parse(q); err != nil {
			b.Fatal(err)
		}
	}
}
