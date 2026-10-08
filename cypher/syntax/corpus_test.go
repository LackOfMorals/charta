package syntax

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// outlinePlaceholder matches Scenario Outline parameters such as <pattern>.
var outlinePlaceholder = regexp.MustCompile(`<[a-z][a-zA-Z0-9_]*>`)

// tckQueries extracts the Cypher docstrings (""" blocks) from the TCK feature
// files, split into queries from scenarios that expect an error ("should be
// raised") and queries from all other scenarios.
func tckQueries(t *testing.T) (valid, expectErr map[string][]string) {
	t.Helper()
	root := filepath.Join("..", "..", "compat", "testdata", "tck")
	valid, expectErr = map[string][]string{}, map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".feature") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Split into scenarios; each starts at a "Scenario" line.
		var scenarios []string
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "Scenario:") || strings.HasPrefix(trimmed, "Scenario Outline:") {
				scenarios = append(scenarios, "")
			}
			if len(scenarios) > 0 {
				scenarios[len(scenarios)-1] += line + "\n"
			}
		}
		for _, sc := range scenarios {
			dest := valid
			if strings.Contains(sc, "should be raised") {
				dest = expectErr
			}
			var cur []string
			in := false
			for _, line := range strings.Split(sc, "\n") {
				if strings.TrimSpace(line) == `"""` {
					if in {
						dest[path] = append(dest[path], strings.Join(cur, "\n"))
						cur = nil
					}
					in = !in
					continue
				}
				if in {
					cur = append(cur, strings.TrimSpace(line))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Skipf("TCK files unavailable: %v", err)
	}
	return valid, expectErr
}

// TestParse_TCKCorpus parses every concrete query of the TCK scenarios that do
// not expect an error (outline templates with <placeholders> are skipped). All
// of them must parse; queries in scenarios that expect an error may be
// rejected by the parser or, for compile-time semantic errors, by the analysis
// pass, so they are only required not to panic.
func TestParse_TCKCorpus(t *testing.T) {
	valid, expectErr := tckQueries(t)
	total, failed := 0, 0
	for file, qs := range valid {
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
	for _, qs := range expectErr {
		for _, q := range qs {
			if !outlinePlaceholder.MatchString(q) {
				_, _ = Parse(q) // must not panic
			}
		}
	}
	t.Logf("parsed %d queries from non-error scenarios, %d failed", total, failed)
	if total == 0 {
		t.Skip("no TCK queries found")
	}
	if failed > 0 {
		t.Errorf("%d of %d TCK queries failed to parse", failed, total)
	}
}
