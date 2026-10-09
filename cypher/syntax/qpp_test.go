package syntax

import (
	"errors"
	"strings"
	"testing"
)

func TestParse_QuantifiedPathPatterns(t *testing.T) {
	tests := []struct{ src, want string }{
		// quantified relationships (examples from the Neo4j manual)
		{"MATCH (n:Stop)-[:NEXT]->{1,10}(m:Stop) RETURN n", "MATCH (n:Stop)-[:NEXT]->{1,10}(m:Stop) RETURN n"},
		{"MATCH (a)--+(b) RETURN a", "MATCH (a)--{1,}(b) RETURN a"},
		{"MATCH (a)-->*(b) RETURN a", "MATCH (a)-->{0,}(b) RETURN a"},
		{"MATCH (a)-[:LINK]-+(b) RETURN a", "MATCH (a)-[:LINK]-{1,}(b) RETURN a"},
		{"MATCH (a)<-[:LINK]-*(b) RETURN a", "MATCH (a)<-[:LINK]-{0,}(b) RETURN a"},
		{"MATCH (a)-[l:LINK WHERE l.distance < 10]-+(b) RETURN a", "MATCH (a)-[l:LINK WHERE (chain (. l distance) < 10)]-{1,}(b) RETURN a"},
		{"MATCH (a)-[:R]->{3}(b) RETURN a", "MATCH (a)-[:R]->{3,3}(b) RETURN a"},
		{"MATCH (a)-[:R]->{2,}(b) RETURN a", "MATCH (a)-[:R]->{2,}(b) RETURN a"},
		{"MATCH (a)-[:R]->{,4}(b) RETURN a", "MATCH (a)-[:R]->{0,4}(b) RETURN a"},
		{"MATCH (a)-[:R]->{1,3}(b)-[:S]->+(c) RETURN a", "MATCH (a)-[:R]->{1,3}(b)-[:S]->{1,}(c) RETURN a"},
		// the legacy range still works and is not a quantifier
		{"MATCH (a)-[:R*1..3]->(b) RETURN a", "MATCH (a)-[:R*1..3]->(b) RETURN a"},

		// quantified path patterns
		{"MATCH (a) ((x)-[:R]->(y)){1,3} (b) RETURN a", "MATCH (a)((x)-[:R]->(y)){1,3}(b) RETURN a"},
		{"MATCH ((:Stop)-[:NEXT]->(:Stop)){1,3} RETURN 1", "MATCH ((:Stop)-[:NEXT]->(:Stop)){1,3} RETURN 1"},
		{"MATCH (a) ((x)-->(y))+ (b) RETURN a", "MATCH (a)((x)-->(y)){1,}(b) RETURN a"},
		{"MATCH (a) ((x)-->(y))* (b) RETURN a", "MATCH (a)((x)-->(y)){0,}(b) RETURN a"},
		{"MATCH (a) ((x)-->(y)) (b) RETURN a", "MATCH (a)((x)-->(y))(b) RETURN a"},
		{"MATCH (a)((x)-[:L]-(y WHERE y.v > x.v))+(b) RETURN a", "MATCH (a)((x)-[:L]-(y WHERE (chain (. y v) > (. x v)))){1,}(b) RETURN a"},
		{"MATCH (a) ((x)-[:L]-(y) WHERE y.v > x.v)+ (b) RETURN a", "MATCH (a)((x)-[:L]-(y) WHERE (chain (. y v) > (. x v))){1,}(b) RETURN a"},
		{"MATCH (a) (((x)-->(y))+ (z))+ (b) RETURN a", "MATCH (a)(((x)-->(y)){1,}(z)){1,}(b) RETURN a"},
		{"MATCH (a)-->(b) ((x)-->(y))+ (c)<--(d) RETURN a", "MATCH (a)-->(b)((x)-->(y)){1,}(c)<--(d) RETURN a"},
		{"MATCH p = (a) ((x)-->(y)){2} (b) RETURN p", "MATCH p=(a)((x)-->(y)){2,2}(b) RETURN p"},

		// path selectors (examples from the Neo4j manual)
		{"MATCH path = ANY (:Station {name: 'P'})-[l:LINK WHERE l.distance < 10]-+(b:Station) RETURN path", `MATCH path=<ANY> (:Station {name:"P"})-[l:LINK WHERE (chain (. l distance) < 10)]-{1,}(b:Station) RETURN path`},
		{"MATCH p = SHORTEST 1 (a:Station)-[:LINK]-+(b:Station) RETURN p", "MATCH p=<SHORTEST k=1> (a:Station)-[:LINK]-{1,}(b:Station) RETURN p"},
		{"MATCH p = ALL SHORTEST (a:Station)-[:LINK]-+(b:Station) RETURN p", "MATCH p=<ALL SHORTEST> (a:Station)-[:LINK]-{1,}(b:Station) RETURN p"},
		{"MATCH p = SHORTEST 2 GROUPS (a:Station)-[:LINK]-+(b:Station) RETURN p", "MATCH p=<SHORTEST GROUPS k=2> (a:Station)-[:LINK]-{1,}(b:Station) RETURN p"},
		{"MATCH p = ANY SHORTEST (a)-[:L]-+(b) RETURN p", "MATCH p=<ANY SHORTEST> (a)-[:L]-{1,}(b) RETURN p"},
		{"MATCH SHORTEST 3 (a)-[:L]-+(b) RETURN a", "MATCH <SHORTEST k=3> (a)-[:L]-{1,}(b) RETURN a"},
		{"MATCH SHORTEST $k (a)-[:L]-+(b) RETURN a", "MATCH <SHORTEST k=$k> (a)-[:L]-{1,}(b) RETURN a"},
		{"MATCH ANY 2 (a)-[:L]-+(b) RETURN a", "MATCH <ANY k=2> (a)-[:L]-{1,}(b) RETURN a"},
		{"MATCH ALL (a)-[:L]-+(b) RETURN a", "MATCH <ALL> (a)-[:L]-{1,}(b) RETURN a"},
		{"MATCH SHORTEST GROUPS (a)-[:L]-+(b) RETURN a", "MATCH <SHORTEST GROUPS> (a)-[:L]-{1,}(b) RETURN a"},
		// a path variable declared inside the parentheses, with pre-filter
		{"MATCH SHORTEST 1 (p = (a:Station)--+(b:Station) WHERE length(p) % 2 = 0) RETURN p", "MATCH <SHORTEST k=1> (p=(a:Station)--{1,}(b:Station) WHERE (chain (% (call length p) 2) = 0)) RETURN p"},
		// legacy function forms still parse
		{"MATCH p = shortestPath((a)-[*]-(b)) RETURN p", "MATCH p=shortestPath[(a)-[*]-(b)] RETURN p"},
		// words used as variables keep working
		{"MATCH any = (a)-->(b) RETURN any", "MATCH any=(a)-->(b) RETURN any"},
		{"MATCH (any)-->(all) RETURN any", "MATCH (any)-->(all) RETURN any"},

		// match modes
		{"MATCH DIFFERENT RELATIONSHIPS (a)-->(b) RETURN a", "MATCH (a)-->(b) RETURN a"},
		{"MATCH REPEATABLE ELEMENTS (a)-->(b)-->(c) RETURN a", "MATCH (a)-->(b)-->(c) RETURN a"},
		{"OPTIONAL MATCH REPEATABLE ELEMENTS (a)-->(b) RETURN a", "OPTIONAL MATCH (a)-->(b) RETURN a"},
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

func TestParse_MatchModes(t *testing.T) {
	m := parseOK(t, "MATCH DIFFERENT RELATIONSHIPS (a)-->(b) RETURN a").Body.(*SingleQuery).Clauses[0].(*Match)
	if m.Mode != MatchDifferentRelationships {
		t.Errorf("mode = %v", m.Mode)
	}
	m = parseOK(t, "MATCH REPEATABLE ELEMENTS (a)-->(b) RETURN a").Body.(*SingleQuery).Clauses[0].(*Match)
	if m.Mode != MatchRepeatableElements {
		t.Errorf("mode = %v", m.Mode)
	}
}

func TestParse_QuantifierValues(t *testing.T) {
	q := parseOK(t, "MATCH (a)-[:R]->{,4}(b)-[:S]->*(c) RETURN a").Body.(*SingleQuery).Clauses[0].(*Match)
	chain := q.Patterns[0].Elems
	r1, r2 := chain[1].(*RelPattern).Quant, chain[3].(*RelPattern).Quant
	if r1.Min != 0 || r1.Max == nil || *r1.Max != 4 {
		t.Errorf("{,4} = min %d max %v", r1.Min, r1.Max)
	}
	if r2.Min != 0 || r2.Max != nil {
		t.Errorf("* = min %d max %v, want 0 and unbounded", r2.Min, r2.Max)
	}
}

func TestParse_QuantifiedPatternErrors(t *testing.T) {
	tests := []struct {
		name, src string
		line, col int
		msg       string
	}{
		{"adjacent nodes", "MATCH (a)(b) RETURN a", 1, 10, "must be separated"},
		{"adjacent nodes after chain", "MATCH (a)-->(b)(c) RETURN a", 1, 16, "must be separated"},
		{"empty quantifier", "MATCH (a)-[:R]->{}(b) RETURN a", 1, 18, "unexpected token"},
		{"comma only quantifier", "MATCH (a)-[:R]->{,}(b) RETURN a", 1, 19, "unexpected token"},
		{"three-part quantifier", "MATCH (a)-[:R]->{1,2,3}(b) RETURN a", 1, 21, "unexpected token"},
		{"unterminated quantifier", "MATCH (a)-[:R]->{1,2 (b) RETURN a", 1, 22, "unexpected token"},
		{"range and quantifier", "MATCH (a)-[*1..2]->+(b) RETURN a", 1, 20, "both a *range and a quantifier"},
		{"relationship without node", "MATCH (a)-[:R]->{2} RETURN a", 1, 21, "unexpected token"},
		{"group unclosed", "MATCH (a) ((x)-->(y) RETURN a", 1, 22, "unexpected token"},
		{"group quantifier too large", "MATCH (a) ((x)-->(y)){99999999999999999999} RETURN a", 1, 23, "too large"},
		{"selector without pattern", "MATCH ANY SHORTEST RETURN 1", 1, 20, "unexpected token"},
		{"selector with bad k", "MATCH SHORTEST x (a)-->(b) RETURN a", 1, 16, "unexpected token"},
		{"groups nested too deep", "MATCH " + strings.Repeat("(", 600) + "(a)" + strings.Repeat(")", 600), 1, 0, "nested too deeply"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.src)
			var se *SyntaxError
			if !errors.As(err, &se) {
				t.Fatalf("Parse(%q) error = %v, want *SyntaxError", tc.src, err)
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
