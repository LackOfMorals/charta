package interp

import (
	"strings"
	"testing"

	"github.com/graingraph/graingraph/cypher/syntax"
)

// An equality on a property may use any expression that can be evaluated once,
// from the row, to narrow the node scan (hintable); the answer must not change.
var hintQueries = []string{
	// arithmetic and modulo, the typical UNWIND-driven lookup
	"UNWIND range(1, 30) AS i MATCH (n:N {i: i % 7 + 1}) RETURN i, n.i AS ni",
	"UNWIND range(1, 20) AS i MATCH (n:N) WHERE n.i = i * 2 RETURN i, n.i AS ni",
	"UNWIND [1, 2, 3] AS i MATCH (n:N {g: i - 1}) RETURN i, count(n) AS c ORDER BY i",
	"UNWIND range(0, 5) AS i MATCH (n:N {i: -i + 10}) RETURN n.i AS ni",
	"UNWIND range(1, 6) AS i MATCH (n:N {i: i ^ 2}) RETURN n.i AS ni",
	"UNWIND range(1, 12) AS i MATCH (n:N {f: i / 2.0}) RETURN i, n.i AS ni",
	"UNWIND range(1, 12) AS i MATCH (n:N {f: i * 0.5}) RETURN i, n.i AS ni",
	// strings
	"UNWIND range(0, 8) AS i MATCH (n:N {s: 's' + toString(i % 4)}) RETURN i, count(n) AS c ORDER BY i",
	"UNWIND ['S1', 'S2', 'zz'] AS x MATCH (n:N {s: toLower(x)}) RETURN x, count(n) AS c ORDER BY x",
	"UNWIND ['s1', 's2'] AS x MATCH (n:N {s: x + ''}) RETURN x, count(n) AS c ORDER BY x",
	"UNWIND [' s3 '] AS x MATCH (n:N {s: trim(x)}) RETURN count(n) AS c",
	// property and index access, functions
	"UNWIND [{k: 5}, {k: 9}, {k: 0}] AS m MATCH (n:N {i: m.k}) RETURN m.k AS k, n.i AS ni ORDER BY k",
	"UNWIND [[1, 2], [3, 4]] AS p MATCH (n:N {i: p[1]}) RETURN n.i AS ni ORDER BY ni",
	"UNWIND range(1, 5) AS i MATCH (n:N {i: coalesce(null, i)}) RETURN n.i AS ni",
	"UNWIND range(1, 5) AS i MATCH (n:N {i: toInteger(i * 1.9)}) RETURN i, n.i AS ni",
	"UNWIND range(1, 5) AS i MATCH (n:N {i: abs(i - 3)}) RETURN i, n.i AS ni",
	"UNWIND [[3, 4, 5]] AS l MATCH (n:N {i: size(l) + head(l)}) RETURN n.i AS ni",
	// a later pattern part reads an earlier one, and WHERE equalities
	"MATCH (a:N {i: 5}), (b:N {i: a.i + 1}) RETURN a.i AS a, b.i AS b",
	"MATCH (a:N {i: 3}) MATCH (b:N) WHERE b.i = a.i * 2 RETURN b.i AS b",
	"WITH 4 AS k MATCH (n:N) WHERE n.g = k % 5 AND n.s = 's' + toString(k % 7) RETURN n.i AS ni ORDER BY ni LIMIT 6",
	"MATCH (a:N) WHERE a.i < 6 MATCH (b:N {i: a.i * 3}) RETURN a.i AS a, b.i AS b ORDER BY a",
	"MATCH (a:N {i: 2})-[:R2]->(b) MATCH (c:N {i: b.i + 1}) RETURN b.i AS b, c.i AS c ORDER BY b",
	// parameters
	"UNWIND range(1, 4) AS i MATCH (n:N {i: $base + i}) RETURN n.i AS ni",
	// no hint: null, unbound, wrong type, not pure, errors that must stay with the candidates
	"UNWIND range(1, 3) AS i MATCH (n:N {i: null + i}) RETURN count(n) AS c",
	"MATCH (n:N {i: n.g}) RETURN n.i AS ni ORDER BY ni LIMIT 5",
	"UNWIND range(1, 4) AS i MATCH (n:N {b: i % 2 = 0}) RETURN count(n) AS c",
	"UNWIND [1] AS i MATCH (n:N {l: [i % 4, i % 2]}) RETURN count(n) AS c",
	"UNWIND range(1, 4) AS i MATCH (n:N {i: toInteger(rand() * 0 + i)}) RETURN n.i AS ni",
	"UNWIND [0] AS i MATCH (n:N {i: 1 / i}) RETURN n.i AS ni",
	"UNWIND [0] AS i MATCH (n:Nothing {i: 1 / i}) RETURN n.i AS ni",
	"UNWIND [$s] AS x MATCH (n:N {i: x * 2}) RETURN n.i AS ni",
	"UNWIND [$s] AS x MATCH (n:Nothing {i: x * 2}) RETURN n.i AS ni",
	"UNWIND [1] AS i MATCH (n:N {i: toInteger('x') + i}) RETURN n.i AS ni",
}

func TestExpressionHintsAgree(t *testing.T) {
	db := richGraph(t, 3, 900, 300)
	params := map[string]any{"base": int64(10), "s": "a"}
	before := hintsUsed.Load()
	for _, q := range hintQueries {
		hintExprDisabled = false
		got, gerr := runOrdered(t, db, q, params)
		hintExprDisabled = true
		want, werr := runOrdered(t, db, q, params)
		hintExprDisabled = false
		if gerr != werr || strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("%s\n with hints:    %.400v / %q\n without hints: %.400v / %q", q, got, gerr, want, werr)
		}
	}
	if used := hintsUsed.Load() - before; used < 20 {
		t.Fatalf("expression hints were used only %d times", used)
	}
}

// The hint is what keeps an UNWIND-driven lookup from scanning the label once
// per row.
func TestExpressionHintNarrowsTheScan(t *testing.T) {
	db := richGraph(t, 5, 1500, 100)
	for _, q := range []string{
		"UNWIND range(1, 50) AS i MATCH (n:N {i: i % 10 + 1}) RETURN count(n) AS c",
		"UNWIND range(1, 50) AS i MATCH (n:N) WHERE n.i = i * 3 RETURN count(n) AS c",
		"UNWIND range(1, 50) AS i MATCH (n:N) WHERE n.i = toInteger(i * 2.5) RETURN count(n) AS c",
	} {
		hintExprDisabled = false
		start := scanRowsRead.Load()
		got, _ := runCounted(t, db, q, nil)
		narrowed := scanRowsRead.Load() - start
		hintExprDisabled = true
		start = scanRowsRead.Load()
		want, _ := runCounted(t, db, q, nil)
		full := scanRowsRead.Load() - start
		hintExprDisabled = false
		t.Logf("%-86s read %5d rows with the hint, %6d without", q, narrowed, full)
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s: %v vs %v", q, got, want)
		}
		if narrowed*20 > full {
			t.Errorf("%s: the hint read %d rows against %d without it", q, narrowed, full)
		}
	}
}

// Only expressions that give the same value each time they are evaluated may
// be used as a hint.
func TestHintableForms(t *testing.T) {
	row0 := row{"i": int64(1), "m": map[string]any{"k": int64(2)}}
	for expr, want := range map[string]bool{
		"1": true, "'a'": true, "1.5": true, "$p": true, "i": true, "i + 1": true, "i % 7 + 1": true,
		"-i": true, "i * 2 - 1": true, "'s' + toString(i)": true, "m.k": true, "[1, 2][0]": false,
		"toLower('A')": true, "coalesce(null, i)": true, "size('abc')": true, "abs(i - 3)": true,
		"rand()": false, "toInteger(rand() * 5)": false, "randomUUID()": false, "timestamp()": false,
		"j": false, "i + j": false, "count(i)": false, "collect(i)": false, "NOT true": false,
		"i = 1": false, "[i]": false, "{a: i}": false, "apoc.text.join(['a'], ',')": false,
		"toInteger(i) + abs(-i)": true, "unknownFunc(i)": false, "date()": false,
	} {
		st, err := syntax.Parse("RETURN " + expr)
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		ret := st.Body.(*syntax.SingleQuery).Clauses[0].(*syntax.Return)
		if got := hintable(ret.Items[0].Expr, row0); got != want {
			t.Errorf("hintable(%s) = %v, want %v", expr, got, want)
		}
	}
}
