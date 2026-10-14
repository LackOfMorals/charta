// Package skills holds the agent skills the server hands out, as
// Markdown documents in the Agent Skills format (SKILL.md with YAML
// frontmatter).
package skills

import (
	"embed"
	"io/fs"
	"sort"
	"strings"
)

//go:embed */SKILL.md
var files embed.FS

// BasePlaceholder is replaced by the server's base URL when a skill is
// rendered.
const BasePlaceholder = "{{BASE_URL}}"

// Skill describes one embedded skill.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// List returns every skill, sorted by name.
func List() []Skill {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil
	}
	var out []Skill
	for _, e := range entries {
		raw, err := files.ReadFile(e.Name() + "/SKILL.md")
		if err != nil {
			continue
		}
		s := Skill{Name: e.Name()}
		for _, line := range strings.Split(frontmatter(string(raw)), "\n") {
			if v, ok := strings.CutPrefix(line, "description:"); ok {
				s.Description = strings.TrimSpace(v)
			}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Render returns the SKILL.md of the named skill with baseURL substituted
// into its examples, and whether the skill exists.
func Render(name, baseURL string) (string, bool) {
	// Names are directory names; reject anything that could be a path.
	if name == "" || strings.ContainsAny(name, `/\.`) {
		return "", false
	}
	raw, err := files.ReadFile(name + "/SKILL.md")
	if err != nil {
		return "", false
	}
	return strings.ReplaceAll(string(raw), BasePlaceholder, strings.TrimRight(baseURL, "/")), true
}

// frontmatter returns the YAML block between the leading "---" lines.
func frontmatter(doc string) string {
	rest, ok := strings.CutPrefix(doc, "---\n")
	if !ok {
		return ""
	}
	fm, _, _ := strings.Cut(rest, "\n---")
	return fm
}
