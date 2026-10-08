package syntax

import (
	"errors"
	"strings"
	"testing"
)

func TestParse_LabelAndTypeExpressions(t *testing.T) {
	tests := []struct{ src, want string }{
		// node label expressions (examples from the Neo4j manual)
		{"MATCH (n:A&B) RETURN n", "MATCH (n:(& A B)) RETURN n"},
		{"MATCH (n:(TrainStation&BusStation)) RETURN n", "MATCH (n:(& TrainStation BusStation)) RETURN n"},
		{"MATCH (n:(TrainStation|BusStation)) RETURN n", "MATCH (n:(| TrainStation BusStation)) RETURN n"},
		{"MATCH (n:(TrainStation&BusStation)|StationGroup) RETURN n", "MATCH (n:(| (& TrainStation BusStation) StationGroup)) RETURN n"},
		{"MATCH (n:A|B) RETURN n", "MATCH (n:(| A B)) RETURN n"},
		{"MATCH (n:!A) RETURN n", "MATCH (n:(! A)) RETURN n"},
		{"MATCH (n:%) RETURN n", "MATCH (n:%) RETURN n"},
		{"MATCH (n:A&(B|C)) RETURN n", "MATCH (n:(& A (| B C))) RETURN n"},
		{"MATCH (n:!(A|B)) RETURN n", "MATCH (n:(! (| A B))) RETURN n"},
		{"MATCH (n:A&!B) RETURN n", "MATCH (n:(& A (! B))) RETURN n"},
		{"MATCH (n:!!A) RETURN n", "MATCH (n:(! (! A))) RETURN n"},
		{"MATCH (n:A:B:C) RETURN n", "MATCH (n:(& (& A B) C)) RETURN n"},
		// precedence: ! > & > |
		{"MATCH (n:A|B&C) RETURN n", "MATCH (n:(| A (& B C))) RETURN n"},
		{"MATCH (n:A&B|C&D) RETURN n", "MATCH (n:(| (& A B) (& C D))) RETURN n"},
		{"MATCH (n:!A&B) RETURN n", "MATCH (n:(& (! A) B)) RETURN n"},
		{"MATCH (n:!A|B) RETURN n", "MATCH (n:(| (! A) B)) RETURN n"},
		// label expressions with other pattern parts
		{"MATCH (n:A|B {x: 1} WHERE n.y) RETURN n", "MATCH (n:(| A B) {x:1} WHERE (. n y)) RETURN n"},
		{"MATCH (n:`odd name`&B) RETURN n", "MATCH (n:(& odd name B)) RETURN n"},
		{"MATCH (n:match|set) RETURN n", "MATCH (n:(| match set)) RETURN n"},
		// relationship type expressions
		{"MATCH (a)-[:R1|R2]->(b) RETURN a", "MATCH (a)-[:(| R1 R2)]->(b) RETURN a"},
		{"MATCH (a)-[:R1|:R2]->(b) RETURN a", "MATCH (a)-[:(| R1 R2)]->(b) RETURN a"},
		{"MATCH (a)-[r:R1|R2|R3]->(b) RETURN a", "MATCH (a)-[r:(| (| R1 R2) R3)]->(b) RETURN a"},
		{"MATCH (a)-[:!R]->(b) RETURN a", "MATCH (a)-[:(! R)]->(b) RETURN a"},
		{"MATCH (a)-[:%]->(b) RETURN a", "MATCH (a)-[:%]->(b) RETURN a"},
		{"MATCH (a)-[:(R1|R2)]-(b) RETURN a", "MATCH (a)-[:(| R1 R2)]-(b) RETURN a"},
		{"MATCH (a)-[:R1|R2*1..3]->(b) RETURN a", "MATCH (a)-[:(| R1 R2)*1..3]->(b) RETURN a"},
		{"MATCH (a)-[:!(R1|R2) WHERE true]->(b) RETURN a", "MATCH (a)-[:(! (| R1 R2)) WHERE true]->(b) RETURN a"},
		// label predicates in expressions
		{"MATCH (n) WHERE n:A&B RETURN n", "MATCH (n) WHERE (: n (& A B)) RETURN n"},
		{"MATCH (n) WHERE n:(A|B) AND n:!C RETURN n", "MATCH (n) WHERE (AND (: n (| A B)) (: n (! C))) RETURN n"},
		{"MATCH (n) WHERE n:A:B RETURN n", "MATCH (n) WHERE (: n (& A B)) RETURN n"},
		{"MATCH (n) WHERE NOT n:A|B RETURN n", "MATCH (n) WHERE (NOT (: n (| A B))) RETURN n"},
		{"MATCH (n) RETURN n:A&B AS isAB", "MATCH (n) RETURN (: n (& A B)) AS isAB"},
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

func TestParse_LabelExpressionErrors(t *testing.T) {
	tests := []struct {
		name, src string
		line, col int
		msg       string
	}{
		{"empty label", "MATCH (n:) RETURN n", 1, 10, "unexpected token"},
		{"dangling and", "MATCH (n:A&) RETURN n", 1, 12, "unexpected token"},
		{"dangling or", "MATCH (n:A|) RETURN n", 1, 12, "unexpected token"},
		{"dangling not", "MATCH (n:!) RETURN n", 1, 11, "unexpected token"},
		{"unclosed group", "MATCH (n:(A&B) RETURN n", 1, 16, "unexpected token"},
		{"empty group", "MATCH (n:()) RETURN n", 1, 11, "unexpected token"},
		{"empty relationship type", "MATCH (a)-[:]->(b) RETURN a", 1, 13, "unexpected token"},
		{"dynamic label unclosed", "MATCH (n:$(x) RETURN n", 1, 15, "unexpected token"},
		{"dynamic label without parens", "MATCH (n:$x) RETURN n", 1, 10, "unexpected token"},
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
