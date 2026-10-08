package syntax

import (
	"errors"
	"strings"
	"testing"
)

func sxSchema(n Node) (string, bool) {
	switch n := n.(type) {
	case *CreateIndex:
		s := "CREATE "
		if n.Kind != "" {
			s += n.Kind + " "
		}
		s += "INDEX "
		if n.Name != "" {
			s += n.Name + " "
		}
		if n.IfNotExists {
			s += "IF NOT EXISTS "
		}
		s += "FOR " + sx(n.Target) + " ON "
		if n.Each {
			s += "EACH "
		}
		s += sxExprs(n.Properties)
		return s + opt(" OPTIONS ", n.Options), true
	case *CreateConstraint:
		s := "CREATE CONSTRAINT "
		if n.Name != "" {
			s += n.Name + " "
		}
		if n.IfNotExists {
			s += "IF NOT EXISTS "
		}
		s += "FOR " + sx(n.Target) + " REQUIRE " + sxExprs(n.Properties) + " IS "
		switch n.Kind {
		case ConstraintUnique:
			s += "UNIQUE"
		case ConstraintNotNull:
			s += "NOT NULL"
		case ConstraintKey:
			s += "KEY"
		case ConstraintType:
			s += ":: " + n.ValueType
		}
		return s + opt(" OPTIONS ", n.Options), true
	case *DropSchema:
		s := "DROP INDEX "
		if n.Constraint {
			s = "DROP CONSTRAINT "
		}
		s += n.Name
		if n.IfExists {
			s += " IF EXISTS"
		}
		return s, true
	case *Show:
		return "SHOW " + n.What + sxYield(n.Yield) + opt(" WHERE ", n.Where), true
	case *ServerCommand:
		return "SERVER " + n.Command, true
	case *LoadCSV:
		s := "LOAD CSV "
		if n.WithHeaders {
			s += "WITH HEADERS "
		}
		return s + "FROM " + sx(n.From) + " AS " + n.Var + opt(" FIELDTERMINATOR ", n.FieldTerminator), true
	case *Filter:
		return "FILTER " + sx(n.Cond), true
	case *Let:
		parts := make([]string, len(n.Items))
		for i, it := range n.Items {
			parts[i] = it.Var + "=" + sx(it.Expr)
		}
		return "LET " + strings.Join(parts, ", "), true
	case *Finish:
		return "FINISH", true
	}
	return "", false
}

func TestParse_Prefixes(t *testing.T) {
	tests := []struct{ src, want string }{
		{"EXPLAIN MATCH (n) RETURN n", "EXPLAIN MATCH (n) RETURN n"},
		{"PROFILE MATCH (n) RETURN n", "PROFILE MATCH (n) RETURN n"},
		{"explain match (n) return n", "EXPLAIN MATCH (n) RETURN n"},
		{"CYPHER 25 MATCH (n:Order) RETURN n", "CYPHER 25 MATCH (n:Order) RETURN n"},
		{"CYPHER 25 runtime=parallel MATCH (n:Person) RETURN n.name", "CYPHER 25 runtime=parallel MATCH (n:Person) RETURN (. n name)"},
		{"CYPHER runtime=parallel MATCH (n) RETURN n", "CYPHER runtime=parallel MATCH (n) RETURN n"},
		{"CYPHER 25 runtime=slotted planner=cost MATCH (n) RETURN n", "CYPHER 25 runtime=slotted planner=cost MATCH (n) RETURN n"},
		{"CYPHER 25 retries=3 label='x' MATCH (n) RETURN n", "CYPHER 25 retries=3 label=x MATCH (n) RETURN n"},
		{"EXPLAIN CYPHER 25 MATCH (n) RETURN n", "EXPLAIN CYPHER 25 MATCH (n) RETURN n"},
		{"CYPHER 25 PROFILE MATCH (n) RETURN n", "PROFILE CYPHER 25 MATCH (n) RETURN n"},
		{"EXPLAIN CREATE INDEX FOR (n:L) ON (n.p)", "EXPLAIN CREATE INDEX FOR (n:L) ON (. n p)"},
	}
	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			if got := sx(parseOK(t, tc.src)); got != tc.want {
				t.Errorf("Parse(%q)\n got %s\nwant %s", tc.src, got, tc.want)
			}
		})
	}
	st := parseOK(t, "EXPLAIN CYPHER 25 runtime=parallel RETURN 1")
	if st.Mode != ModeExplain || st.Version != "25" || len(st.Options) != 1 || st.Options[0].Key != "runtime" || st.Options[0].Value != "parallel" {
		t.Errorf("statement = %+v", st)
	}
}

func TestParse_SchemaCommands(t *testing.T) {
	tests := []struct{ src, want string }{
		// indexes (syntax from the Neo4j manual)
		{"CREATE INDEX index_name FOR (n:Label) ON (n.prop)", "CREATE INDEX index_name FOR (n:Label) ON (. n prop)"},
		{"CREATE INDEX index_name FOR (n:Label) ON (n.p1, n.p2)", "CREATE INDEX index_name FOR (n:Label) ON (. n p1), (. n p2)"},
		{"CREATE INDEX index_name FOR ()-[r:TYPE]-() ON (r.prop)", "CREATE INDEX index_name FOR -[r:TYPE]- ON (. r prop)"},
		{"CREATE RANGE INDEX i FOR (n:L) ON (n.p)", "CREATE RANGE INDEX i FOR (n:L) ON (. n p)"},
		{"CREATE TEXT INDEX i FOR (n:L) ON (n.p)", "CREATE TEXT INDEX i FOR (n:L) ON (. n p)"},
		{"CREATE TEXT INDEX i FOR ()-[r:T]-() ON (r.p)", "CREATE TEXT INDEX i FOR -[r:T]- ON (. r p)"},
		{"CREATE POINT INDEX i FOR (n:L) ON (n.p) OPTIONS { indexConfig: { `spatial.cartesian.min`: [-100.0, -100.0] } }", "CREATE POINT INDEX i FOR (n:L) ON (. n p) OPTIONS {indexConfig:{spatial.cartesian.min:[-100.0 -100.0]}}"},
		{"CREATE LOOKUP INDEX i FOR (n) ON EACH labels(n)", "CREATE LOOKUP INDEX i FOR (n) ON EACH (call labels n)"},
		{"CREATE LOOKUP INDEX i FOR ()-[r]-() ON EACH type(r)", "CREATE LOOKUP INDEX i FOR -[r]- ON EACH (call type r)"},
		{"CREATE FULLTEXT INDEX i FOR (n:A|B) ON EACH [n.a, n.b]", "CREATE FULLTEXT INDEX i FOR (n:(| A B)) ON EACH (. n a), (. n b)"},
		{"CREATE VECTOR INDEX i FOR (n:L) ON (n.embedding) OPTIONS {indexConfig: {`vector.dimensions`: 3}}", "CREATE VECTOR INDEX i FOR (n:L) ON (. n embedding) OPTIONS {indexConfig:{vector.dimensions:3}}"},
		{"CREATE INDEX FOR (n:L) ON (n.p)", "CREATE INDEX FOR (n:L) ON (. n p)"},
		{"CREATE INDEX i IF NOT EXISTS FOR (n:L) ON (n.p)", "CREATE INDEX i IF NOT EXISTS FOR (n:L) ON (. n p)"},
		{"CREATE INDEX IF NOT EXISTS FOR (n:L) ON (n.p)", "CREATE INDEX IF NOT EXISTS FOR (n:L) ON (. n p)"},
		{"CREATE INDEX `my index` FOR (n:L) ON n.p", "CREATE INDEX my index FOR (n:L) ON (. n p)"},
		{"create index i for (n:L) on (n.p)", "CREATE INDEX i FOR (n:L) ON (. n p)"},
		// constraints
		{"CREATE CONSTRAINT c FOR (n:L) REQUIRE n.p IS UNIQUE", "CREATE CONSTRAINT c FOR (n:L) REQUIRE (. n p) IS UNIQUE"},
		{"CREATE CONSTRAINT c FOR ()-[r:T]-() REQUIRE r.p IS UNIQUE", "CREATE CONSTRAINT c FOR -[r:T]- REQUIRE (. r p) IS UNIQUE"},
		{"CREATE CONSTRAINT c FOR (n:L) REQUIRE (n.a, n.b) IS UNIQUE", "CREATE CONSTRAINT c FOR (n:L) REQUIRE (. n a), (. n b) IS UNIQUE"},
		{"CREATE CONSTRAINT c FOR (n:L) REQUIRE n.p IS NOT NULL", "CREATE CONSTRAINT c FOR (n:L) REQUIRE (. n p) IS NOT NULL"},
		{"CREATE CONSTRAINT c FOR ()-[r:T]-() REQUIRE r.p IS NOT NULL", "CREATE CONSTRAINT c FOR -[r:T]- REQUIRE (. r p) IS NOT NULL"},
		{"CREATE CONSTRAINT c FOR (n:L) REQUIRE n.p IS :: INTEGER", "CREATE CONSTRAINT c FOR (n:L) REQUIRE (. n p) IS :: INTEGER"},
		{"CREATE CONSTRAINT c FOR (n:L) REQUIRE n.p IS :: STRING | LIST<STRING NOT NULL>", "CREATE CONSTRAINT c FOR (n:L) REQUIRE (. n p) IS :: STRING | LIST<STRING NOT NULL>"},
		{"CREATE CONSTRAINT c FOR (n:L) REQUIRE n.p IS TYPED BOOLEAN", "CREATE CONSTRAINT c FOR (n:L) REQUIRE (. n p) IS :: BOOLEAN"},
		{"CREATE CONSTRAINT c FOR (n:L) REQUIRE n.p IS NODE KEY", "CREATE CONSTRAINT c FOR (n:L) REQUIRE (. n p) IS KEY"},
		{"CREATE CONSTRAINT c FOR (n:L) REQUIRE (n.a, n.b) IS NODE KEY", "CREATE CONSTRAINT c FOR (n:L) REQUIRE (. n a), (. n b) IS KEY"},
		{"CREATE CONSTRAINT c FOR ()-[r:T]-() REQUIRE r.p IS RELATIONSHIP KEY", "CREATE CONSTRAINT c FOR -[r:T]- REQUIRE (. r p) IS KEY"},
		{"CREATE CONSTRAINT c IF NOT EXISTS FOR (n:L) REQUIRE n.p IS UNIQUE", "CREATE CONSTRAINT c IF NOT EXISTS FOR (n:L) REQUIRE (. n p) IS UNIQUE"},
		{"CREATE CONSTRAINT FOR (n:L) REQUIRE n.p IS UNIQUE OPTIONS {indexProvider: 'range-1.0'}", `CREATE CONSTRAINT FOR (n:L) REQUIRE (. n p) IS UNIQUE OPTIONS {indexProvider:"range-1.0"}`},
		// drop
		{"DROP INDEX i", "DROP INDEX i"},
		{"DROP INDEX i IF EXISTS", "DROP INDEX i IF EXISTS"},
		{"DROP CONSTRAINT c", "DROP CONSTRAINT c"},
		{"DROP CONSTRAINT c IF EXISTS", "DROP CONSTRAINT c IF EXISTS"},
		// show
		{"SHOW INDEXES", "SHOW INDEXES"},
		{"SHOW CONSTRAINTS", "SHOW CONSTRAINTS"},
		{"SHOW TEXT INDEXES", "SHOW TEXT INDEXES"},
		{"SHOW ALL CONSTRAINTS", "SHOW ALL CONSTRAINTS"},
		{"SHOW INDEXES YIELD name, type AS t", "SHOW INDEXES YIELD name, type AS t"},
		{"SHOW INDEXES YIELD name WHERE name = 'x'", `SHOW INDEXES YIELD name WHERE (chain name = "x")`},
		{"SHOW INDEXES WHERE type = 'RANGE'", `SHOW INDEXES WHERE (chain type = "RANGE")`},
		{"SHOW FUNCTIONS EXECUTABLE BY CURRENT USER", "SHOW FUNCTIONS EXECUTABLE BY CURRENT USER"},
		{"SHOW PROCEDURES;", "SHOW PROCEDURES"},
		// ordinary CREATE is unaffected
		{"CREATE (n:Index)", "CREATE (n:Index)"},
		{"CREATE (n)-[:R]->(m) RETURN n", "CREATE (n)-[:R]->(m) RETURN n"},
	}
	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			if got := sx(parseOK(t, tc.src)); got != tc.want {
				t.Errorf("Parse(%q)\n got %s\nwant %s", tc.src, got, tc.want)
			}
		})
	}
}

func TestParse_ServerCommands(t *testing.T) {
	tests := []struct{ src, want string }{
		{"USE neo4j MATCH (n) RETURN n", "SERVER USE"},
		{"USE `my db`", "SERVER USE"},
		{"CREATE USER foo SET PASSWORD 'secret'", "SERVER CREATE USER"},
		{"CREATE ROLE reader", "SERVER CREATE ROLE"},
		{"CREATE DATABASE db", "SERVER CREATE DATABASE"},
		{"CREATE OR REPLACE DATABASE db", "SERVER CREATE OR"},
		{"CREATE ALIAS a FOR DATABASE db", "SERVER CREATE ALIAS"},
		{"DROP DATABASE db", "SERVER DROP DATABASE"},
		{"DROP USER foo", "SERVER DROP USER"},
		{"GRANT ROLE admin TO foo", "SERVER GRANT"},
		{"DENY READ {*} ON GRAPH * TO reader", "SERVER DENY"},
		{"REVOKE ROLE admin FROM foo", "SERVER REVOKE"},
		{"ALTER USER foo SET PASSWORD 'x'", "SERVER ALTER"},
		{"START DATABASE db", "SERVER START"},
		{"STOP DATABASE db", "SERVER STOP"},
	}
	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			if got := sx(parseOK(t, tc.src)); got != tc.want {
				t.Errorf("Parse(%q)\n got %s\nwant %s", tc.src, got, tc.want)
			}
		})
	}
}

func TestParse_LoadCSV(t *testing.T) {
	tests := []struct{ src, want string }{
		{"LOAD CSV FROM 'file:///x.csv' AS row RETURN row", `LOAD CSV FROM "file:///x.csv" AS row RETURN row`},
		{"LOAD CSV WITH HEADERS FROM $url AS r CREATE (:N {a: r.a})", "LOAD CSV WITH HEADERS FROM $url AS r CREATE (:N {a:(. r a)})"},
		{"LOAD CSV FROM 'f' AS row FIELDTERMINATOR ';' RETURN row", `LOAD CSV FROM "f" AS row FIELDTERMINATOR ";" RETURN row`},
		{"LOAD CSV WITH HEADERS FROM 'f' AS r WITH r WHERE r.x MATCH (n) RETURN n", `LOAD CSV WITH HEADERS FROM "f" AS r WITH r WHERE (. r x) MATCH (n) RETURN n`},
		{"MATCH (a) WITH a LOAD CSV FROM 'f' AS row RETURN row", `MATCH (a) WITH a LOAD CSV FROM "f" AS row RETURN row`},
	}
	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			if got := sx(parseOK(t, tc.src)); got != tc.want {
				t.Errorf("Parse(%q)\n got %s\nwant %s", tc.src, got, tc.want)
			}
		})
	}
}

func TestParse_Cypher25Clauses(t *testing.T) {
	tests := []struct{ src, want string }{
		{"MATCH (n) FILTER n:Swedish RETURN n", "MATCH (n) FILTER (: n Swedish) RETURN n"},
		{"MATCH (n) FILTER WHERE n.x > 1 RETURN n", "MATCH (n) FILTER (chain (. n x) > 1) RETURN n"},
		{"MATCH (n) FILTER n.a AND n.b RETURN n", "MATCH (n) FILTER (AND (. n a) (. n b)) RETURN n"},
		{"MATCH (s)-->(p) LET supplier = s.name, product = p.name RETURN supplier", "MATCH (s)-->(p) LET supplier=(. s name), product=(. p name) RETURN supplier"},
		{"MATCH (c) LET full = c.first + ' ' + c.last LET price = c.p * 2 RETURN full", `MATCH (c) LET full=(+ (+ (. c first) " ") (. c last)) LET price=(* (. c p) 2) RETURN full`},
		{"MATCH (n) LET x = 1 FILTER x > 0 RETURN x", "MATCH (n) LET x=1 FILTER (chain x > 0) RETURN x"},
		{"FINISH", "FINISH"},
		{"MATCH (n) FINISH", "MATCH (n) FINISH"},
		{"UNWIND [1] AS x CREATE (:N {v: x}) WITH x FINISH", "UNWIND [1] AS x CREATE (:N {v:x}) WITH x FINISH"},
		{"MATCH (n) OPTIONAL MATCH (n)-->(m) FILTER m IS NOT NULL RETURN m", "MATCH (n) OPTIONAL MATCH (n)-->(m) FILTER (isnotnull m) RETURN m"},
		// the words stay usable as names
		{"MATCH (n) RETURN n.filter, n.let, n.finish", "MATCH (n) RETURN (. n filter), (. n let), (. n finish)"},
	}
	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			if got := sx(parseOK(t, tc.src)); got != tc.want {
				t.Errorf("Parse(%q)\n got %s\nwant %s", tc.src, got, tc.want)
			}
		})
	}
}

func TestParse_Hints(t *testing.T) {
	tests := []struct{ src, want string }{
		{"MATCH (n:Person) USING INDEX n:Person(name) WHERE n.name = 'x' RETURN n", `MATCH (n:Person) USING<INDEX>(INDEX n:Person(name)) WHERE (chain (. n name) = "x") RETURN n`},
		{"MATCH (n:Person) USING INDEX SEEK n:Person(name) WHERE n.name = 'x' RETURN n", `MATCH (n:Person) USING<INDEX>(INDEX SEEK n:Person(name)) WHERE (chain (. n name) = "x") RETURN n`},
		{"MATCH (n:Person) USING TEXT INDEX n:Person(name) WHERE n.name = 'x' RETURN n", `MATCH (n:Person) USING<TEXT INDEX>(TEXT INDEX n:Person(name)) WHERE (chain (. n name) = "x") RETURN n`},
		{"MATCH (n:Person) USING SCAN n:Person RETURN n", "MATCH (n:Person) USING<SCAN>(SCAN n:Person) RETURN n"},
		{"MATCH (a)-->(b) USING JOIN ON b RETURN a", "MATCH (a)-->(b) USING<JOIN>(JOIN ON b) RETURN a"},
		{"MATCH (a:A)-->(b:B) USING INDEX a:A(x) USING INDEX b:B(y) RETURN a", "MATCH (a:A)-->(b:B) USING<INDEX>(INDEX a:A(x)) USING<INDEX>(INDEX b:B(y)) RETURN a"},
		{"MATCH (n:Person) USING INDEX n:Person(name) USING INDEX n:Person(age) RETURN n", "MATCH (n:Person) USING<INDEX>(INDEX n:Person(name)) USING<INDEX>(INDEX n:Person(age)) RETURN n"},
	}
	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			if got := sx(parseOK(t, tc.src)); got != tc.want {
				t.Errorf("Parse(%q)\n got %s\nwant %s", tc.src, got, tc.want)
			}
		})
	}
}

func TestParse_Ext2Errors(t *testing.T) {
	tests := []struct {
		name, src string
		line, col int
		msg       string
	}{
		{"cypher 5", "CYPHER 5 MATCH (n) RETURN n", 1, 8, "Cypher 25"},
		{"cypher 4.0", "CYPHER 4.0 MATCH (n) RETURN n", 1, 8, "Cypher 25"},
		{"cypher alone", "CYPHER MATCH (n) RETURN n", 1, 8, "unexpected token"},
		{"cypher option swallows next word as value", "CYPHER 25 runtime= MATCH (n) RETURN n", 1, 26, "unexpected token"},
		{"duplicate cypher prefix", "CYPHER 25 CYPHER 25 MATCH (n) RETURN n", 1, 11, "duplicate CYPHER"},
		{"explain and profile", "EXPLAIN PROFILE MATCH (n) RETURN n", 1, 9, "only one of EXPLAIN"},
		{"explain alone", "EXPLAIN", 1, 8, "end of input"},
		{"index missing for", "CREATE INDEX i (n:L) ON (n.p)", 1, 16, "unexpected token"},
		{"index missing on", "CREATE INDEX i FOR (n:L)", 1, 25, "end of input"},
		{"index bad target", "CREATE INDEX i FOR (a)-->(b)-->(c) ON (a.p)", 1, 20, "expected a node pattern"},
		{"index unclosed props", "CREATE INDEX i FOR (n:L) ON (n.p", 1, 33, "end of input"},
		{"index options not map", "CREATE INDEX i FOR (n:L) ON (n.p) OPTIONS 5", 1, 43, "unexpected token"},
		{"constraint missing require", "CREATE CONSTRAINT c FOR (n:L) n.p IS UNIQUE", 1, 31, "unexpected token"},
		{"constraint bad kind", "CREATE CONSTRAINT c FOR (n:L) REQUIRE n.p IS SHINY", 1, 46, "unexpected token"},
		{"node key missing key", "CREATE CONSTRAINT c FOR (n:L) REQUIRE n.p IS NODE", 1, 50, "KEY"},
		{"drop missing name", "DROP INDEX", 1, 11, "end of input"},
		{"drop unknown", "DROP TABLE t", 1, 1, "unexpected token"},
		{"show alone", "SHOW", 1, 5, "what to show"},
		{"load csv missing from", "LOAD CSV 'f' AS row RETURN row", 1, 10, "unexpected token"},
		{"load csv missing as", "LOAD CSV FROM 'f' RETURN 1", 1, 19, "unexpected token"},
		{"let without value", "MATCH (n) LET x RETURN x", 1, 17, "unexpected token"},
		{"let needs equals", "MATCH (n) LET x 1 RETURN x", 1, 17, "unexpected token"},
		{"filter without condition", "MATCH (n) FILTER", 1, 17, "end of input"},
		{"finish not last", "MATCH (n) FINISH RETURN n", 1, 18, "FINISH must be the last"},
		{"ends with let", "MATCH (n) LET x = 1", 1, 11, "cannot conclude with LET"},
		{"ends with filter", "MATCH (n) FILTER n.x", 1, 11, "cannot conclude with FILTER"},
		{"extract removed", "RETURN extract(x IN [1, 2] | x * 2)", 1, 8, "use a list comprehension"},
		{"filter function removed", "RETURN filter(x IN [1, 2] WHERE x > 1)", 1, 8, "use a list comprehension"},
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

func TestParse_ExtractAsPlainFunctionStillParses(t *testing.T) {
	// Only the removed `extract(x IN …)` / `filter(x IN …)` shapes are rejected.
	for _, src := range []string{"RETURN extract(l)", "RETURN filter(a, b)", "RETURN n.extract"} {
		if _, err := Parse(src); err != nil {
			t.Errorf("Parse(%q): %v", src, err)
		}
	}
}
