package api

import (
	"strings"
	"testing"
)

func TestQueryTypeOf(t *testing.T) {
	cases := []struct {
		stmt string
		want string
	}{
		{"MATCH (n) RETURN n", "r"},
		{"RETURN 1", "r"},
		{"SHOW INDEXES", "r"},
		{"EXPLAIN MATCH (n) RETURN n", "r"},
		{"CREATE (n:Person)", "w"},
		{"CREATE (n:Person) RETURN n", "rw"},
		{"MERGE (n:Person {name: 'x'}) SET n.age = 1", "w"},
		{"MERGE (n:Person {name: 'x'}) RETURN n", "rw"},
		{"MATCH (n) DELETE n", "w"},
		{"MATCH (n) SET n.age = 1", "w"},
		{"MATCH (n) SET n.age = 1 RETURN n", "rw"},
		{"MATCH (n:Person {name: $x}) RETURN n", "r"},
		{"CREATE INDEX FOR (p:Person) ON (p.name)", "s"},
		{"CREATE CONSTRAINT FOR (p:Person) REQUIRE p.email IS UNIQUE", "s"},
		{"DROP INDEX person_index", "s"},
		{"FOREACH (x IN [1, 2] | CREATE (n {x: x}))", "w"},
		{"CALL db.labels()", "r"},
		{"this is not cypher at all!!!", "r"}, // unparseable defaults to r
	}
	for _, c := range cases {
		if got := queryTypeOf(c.stmt); got != c.want {
			t.Errorf("queryTypeOf(%q) = %q, want %q", c.stmt, got, c.want)
		}
	}
}

func TestColumnsOf(t *testing.T) {
	for _, c := range []struct {
		stmt string
		want []string
	}{
		{"RETURN 1/0 AS x", []string{"x"}},
		{"MATCH (n) RETURN n.name, count(*) AS c", []string{"n.name", "c"}},
		{"RETURN 1 AS a UNION RETURN 2 AS a", []string{"a"}},
		{"MATCH (n) RETURN *", nil},
		{"CREATE (n)", nil},
		{"not cypher", nil},
	} {
		got := columnsOf(c.stmt)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("columnsOf(%q) = %v, want %v", c.stmt, got, c.want)
		}
	}
}
