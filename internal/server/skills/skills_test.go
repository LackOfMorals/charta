package skills

import (
	"strings"
	"testing"
)

func TestListAndRender(t *testing.T) {
	list := List()
	if len(list) != 1 || list[0].Name != "charta-query" || list[0].Description == "" {
		t.Fatalf("List() = %+v", list)
	}
	doc, ok := Render("charta-query", "https://graph.example.com/")
	if !ok {
		t.Fatal("charta-query not found")
	}
	if !strings.HasPrefix(doc, "---\nname: charta-query\n") {
		t.Errorf("missing frontmatter: %.60q", doc)
	}
	if strings.Contains(doc, BasePlaceholder) || !strings.Contains(doc, "https://graph.example.com/db/neo4j/query/v2") {
		t.Error("base URL not substituted")
	}
}

func TestRenderRejectsPaths(t *testing.T) {
	for _, n := range []string{"", "nope", "../x", "charta-query/..", `a\b`, "."} {
		if _, ok := Render(n, "http://x"); ok {
			t.Errorf("Render(%q) found a skill", n)
		}
	}
}
