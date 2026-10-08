package syntax

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// outlinePlaceholder matches Scenario Outline parameters such as <pattern>.
var outlinePlaceholder = regexp.MustCompile(`<[a-z_]+>`)

// tckQueries extracts the Cypher docstrings (""" blocks) from the vendored TCK
// feature files.
func tckQueries(t *testing.T) map[string][]string {
	t.Helper()
	root := filepath.Join("..", "..", "compat", "testdata", "tck")
	out := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".feature") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var cur []string
		in := false
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == `"""` {
				if in {
					out[path] = append(out[path], strings.Join(cur, "\n"))
					cur = nil
				}
				in = !in
				continue
			}
			if in {
				cur = append(cur, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Skipf("TCK files unavailable: %v", err)
	}
	return out
}

// TestParse_TCKCorpus parses every concrete query in the vendored TCK (outline
// templates with <placeholders> are skipped). All of them must parse; the
// TCK scenarios that expect a compile-time error are semantic, not syntactic,
// and are handled by the semantic-analysis iteration.
func TestParse_TCKCorpus(t *testing.T) {
	total, failed := 0, 0
	for file, qs := range tckQueries(t) {
		for _, q := range qs {
			if outlinePlaceholder.MatchString(q) {
				continue
			}
			total++
			if _, err := Parse(q); err != nil {
				failed++
				t.Logf("%s: %v\n    %s", filepath.Base(file), err, strings.ReplaceAll(q, "\n", " "))
			}
		}
	}
	t.Logf("parsed %d queries, %d failed", total, failed)
	if total == 0 {
		t.Skip("no TCK queries found")
	}
	if failed > 0 {
		t.Errorf("%d of %d TCK queries failed to parse", failed, total)
	}
}
