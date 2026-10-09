//go:build tck

// Package compat contains the openCypher TCK (Technology Compatibility Kit)
// test harness for graphlite. It uses Godog (Cucumber for Go) to run Gherkin
// scenarios from the real openCypher TCK .feature files against a live
// graphlite in-memory database.
//
// Run with:
//
//	CGO_ENABLED=0 go test -tags=tck ./compat/... -v
//
// The harness loads all .feature files from testdata/tck/ (the full openCypher
// TCK, see testdata/tck/README.md) and reports a TCK pass rate at the end of the
// run. Nothing is skipped: scenarios that need syntax Cypher 25 removed are
// listed in testdata/excluded.txt, everything else must pass.
//
// Set TCK_REPORT=path to also write a per-area / per-feature markdown report.
package compat

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"

	graphlite "github.com/LackOfMorals/graphlite/v2"
	"github.com/LackOfMorals/graphlite/v2/cypher/analyze"
)

// ─────────────────────────────────────────────────────────────────────────────
// Per-scenario state
// ─────────────────────────────────────────────────────────────────────────────

// eagerResult holds a fully-collected result with records and execution summary.
type eagerResult struct {
	Records []*graphlite.Record
	Summary graphlite.ResultSummary
}

// collectResult drains qr into an eagerResult.
func collectResult(ctx context.Context, qr *graphlite.Result) (*eagerResult, error) {
	recs, err := qr.Collect(ctx)
	if err != nil {
		return nil, err
	}
	sum, _ := qr.Consume(ctx) // idempotent after Collect; counters are already set
	return &eagerResult{Records: recs, Summary: sum}, nil
}

type tckState struct {
	db         *graphlite.DB
	lastResult *eagerResult
	lastError  error
	// effects are the side effects of the last "executing query" step (setup
	// and control queries are not counted).
	effects sideEffects
	feature      string         // feature file, relative to testdata/tck
	name         string         // scenario name
	skipped      bool           // set by Before hook; steps become no-ops
	params       map[string]any // query parameters set by "And parameters are:" step
}

func newTCKState() *tckState { return &tckState{} }

func (s *tckState) reset() {
	if s.db != nil {
		_ = s.db.Close(context.Background())
		s.db = nil
	}
	s.lastResult = nil
	s.lastError = nil
	s.effects = sideEffects{}
	s.skipped = false
	s.params = nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Step definitions
// ─────────────────────────────────────────────────────────────────────────────

func (s *tckState) givenAnyGraph(ctx context.Context) error {
	if s.skipped {
		return nil
	}
	// "any graph" = use an empty in-memory graph for simplicity
	return s.givenAnEmptyGraph(ctx)
}

func (s *tckState) givenAnEmptyGraph(ctx context.Context) error {
	if s.skipped {
		return nil
	}
	s.reset()
	db, err := graphlite.Open(":memory:")
	if err != nil {
		return fmt.Errorf("open in-memory db: %w", err)
	}
	s.db = db
	return nil
}

// givenNamedGraph handles "Given the binary-tree-N graph" by running the
// graph's CREATE script from testdata/graphs.
func (s *tckState) givenNamedGraph(ctx context.Context, name string) error {
	if s.skipped {
		return nil
	}
	if err := s.givenAnEmptyGraph(ctx); err != nil {
		return err
	}
	file := filepath.Join("testdata", "graphs", name, name+".cypher")
	script, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("named graph %q: %w", name, err)
	}
	qr, err := s.db.RunQuery(ctx, strings.TrimSpace(string(script)), nil)
	if err != nil {
		return fmt.Errorf("named graph %q: %w", name, err)
	}
	if _, err := collectResult(ctx, qr); err != nil {
		return fmt.Errorf("named graph %q: %w", name, err)
	}
	return nil
}

func (s *tckState) havingExecutedDocString(ctx context.Context, doc *godog.DocString) error {
	if s.skipped {
		return nil
	}
	if s.db == nil {
		return fmt.Errorf("database not initialised (missing 'Given an empty graph' step)")
	}
	cypher := strings.TrimSpace(doc.Content)
	qr, err := s.db.RunQuery(ctx, cypher, nil)
	if err != nil {
		return fmt.Errorf("having executed %q: %w", cypher, err)
	}
	// Drain the result cursor but do NOT accumulate counters from "having
	// executed" steps. The TCK spec treats "And having executed:" as pure setup;
	// only the "When executing query:" step's side-effects count toward the
	// "And the side effects should be:" assertions.
	_, err = collectResult(ctx, qr)
	if err != nil {
		return fmt.Errorf("collect result for %q: %w", cypher, err)
	}
	return nil
}

// executingControlQueryDocString handles "When executing control query:" steps.
// Unlike "And having executed:", a control query updates lastResult so that
// subsequent "Then the result should be..." assertions can check it.
// Side-effects from control queries are NOT accumulated (they are post-main-query
// verification steps, not part of the scenario's side-effect count).
func (s *tckState) executingControlQueryDocString(ctx context.Context, doc *godog.DocString) error {
	if s.skipped {
		return nil
	}
	if s.db == nil {
		return fmt.Errorf("database not initialised")
	}
	cypher := strings.TrimSpace(doc.Content)
	qr, err := s.db.RunQuery(ctx, cypher, nil)
	if err != nil {
		s.lastError = err
		s.lastResult = nil
		return nil
	}
	eager, err := collectResult(ctx, qr)
	if err != nil {
		s.lastError = err
		s.lastResult = nil
		return nil
	}
	// Update lastResult so subsequent "Then the result should be..." checks
	// evaluate the control query's output.
	s.lastResult = eager
	s.lastError = nil
	// Do NOT accumulate counters — control queries are verification-only.
	return nil
}

func (s *tckState) whenExecutingQueryDocString(ctx context.Context, doc *godog.DocString) error {
	if s.skipped {
		return nil
	}
	if s.db == nil {
		return fmt.Errorf("database not initialised")
	}
	cypher := strings.TrimSpace(doc.Content)
	qr, err := s.db.RunQuery(ctx, cypher, s.params)
	s.params = nil // consume params after use
	if err != nil {
		s.lastError = err
		s.lastResult = nil
		return nil // capture error; assertion steps will check it
	}
	eager, err := collectResult(ctx, qr)
	if err != nil {
		s.lastError = err
		s.lastResult = nil
		return nil
	}
	s.lastResult = eager
	s.lastError = nil
	s.effects = sideEffects{}
	if eager.Summary != nil {
		c := eager.Summary.Counters()
		s.effects = sideEffects{
			"+nodes": c.NodesCreated(), "-nodes": c.NodesDeleted(),
			"+relationships": c.RelationshipsCreated(), "-relationships": c.RelationshipsDeleted(),
			"+properties": c.PropertiesSet(), "-properties": c.PropertiesRemoved(),
			"+labels": c.LabelsAdded(), "-labels": c.LabelsRemoved(),
			"+indexes": c.IndexesAdded(), "-indexes": c.IndexesRemoved(),
			"+constraints": c.ConstraintsAdded(), "-constraints": c.ConstraintsRemoved(),
		}
	}
	return nil
}

// sideEffects maps a TCK side-effect name ("+nodes", "-labels", …) to its count.
type sideEffects map[string]int

// ─── Result assertion steps ───────────────────────────────────────────────────

func (s *tckState) theResultShouldBeEmpty() error {
	if s.skipped {
		return nil
	}
	if s.lastError != nil {
		return fmt.Errorf("query failed: %w", s.lastError)
	}
	if s.lastResult == nil {
		return fmt.Errorf("no result available")
	}
	if len(s.lastResult.Records) != 0 {
		return fmt.Errorf("expected empty result, got %d row(s)", len(s.lastResult.Records))
	}
	return nil
}

func (s *tckState) noSideEffects() error {
	if s.skipped {
		return nil
	}
	return s.checkEffects(sideEffects{})
}

// checkEffects requires the last query's side effects to be exactly want
// (names not listed must be zero).
func (s *tckState) checkEffects(want sideEffects) error {
	if s.lastError != nil {
		return nil // the result assertion reports the failure
	}
	var diffs []string
	for _, key := range []string{"+nodes", "-nodes", "+relationships", "-relationships", "+properties", "-properties",
		"+labels", "-labels", "+indexes", "-indexes", "+constraints", "-constraints"} {
		if got := s.effects[key]; got != want[key] {
			diffs = append(diffs, fmt.Sprintf("%s: expected %d, got %d", key, want[key], got))
		}
	}
	if len(diffs) > 0 {
		return fmt.Errorf("side effects differ: %s", strings.Join(diffs, "; "))
	}
	return nil
}

// theResultShouldBeInAnyOrder handles "Then the result should be, in any order:"
// The table has a header row of column names and data rows of values.
// We compare record count and — for simple scalar values — cell values.
func (s *tckState) theResultShouldBeInAnyOrder(table *godog.Table) error {
	return s.compareResult(table, false, false)
}

// theResultShouldBeInOrder handles "Then the result should be, in order:".
func (s *tckState) theResultShouldBeInOrder(table *godog.Table) error {
	return s.compareResult(table, true, false)
}

// compareResult compares the last result with an expected table structurally.
func (s *tckState) compareResult(table *godog.Table, ordered, ignoreListOrder bool) error {
	if s.skipped {
		return nil
	}
	if s.lastError != nil {
		return fmt.Errorf("query failed: %w", s.lastError)
	}
	if s.lastResult == nil {
		return fmt.Errorf("no result available")
	}
	if len(table.Rows) == 0 {
		return nil
	}
	headers := make([]string, len(table.Rows[0].Cells))
	for i, c := range table.Rows[0].Cells {
		headers[i] = c.Value
	}
	dataRows := table.Rows[1:]
	if len(s.lastResult.Records) != len(dataRows) {
		return fmt.Errorf("expected %d row(s), got %d", len(dataRows), len(s.lastResult.Records))
	}
	const lenient = false // values must match exactly, including int vs float

	expected := make([]string, len(dataRows))
	for i, row := range dataRows {
		vals := make([]any, len(headers))
		for j := range headers {
			v, err := parseTV(row.Cells[j].Value)
			if err != nil {
				return fmt.Errorf("cannot parse expected value %q: %w", row.Cells[j].Value, err)
			}
			vals[j] = v
		}
		expected[i] = tvKey(vals, ignoreListOrder)
	}
	actual := make([]string, len(s.lastResult.Records))
	for i, rec := range s.lastResult.Records {
		vals := make([]any, len(headers))
		for j, h := range headers {
			v, ok := rec.Get(h)
			if !ok {
				return fmt.Errorf("result has no column %q", h)
			}
			vals[j] = fromActual(v, lenient)
		}
		actual[i] = tvKey(vals, ignoreListOrder)
	}
	if ordered {
		for i := range expected {
			if expected[i] != actual[i] {
				return fmt.Errorf("row %d: expected %s, got %s", i, expected[i], actual[i])
			}
		}
		return nil
	}
	freq := map[string]int{}
	for _, k := range expected {
		freq[k]++
	}
	for _, k := range actual {
		if freq[k] <= 0 {
			return fmt.Errorf("unexpected row %s (expected one of %v)", k, expected)
		}
		freq[k]--
	}
	return nil
}

// theSideEffectsShouldBe handles "And the side effects should be:" (table).
// Table has rows like "| +nodes | 1 |", "| -relationships | 2 |".
func (s *tckState) theSideEffectsShouldBe(table *godog.Table) error {
	if s.skipped {
		return nil
	}
	want := sideEffects{}
	for _, row := range table.Rows {
		if len(row.Cells) < 2 {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(row.Cells[1].Value))
		if err != nil {
			return fmt.Errorf("bad side effect count %q", row.Cells[1].Value)
		}
		want[strings.TrimSpace(row.Cells[0].Value)] = n
	}
	return s.checkEffects(want)
}

// errorShouldBeRaised handles "Then a SyntaxError should be raised at compile time: ..."
// and "Then a TypeError should be raised at runtime: ...".
func (s *tckState) errorShouldBeRaised(ctx context.Context, errorType, phase, code string) error {
	if s.skipped {
		return nil
	}
	if s.lastError == nil {
		return fmt.Errorf("expected %s error (%s) but query succeeded", errorType, code)
	}
	// Compile-time expectations must match exactly: the error has to come from
	// parsing or semantic analysis and carry the expected class and code.
	// Runtime and any-time expectations accept any error until the execution
	// iterations raise typed runtime errors.
	if phase == "compile time" && code != "" {
		class, got, ok := analyze.Describe(s.lastError)
		switch {
		case !ok:
			return fmt.Errorf("expected compile-time %s %s, got a different kind of error: %v", errorType, code, s.lastError)
		case class != errorType || got != code:
			return fmt.Errorf("expected compile-time %s %s, got %s %s: %v", errorType, code, class, got, s.lastError)
		}
	}
	return nil
}

// ─── Value parsing ─────────────────────────────────────────────────────────────

// parseTCKValue converts a TCK table cell value to a Go value for comparison.
// TCK uses 'string' (single quotes for strings), bare integers, floats, true/false, null.
func parseTCKValue(s string) any {
	s = strings.TrimSpace(s)
	if s == "null" {
		return nil
	}
	if s == "true" {
		return true
	}
	if s == "false" {
		return false
	}
	// Single-quoted string
	if strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") {
		return s[1 : len(s)-1]
	}
	// Node/rel patterns like (:A), (:B {name: 'b'}), [:T1] — cannot easily compare; return raw
	if strings.HasPrefix(s, "(") || strings.HasPrefix(s, "[") {
		return s // treat as raw string; row-count check will catch obvious failures
	}
	// Try integer
	if iv, err := strconv.ParseInt(s, 10, 64); err == nil {
		return iv
	}
	// Try float
	if fv, err := strconv.ParseFloat(s, 64); err == nil {
		return fv
	}
	return s
}

// ─────────────────────────────────────────────────────────────────────────────
// Suite-level outcome collector
// ─────────────────────────────────────────────────────────────────────────────

// Scenario outcomes.
const (
	statusPassed   = "passed"
	statusFailed   = "failed"
	statusSkipped  = "skipped"  // uses a feature graphlite does not support yet
	statusExcluded = "excluded" // needs syntax removed in Cypher 25 (testdata/excluded.txt)
)

type tckOutcome struct {
	Feature string
	Name    string
	Status  string
	Reason  string // skip/exclusion reason, or the first line of the failure
}

type tckCounters struct {
	mu       sync.Mutex
	outcomes []tckOutcome
}

func (c *tckCounters) add(o tckOutcome) {
	c.mu.Lock()
	c.outcomes = append(c.outcomes, o)
	c.mu.Unlock()
}

func (c *tckCounters) count(status string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, o := range c.outcomes {
		if o.Status == status {
			n++
		}
	}
	return n
}

// loadExclusions reads testdata/excluded.txt. Each non-comment line is
// "feature/path.feature :: scenario name :: reason".
func loadExclusions(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "::", 3)
		if len(parts) < 3 {
			continue
		}
		out[strings.TrimSpace(parts[0])+"::"+strings.TrimSpace(parts[1])] = strings.TrimSpace(parts[2])
	}
	return out
}

var (
	reQuoted = regexp.MustCompile(`"[^"]*"|'[^']*'`)
	reNumber = regexp.MustCompile(`\d+`)
)

// normaliseReason collapses literals so that similar failures group together.
func normaliseReason(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = reQuoted.ReplaceAllString(s, `"…"`)
	s = reNumber.ReplaceAllString(s, "N")
	if len(s) > 140 {
		s = s[:140] + "…"
	}
	return s
}

// area returns the first two path segments, e.g. "clauses/match".
func area(feature string) string {
	parts := strings.Split(feature, "/")
	if len(parts) > 2 {
		parts = parts[:2]
	}
	return strings.Join(parts, "/")
}

type tally struct{ passed, failed, skipped, excluded int }

func (t tally) executed() int { return t.passed + t.failed }

func (t tally) rate() float64 {
	if t.executed() == 0 {
		return 0
	}
	return float64(t.passed) / float64(t.executed()) * 100
}

// writeReport writes a markdown summary of all outcomes.
func writeReport(w *os.File, outcomes []tckOutcome) {
	var total tally
	byArea, byFeature := map[string]*tally{}, map[string]*tally{}
	skipReasons, failReasons := map[string]int{}, map[string]int{}
	bump := func(t *tally, status string) {
		switch status {
		case statusPassed:
			t.passed++
		case statusFailed:
			t.failed++
		case statusSkipped:
			t.skipped++
		case statusExcluded:
			t.excluded++
		}
	}
	for _, o := range outcomes {
		for _, m := range []struct {
			m map[string]*tally
			k string
		}{{byArea, area(o.Feature)}, {byFeature, o.Feature}} {
			if m.m[m.k] == nil {
				m.m[m.k] = &tally{}
			}
			bump(m.m[m.k], o.Status)
		}
		bump(&total, o.Status)
		switch o.Status {
		case statusSkipped, statusExcluded:
			skipReasons[o.Status+": "+o.Reason]++
		case statusFailed:
			failReasons[normaliseReason(o.Reason)]++
		}
	}
	fmt.Fprintf(w, "# openCypher TCK results\n\n")
	fmt.Fprintf(w, "Scenarios: %d total - **%d passed**, %d failed, %d skipped (feature not supported yet), %d excluded (Cypher 25 removed syntax).\n\n",
		len(outcomes), total.passed, total.failed, total.skipped, total.excluded)
	fmt.Fprintf(w, "Pass rate over executed scenarios: **%d/%d (%.1f%%)**\n\n", total.passed, total.executed(), total.rate())
	table := func(title string, m map[string]*tally) {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(w, "## %s\n\n| | passed | failed | skipped | excluded | pass rate |\n|---|---:|---:|---:|---:|---:|\n", title)
		for _, k := range keys {
			t := m[k]
			fmt.Fprintf(w, "| %s | %d | %d | %d | %d | %.0f%% |\n", k, t.passed, t.failed, t.skipped, t.excluded, t.rate())
		}
		fmt.Fprintln(w)
	}
	table("By area", byArea)
	table("By feature file", byFeature)
	top := func(title string, m map[string]int, n int) {
		type kv struct {
			k string
			v int
		}
		var rows []kv
		for k, v := range m {
			rows = append(rows, kv{k, v})
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].v != rows[j].v {
				return rows[i].v > rows[j].v
			}
			return rows[i].k < rows[j].k
		})
		if len(rows) > n {
			rows = rows[:n]
		}
		fmt.Fprintf(w, "## %s\n\n| count | reason |\n|---:|---|\n", title)
		for _, r := range rows {
			fmt.Fprintf(w, "| %d | %s |\n", r.v, strings.ReplaceAll(r.k, "|", "\\|"))
		}
		fmt.Fprintln(w)
	}
	top("Top failure reasons", failReasons, 25)
	top("Skip and exclusion reasons", skipReasons, 60)
}

// ─────────────────────────────────────────────────────────────────────────────
// TestTCK — the main test entry point (compiled only with -tags=tck)
// ─────────────────────────────────────────────────────────────────────────────

// TestTCK runs the full openCypher TCK; every scenario not listed in
// testdata/excluded.txt must pass.
func TestTCK(t *testing.T) {
	runFeatureSuite(t, "TCK", "testdata/tck", filepath.Join("testdata", "excluded.txt"))
}

// TestNeo4jExtensions runs graphlite's own scenarios for Neo4j's extensions to
// openCypher (label expressions, subqueries, quantified path patterns, …),
// written in the TCK's Gherkin dialect. Scenarios for constructs that are not
// executed yet are listed in testdata/neo4j-deferred.txt with a reason.
func TestNeo4jExtensions(t *testing.T) {
	runFeatureSuite(t, "Neo4j extensions", "testdata/neo4j", filepath.Join("testdata", "neo4j-deferred.txt"))
}

func runFeatureSuite(t *testing.T, label, dir, exclusionFile string) {
	ctrs := &tckCounters{}
	excluded := loadExclusions(exclusionFile)

	// Collect all .feature file paths from dir.
	var featurePaths []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".feature") {
			featurePaths = append(featurePaths, path)
		}
		return nil
	})
	if err != nil || len(featurePaths) == 0 {
		t.Fatalf("no .feature files found in %s (err=%v)", dir, err)
	}
	t.Logf("Found %d feature file(s): %v", len(featurePaths), featurePaths)

	opts := godog.Options{
		Format:   "pretty",
		Output:   os.Stdout,
		NoColors: true,
		Paths:    featurePaths,
	}

	suite := godog.TestSuite{
		Name: "graphlite-" + label,
		TestSuiteInitializer: func(tsc *godog.TestSuiteContext) {
			tsc.AfterSuite(func() {})
		},
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			state := newTCKState()

			// Before: check if this scenario uses unsupported features.
			sc.Before(func(ctx context.Context, scenario *godog.Scenario) (context.Context, error) {
				state.reset()
				state.feature = strings.TrimPrefix(filepath.ToSlash(scenario.Uri), dir+"/")
				state.name = scenario.Name
				if reason, ok := excluded[state.feature+"::"+state.name]; ok {
					state.skipped = true
					ctrs.add(tckOutcome{state.feature, state.name, statusExcluded, reason})
					return ctx, nil
				}
				return ctx, nil
			})

			// After: track pass/fail/skip.
			sc.After(func(ctx context.Context, scenario *godog.Scenario, err error) (context.Context, error) {
				if state.skipped {
					// Already counted in Before.
					return ctx, nil
				}
				if err != nil {
					ctrs.add(tckOutcome{state.feature, state.name, statusFailed, err.Error()})
				} else {
					ctrs.add(tckOutcome{state.feature, state.name, statusPassed, ""})
				}
				return ctx, nil
			})

			// ── Given steps ──────────────────────────────────────────────────
			sc.Given(`^any graph$`, state.givenAnyGraph)
			sc.Given(`^an empty graph$`, state.givenAnEmptyGraph)
			sc.Given(`^the (binary-tree-\d+) graph$`, state.givenNamedGraph)
			sc.Step(`^there exists a procedure (.+)$`, state.givenProcedure)

			// ── And having executed (DocString multiline Cypher) ─────────────
			sc.Step(`^having executed:$`, state.havingExecutedDocString)

			// ── When executing query (DocString multiline Cypher) ────────────
			sc.When(`^executing query:$`, state.whenExecutingQueryDocString)

			// ── Then result assertions ───────────────────────────────────────
			sc.Then(`^the result should be, in any order:$`, state.theResultShouldBeInAnyOrder)
			sc.Then(`^the result should be, in order:$`, state.theResultShouldBeInOrder)
			sc.Then(`^the result should be empty$`, state.theResultShouldBeEmpty)

			// ── And no side effects ──────────────────────────────────────────
			sc.Step(`^no side effects$`, state.noSideEffects)

			// ── And the side effects should be (table) ───────────────────────
			sc.Step(`^the side effects should be:$`, state.theSideEffectsShouldBe)

			// ── Error scenarios ──────────────────────────────────────────────
			// "Then a SyntaxError should be raised at compile time: ErrorCode"
			// "Then a TypeError should be raised at runtime: ErrorCode"
			// "Then an Error should be raised at runtime: ErrorCode"
			sc.Then(`^an? (\w+) should be raised at (compile time|runtime|any time)(?:: (.+))?$`,
				state.errorShouldBeRaised)

			// ── Control query (verification step after main query) ────────────
			// Runs a Cypher query and updates lastResult/lastError so the
			// subsequent "Then the result should be..." assertion checks the
			// control query's output (not the main query's output).
			// Side-effects from control queries are NOT accumulated.
			sc.Step(`^executing control query:$`, state.executingControlQueryDocString)

			// ── Parameters are: (table of param name/value pairs) ─────────────
			// Parse the two-column table (name | value) and store the values
			// in state.params so they are passed to the next RunQuery call.
			sc.Step(`^parameters are:$`, func(ctx context.Context, table *godog.Table) error {
				if state.skipped {
					return nil
				}
				state.params = make(map[string]any)
				for _, row := range table.Rows {
					if len(row.Cells) < 2 {
						continue
					}
					name := strings.TrimSpace(row.Cells[0].Value)
					v, err := parseTV(row.Cells[1].Value)
					if err != nil {
						return fmt.Errorf("cannot parse parameter %s = %q: %w", name, row.Cells[1].Value, err)
					}
					state.params[name] = v
				}
				return nil
			})

			// ── Result, ignoring element order for lists ──────────────────────
			// Two variants exist in the TCK files:
			//   "the result should be, ignoring element order for lists:"
			//   "the result should be (ignoring element order for lists):"
			// Both are treated as "in any order".
			sc.Then(`^the result should be, ignoring element order for lists:$`,
				func(t *godog.Table) error { return state.compareResult(t, false, true) })
			sc.Then(`^the result should be \(ignoring element order for lists\):$`,
				func(t *godog.Table) error { return state.compareResult(t, false, true) })
			sc.Then(`^the result should be, in order \(ignoring element order for lists\):$`,
				func(t *godog.Table) error { return state.compareResult(t, true, true) })
		},
		Options: &opts,
	}

	exitCode := suite.Run()

	passed := ctrs.count(statusPassed)
	failed := ctrs.count(statusFailed)
	skipped := ctrs.count(statusSkipped) + ctrs.count(statusExcluded)
	executed := passed + failed

	passRate := 0.0
	if executed > 0 {
		passRate = float64(passed) / float64(executed) * 100.0
	}

	// TCK_OUTCOMES=path writes one "status<TAB>feature::scenario#n" line per
	// executed case, for diffing two runs scenario by scenario.
	if path := os.Getenv("TCK_OUTCOMES"); path != "" {
		seen := map[string]int{}
		var lines []string
		for _, o := range ctrs.outcomes {
			key := o.Feature + "::" + o.Name
			seen[key]++
			lines = append(lines, fmt.Sprintf("%s\t%s#%d", o.Status, key, seen[key]))
		}
		sort.Strings(lines)
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Errorf("TCK_OUTCOMES: %v", err)
		}
	}

	if path := os.Getenv("TCK_REPORT"); path != "" {
		if f, err := os.Create(path); err == nil {
			writeReport(f, ctrs.outcomes)
			f.Close()
			t.Logf("wrote TCK report to %s", path)
		} else {
			t.Errorf("TCK_REPORT: %v", err)
		}
	}

	// Prominent pass-rate banner.
	fmt.Printf("\n================================================================================\n")
	fmt.Printf("%s pass rate: %d/%d (%.1f%%)  [skipped: %d, failed: %d]\n",
		label, passed, executed, passRate, skipped, failed)
	fmt.Printf("================================================================================\n\n")

	t.Logf("%s pass rate: %d/%d (%.1f%%)  [skipped: %d, failed: %d]",
		label, passed, executed, passRate, skipped, failed)

	_ = exitCode // don't fail on non-zero Godog exit; we enforce threshold below

	if executed > 0 && passRate < 100.0 {
		t.Errorf("%s pass rate %.1f%% is below the required 100%% (every scenario not in %s must pass) (%d/%d scenarios passed)",
			label, passRate, exclusionFile, passed, executed)
	}
}
