package cypher_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/LackOfMorals/graphlite/v2/cypher"
	"github.com/LackOfMorals/graphlite/v2/cypher/syntax"
)

// This file is the differential test between the legacy ANTLR-based
// cypher.Parse and the hand-written cypher.ParseNew. It parses a large corpus
// with both and requires them to agree on accept/reject and on the resulting
// Query, except for the divergences listed in knownDivergences.

// ─── corpus ──────────────────────────────────────────────────────────────────

var (
	outlinePlaceholder = regexp.MustCompile(`<[a-z_]+>`)
	clauseStart        = regexp.MustCompile(`(?i)^\s*(MATCH|OPTIONAL|CREATE|MERGE|RETURN|WITH|UNWIND|CALL|DELETE|DETACH|SET|REMOVE|FOREACH|UNION|EXPLAIN|PROFILE)\b`)
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// corpusTCK returns the Cypher docstrings from the vendored TCK features.
func corpusTCK(t *testing.T, root string) []string {
	var out []string
	_ = filepath.WalkDir(filepath.Join(root, "compat", "testdata", "tck"), func(path string, d os.DirEntry, err error) error {
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
					out = append(out, strings.Join(cur, "\n"))
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
	return out
}

// corpusGoTests returns every Go string literal in the repo's _test.go files
// (outside vendor/) that looks like a Cypher statement.
func corpusGoTests(t *testing.T, root string) []string {
	var out []string
	fset := token.NewFileSet()
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case "vendor", ".git", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err == nil && clauseStart.MatchString(s) {
				out = append(out, s)
			}
			return true
		})
		return nil
	})
	return out
}

// corpusGenerated builds queries from the subset of Cypher both parsers are
// meant to agree on, using a fixed seed so failures reproduce.
func corpusGenerated(n int, seed int64) []string {
	r := rand.New(rand.NewSource(seed))
	pick := func(xs ...string) string { return xs[r.Intn(len(xs))] }
	var expr func(depth int) string
	atom := func() string {
		return pick("n.age", "n.name", "m.x", "n", "m", "1", "42", "'s'", "\"d q\"", "$p", "true", "null", "3.5", "n.a.b", "[1, 2, 3]", "(n.age)", "count(*)", "n.t")
	}
	expr = func(depth int) string {
		if depth <= 0 {
			return atom()
		}
		switch r.Intn(14) {
		case 0:
			return expr(depth-1) + " " + pick("=", "<>", "<", ">", "<=", ">=") + " " + expr(depth-1)
		case 1:
			return expr(depth-1) + " " + pick("AND", "OR", "XOR") + " " + expr(depth-1)
		case 2:
			return "NOT " + expr(depth-1)
		case 3:
			return expr(depth-1) + " " + pick("+", "-") + " " + expr(depth-1)
		case 4:
			return expr(depth-1) + " " + pick("*", "/", "%", "^") + " " + expr(depth-1)
		case 5:
			return "n.name " + pick("STARTS WITH", "ENDS WITH", "CONTAINS") + " " + pick("'a'", "$p")
		case 6:
			return "n.age IN [" + atom() + ", " + atom() + "]"
		case 7:
			return "n.age IS " + pick("", "NOT ") + "NULL"
		case 8:
			return pick("count", "sum", "avg", "min", "max", "collect") + "(" + pick("", "DISTINCT ") + "n.age)"
		case 9:
			return "CASE WHEN " + expr(depth-1) + " THEN " + atom() + " ELSE " + atom() + " END"
		case 10:
			return "exists(n.name)"
		case 11:
			return "toLower(" + atom() + ")"
		case 12:
			return "n:" + pick("A", "A:B")
		default:
			return atom()
		}
	}
	node := func(v string) string {
		s := "(" + v
		if r.Intn(2) == 0 {
			s += pick(":Person", ":A:B", ":L")
		}
		if r.Intn(3) == 0 {
			s += " {" + pick("name: 'x'", "a: 1, b: $p", "tags: [1, 2]", "age: 1 + 2", "k: -1") + "}"
		}
		return s + ")"
	}
	rel := func() string {
		d := ""
		if r.Intn(2) == 0 {
			d += pick("r", "")
		}
		if r.Intn(2) == 0 {
			d += pick(":KNOWS", ":T|U", ":A|:B")
		}
		if r.Intn(4) == 0 {
			d += pick("*", "*2", "*1..3", "*..4", "*2..")
		}
		if r.Intn(5) == 0 {
			d += " {w: 1}"
		}
		l, rr := "-", "-"
		switch r.Intn(3) {
		case 0:
			rr = "->"
		case 1:
			l = "<-"
		}
		if d == "" {
			return l + rr[0:1] + rr[1:]
		}
		return l + "[" + d + "]" + rr
	}
	pattern := func() string {
		s := node("a")
		for i, k := 0, r.Intn(3); i < k; i++ {
			s += rel() + node(pick("b", "c", ""))
		}
		if r.Intn(6) == 0 {
			s = "p = " + s
		}
		return s
	}
	proj := func() string {
		var items []string
		for i, k := 0, 1+r.Intn(3); i < k; i++ {
			it := expr(1 + r.Intn(2))
			if r.Intn(3) == 0 {
				it += " AS " + pick("x", "y", "z")
			}
			items = append(items, it)
		}
		s := strings.Join(items, ", ")
		if r.Intn(4) == 0 {
			s = "DISTINCT " + s
		}
		if r.Intn(3) == 0 {
			s += " ORDER BY " + expr(1) + pick("", " DESC", " ASC")
		}
		if r.Intn(3) == 0 {
			s += " SKIP " + pick("1", "$s")
		}
		if r.Intn(3) == 0 {
			s += " LIMIT " + pick("5", "$l")
		}
		return s
	}
	var out []string
	for i := 0; i < n; i++ {
		var q string
		switch r.Intn(7) {
		case 0:
			q = "MATCH " + pattern() + " WHERE " + expr(3) + " RETURN " + proj()
		case 1:
			q = pick("MATCH ", "OPTIONAL MATCH ") + pattern() + " RETURN " + proj()
		case 2:
			q = "CREATE " + pattern()
		case 3:
			q = "MERGE " + node("n") + pick("", " ON CREATE SET n.c = 1", " ON MATCH SET n.m = "+expr(1), " ON CREATE SET n.c = 1 ON MATCH SET n.m = 2")
		case 4:
			q = "MATCH " + node("n") + " " + pick("SET n.x = "+expr(2), "SET n.x = 1, n.y = $p", "SET n += {a: 1, b: [1, 2], c: 'z'}", "REMOVE n.x", "REMOVE n:A:B", "DETACH DELETE n", "DELETE n, n.x")
		case 5:
			q = "MATCH " + pattern() + " WITH " + proj() + pick("", " WHERE "+expr(2)) + " RETURN " + pick("a", "x", "count(*)")
		default:
			q = "RETURN " + proj()
		}
		out = append(out, q)
	}
	return out
}

func dedupe(qs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, q := range qs {
		q = strings.TrimSpace(q)
		if q == "" || seen[q] || outlinePlaceholder.MatchString(q) {
			continue
		}
		seen[q] = true
		out = append(out, q)
	}
	sort.Strings(out)
	return out
}

// ─── canonical form ─────────────────────────────────────────────────────────

var rawNormalizer = strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "", "(", "", ")", "", "'", "", `"`, "")

// canon renders v deterministically. RawExpr text is compared modulo
// whitespace, parentheses and quote style: the legacy parser keeps the
// verbatim source while ParseNew renders it, and the translator only ever
// echoes that text in error messages.
func canon(v reflect.Value, sb *strings.Builder) {
	switch v.Kind() {
	case reflect.Invalid:
		sb.WriteString("nil")
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			sb.WriteString("nil")
			return
		}
		if v.Kind() == reflect.Pointer && v.Type().Elem().Name() == "RawExpr" {
			sb.WriteString("Raw(" + rawNormalizer.Replace(v.Elem().FieldByName("Text").String()) + ")")
			return
		}
		canon(v.Elem(), sb)
	case reflect.Struct:
		sb.WriteString(v.Type().Name() + "{")
		for i := 0; i < v.NumField(); i++ {
			sb.WriteString(v.Type().Field(i).Name + ":")
			canon(v.Field(i), sb)
			sb.WriteByte(';')
		}
		sb.WriteByte('}')
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			sb.WriteString("nil")
			return
		}
		sb.WriteByte('[')
		for i := 0; i < v.Len(); i++ {
			canon(v.Index(i), sb)
			sb.WriteByte(',')
		}
		sb.WriteByte(']')
	case reflect.Map:
		if v.IsNil() {
			sb.WriteString("nilmap")
			return
		}
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		sb.WriteString("map{")
		for _, k := range keys {
			sb.WriteString(k.String() + "=")
			canon(v.MapIndex(k), sb)
			sb.WriteByte(',')
		}
		sb.WriteByte('}')
	default:
		fmt.Fprintf(sb, "%#v", v.Interface())
	}
}

func canonQuery(q *cypher.Query) string {
	var sb strings.Builder
	canon(reflect.ValueOf(q), &sb)
	return sb.String()
}

// ─── known divergences ──────────────────────────────────────────────────────

// Each rule explains an intentional difference between ParseNew and the legacy
// Parse. A query that diverges must match exactly one rule.
//
// Kinds: "old-error" (ParseNew accepts what Parse rejects), "new-error"
// (ParseNew rejects what Parse accepts) and "different" (both accept with
// different results).
type divergence struct {
	kind   string
	match  func(query string) bool
	reason string
}

func has(sub string) func(string) bool {
	return func(q string) bool { return strings.Contains(q, sub) }
}

func matches(re string) func(string) bool {
	r := regexp.MustCompile(re)
	return r.MatchString
}

// hasChainedComparison reports whether the query contains a comparison chain
// such as `a < b < c`.
func hasChainedComparison(q string) bool {
	st, err := syntax.Parse(q)
	if err != nil {
		return false
	}
	found := false
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if found {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if c, ok := v.Interface().(*syntax.Comparison); ok && len(c.Ops) >= 2 {
				found = true
				return
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(st))
	return found
}

var knownDivergences = []divergence{
	{
		kind:   "different",
		match:  hasChainedComparison,
		reason: "legacy bug: in a chain a < b < c every comparison reuses the first operand (a<b AND a<c); ParseNew lowers it correctly to a<b AND b<c",
	},
	{
		kind:   "different",
		match:  matches(`\[[^\]]*\*[0-9. ]*\{`),
		reason: "legacy bug: a property map after a variable-length range makes the legacy parser lose the hop bounds (min 1, unbounded)",
	},
	{
		kind:   "old-error",
		match:  matches(`(?i)RETURN\s+(DISTINCT\s+)?[^;]*\b(LIMIT\s+\S+\s+(SKIP|OFFSET))\b`),
		reason: "ParseNew accepts LIMIT before SKIP (any order); the legacy grammar required SKIP first",
	},
	{
		kind:   "old-error",
		match:  matches(`(?i)\bOFFSET\b`),
		reason: "ParseNew accepts OFFSET as a Cypher 25 alias of SKIP",
	},
	{
		kind:   "old-error",
		match:  matches(`(?i)\bNODETACH\b`),
		reason: "ParseNew accepts NODETACH DELETE (the default behaviour)",
	},
	{
		kind:   "old-error",
		match:  matches(`(?i)\(\s*(match|set|create|delete|return|with|where|order|limit|skip|merge|remove|union|unwind|call|optional|detach|foreach|yield|by|on|asc|desc|distinct|exists|all|any|none|single|count|sum|min|max|avg|collect)\s*[):{]|(?i)RETURN\s+(match|set)\b`),
		reason: "ParseNew accepts most keywords as variable names; the legacy grammar rejected reserved words",
	},
	{
		kind:   "different",
		match:  matches(`\)\s*\.\w+\s*=`),
		reason: "legacy quirk: SET (n).prop = v leaves SetItem.Expr unset; ParseNew fills it like the unparenthesised form",
	},
}

func classify(query, kind string) (string, bool) {
	for _, d := range knownDivergences {
		if d.kind == kind && d.match(query) {
			return d.reason, true
		}
	}
	return "", false
}

// ruleHits counts how many queries each known-divergence reason explained.
var ruleHits = map[string]int{}

// ─── the test ───────────────────────────────────────────────────────────────

func TestDifferential_ParseVsParseNew(t *testing.T) {
	root := repoRoot(t)
	// GRAPHLITE_DIFF_N and GRAPHLITE_DIFF_SEED enlarge the randomised part of
	// the corpus for ad-hoc sweeps; the defaults keep the unit run quick.
	n, seed := 1500, int64(1)
	if v, err := strconv.Atoi(os.Getenv("GRAPHLITE_DIFF_N")); err == nil {
		n = v
	}
	if v, err := strconv.ParseInt(os.Getenv("GRAPHLITE_DIFF_SEED"), 10, 64); err == nil {
		seed = v
	}
	corpus := dedupe(append(append(corpusTCK(t, root), corpusGoTests(t, root)...), corpusGenerated(n, seed)...))
	t.Logf("corpus: %d distinct queries", len(corpus))

	var agree, bothErr, divergent int
	var report []string
	for _, q := range corpus {
		oldQ, oldErr := cypher.Parse(q)
		newQ, newErr := cypher.ParseNew(q)
		var kind, detail string
		switch {
		case oldErr != nil && newErr != nil:
			bothErr++
			continue
		case oldErr != nil:
			kind, detail = "old-error", oldErr.Error()
		case newErr != nil:
			kind, detail = "new-error", newErr.Error()
		default:
			a, b := canonQuery(oldQ), canonQuery(newQ)
			if a == b {
				agree++
				continue
			}
			kind, detail = "different", firstDifference(a, b)
		}
		if reason, ok := classify(q, kind); ok {
			divergent++
			ruleHits[kind+": "+reason]++
			continue
		}
		report = append(report, fmt.Sprintf("[%s] %s\n    %s", kind, strings.ReplaceAll(q, "\n", " "), detail))
	}
	t.Logf("agree=%d both-error=%d known-divergent=%d unexplained=%d", agree, bothErr, divergent, len(report))
	for reason, n := range ruleHits {
		t.Logf("  %4d  %s", n, reason)
	}
	if len(report) > 0 {
		max := len(report)
		if max > 60 {
			max = 60
		}
		t.Errorf("%d unexplained divergences (showing %d):\n%s", len(report), max, strings.Join(report[:max], "\n"))
	}
}

// firstDifference returns a short window around the first differing byte.
func firstDifference(a, b string) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	lo := i - 40
	if lo < 0 {
		lo = 0
	}
	cut := func(s string) string {
		hi := i + 80
		if hi > len(s) {
			hi = len(s)
		}
		if lo > hi {
			return ""
		}
		return s[lo:hi]
	}
	return "old: …" + cut(a) + "\n    new: …" + cut(b)
}
