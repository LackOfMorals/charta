package analyze_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/LackOfMorals/charta/cypher/analyze"
	"github.com/LackOfMorals/charta/cypher/syntax"
)

// tckCase is one concrete (outline-expanded) TCK scenario.
type tckCase struct {
	file, name  string
	queries     []string // every query the scenario runs: setup, main, control
	main        string   // the "executing query" text
	class, code string   // expected compile-time error, or "" for a valid scenario
}

var (
	reErr     = regexp.MustCompile(`Then an? (\w+) should be raised at compile time: (\w+)`)
	reDoc     = regexp.MustCompile(`(?s)(having executed|executing query|executing control query):\s*"""\s*(.*?)\s*"""`)
	rePlace   = regexp.MustCompile(`<([A-Za-z][A-Za-z0-9_]*)>`)
	reScenHdr = regexp.MustCompile(`^\s*Scenario(?: Outline)?:\s*(.*)`)
)

// parseExamples returns the rows of an Examples table as header->value maps.
func parseExamples(block string) []map[string]string {
	var header []string
	var rows []map[string]string
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if header == nil {
			header = cells
			continue
		}
		row := map[string]string{}
		for i, h := range header {
			if i < len(cells) {
				row[h] = cells[i]
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func loadTCK(t *testing.T) []tckCase {
	t.Helper()
	root := filepath.Join("..", "..", "compat", "testdata", "tck")
	var cases []tckCase
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".feature") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var scenarios []string
		for _, line := range strings.Split(string(data), "\n") {
			if reScenHdr.MatchString(line) {
				scenarios = append(scenarios, "")
			}
			if len(scenarios) > 0 {
				scenarios[len(scenarios)-1] += line + "\n"
			}
		}
		rel, _ := filepath.Rel(root, path)
		for _, sc := range scenarios {
			name := strings.TrimSpace(reScenHdr.FindStringSubmatch(strings.SplitN(sc, "\n", 2)[0])[1])
			body, examples := sc, []map[string]string{{}}
			if i := strings.Index(sc, "Examples:"); i >= 0 {
				body = sc[:i]
				examples = nil
				for _, block := range strings.Split(sc[i:], "Examples:")[1:] {
					examples = append(examples, parseExamples(block)...)
				}
			}
			if strings.Contains(body, "there exists a procedure") {
				continue // test procedures are out of scope
			}
			exp := reErr.FindStringSubmatch(body)
			for _, row := range examples {
				tc := tckCase{file: rel, name: name}
				if exp != nil {
					tc.class, tc.code = exp[1], exp[2]
				}
				for _, m := range reDoc.FindAllStringSubmatch(body, -1) {
					q := m[2]
					for k, v := range row {
						q = strings.ReplaceAll(q, "<"+k+">", v)
					}
					q = strings.TrimSpace(q)
					if rePlace.MatchString(q) {
						continue
					}
					tc.queries = append(tc.queries, q)
					if m[1] == "executing query" {
						tc.main = q
					}
				}
				cases = append(cases, tc)
			}
		}
		return nil
	})
	if err != nil {
		t.Skipf("TCK files unavailable: %v", err)
	}
	return cases
}

// check parses and analyses q, returning the compile-time class and code (or
// "", "" if it passes).
func check(t *testing.T, q string) (class, code string) {
	t.Helper()
	st, err := syntax.Parse(q)
	if err == nil {
		err = analyze.Check(st)
	}
	if err == nil {
		return "", ""
	}
	class, code, ok := analyze.Describe(err)
	if !ok {
		t.Fatalf("query %q: unexpected error type %T: %v", q, err, err)
	}
	return class, code
}

// TestTCK_ValidScenariosPass requires that no query of a scenario that does not
// expect a compile-time error is rejected: the analysis must have no false
// positives on the TCK.
func TestTCK_ValidScenariosPass(t *testing.T) {
	failed, total := 0, 0
	for _, tc := range loadTCK(t) {
		if tc.code != "" {
			continue
		}
		for _, q := range tc.queries {
			total++
			if class, code := check(t, q); code != "" {
				failed++
				if failed <= 40 {
					t.Errorf("%s [%s]: rejected with %s %s\n    %s", tc.file, tc.name, class, code, q)
				}
			}
		}
	}
	t.Logf("%d queries checked, %d wrongly rejected", total, failed)
}

// TestTCK_CompileTimeErrors requires every scenario that expects a compile-time
// error to produce that class and code. The analysis does not model procedures
// or runtime checks, so scenarios in those groups are not asserted.
func TestTCK_CompileTimeErrors(t *testing.T) {
	var wrong, total int
	missed := map[string]int{}
	for _, tc := range loadTCK(t) {
		if tc.code == "" || tc.main == "" {
			continue
		}
		total++
		class, code := check(t, tc.main)
		if class == tc.class && code == tc.code {
			continue
		}
		wrong++
		missed[tc.class+" "+tc.code]++
		if wrong <= 60 {
			t.Errorf("%s [%s]: want %s %s, got %q %q\n    %s", tc.file, tc.name, tc.class, tc.code, class, code, tc.main)
		}
	}
	t.Logf("%d compile-time-error cases, %d wrong: %v", total, wrong, missed)
}
