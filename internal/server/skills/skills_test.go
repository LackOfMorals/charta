package skills

import (
	"strings"
	"testing"
)

func TestListAndRender(t *testing.T) {
	list := List()
	if len(list) != 1 || list[0].Name != "graingraph-query" || list[0].Description == "" {
		t.Fatalf("List() = %+v", list)
	}
	doc, ok := Render("graingraph-query", "https://graph.example.com/")
	if !ok {
		t.Fatal("graingraph-query not found")
	}
	if !strings.HasPrefix(doc, "---\nname: graingraph-query\n") {
		t.Errorf("missing frontmatter: %.60q", doc)
	}
	if strings.Contains(doc, BasePlaceholder) || !strings.Contains(doc, "https://graph.example.com/db/neo4j/query/v2") {
		t.Error("base URL not substituted")
	}
}

func TestRenderRejectsPaths(t *testing.T) {
	for _, n := range []string{"", "nope", "../x", "graingraph-query/..", `a\b`, "."} {
		if _, ok := Render(n, "http://x"); ok {
			t.Errorf("Render(%q) found a skill", n)
		}
	}
}

func TestFrontmatterIgnoresCRLF(t *testing.T) {
	// readSkill is what turns a CRLF checkout back into the LF text that
	// frontmatter expects.
	doc := strings.ReplaceAll("---\nname: x\ndescription: d\n---\nbody", "\n", "\r\n")
	if fm := frontmatter(strings.ReplaceAll(doc, "\r\n", "\n")); !strings.Contains(fm, "description: d") {
		t.Errorf("frontmatter = %q", fm)
	}
	if got, _ := Render("graingraph-query", "http://x"); strings.Contains(got, "\r") {
		t.Error("rendered skill contains a carriage return")
	}
}
