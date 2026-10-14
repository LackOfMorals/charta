package api

import (
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// The expectations in TestToonScalars were checked against the reference
// encoder (@toon-format/toon) over a larger generated set.
func TestToonScalars(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"hello", "hello"},
		{"two words", "two words"},
		{"", `""`},
		{"true", `"true"`},
		{"null", `"null"`},
		{"42", `"42"`},
		{"007", `"007"`},
		{"1e5", `"1e5"`},
		{"a,b", `"a,b"`},
		{"a:b", `"a:b"`},
		{"-x", `"-x"`},
		{"#x", `"#x"`},
		{" x", `" x"`},
		{"x\ny", `"x\ny"`},
		{"say \"hi\"", `"say \"hi\""`},
		{"[x]", `"[x]"`},
		{"\x01", `"\u0001"`},
		{"é", "é"},
	} {
		if got := toonString(tc.in); got != tc.want {
			t.Errorf("toonString(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
	for _, tc := range []struct {
		in   any
		want string
	}{
		{int64(-7), "-7"}, {1.5, "1.5"}, {2.0, "2"}, {1e21, "1000000000000000000000"},
		{nil, "null"}, {true, "true"},
		{[]any{int64(1), "a"}, `"[1,\"a\"]"`},
		{map[string]any{"k": "<v>"}, `"{\"k\":\"<v>\"}"`},
	} {
		if got := toonCell(tc.in); got != tc.want {
			t.Errorf("toonCell(%v) = %s, want %s", tc.in, got, tc.want)
		}
	}
	if got := toonKey("p.name"); got != "p.name" {
		t.Errorf("toonKey: %s", got)
	}
	if got := toonKey("my col"); got != `"my col"` {
		t.Errorf("toonKey: %s", got)
	}
}

// toonDo posts a statement asking for TOON and returns status, content
// type and body text.
func toonDo(t *testing.T, srv string, path, body string) (int, string, string) {
	t.Helper()
	req, err := http.NewRequest("POST", srv+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth(testUser, testPass)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/toon")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Content-Type"), string(data)
}

func TestToonResults(t *testing.T) {
	srv, _ := newTestServer(t)
	const q = "/db/neo4j/query/v2"
	run := func(stmt string) (int, string) {
		code, ct, out := toonDo(t, srv.URL, q, `{"statement":"`+stmt+`"}`)
		if !strings.HasPrefix(ct, "text/toon") {
			t.Errorf("%s: content type %q", stmt, ct)
		}
		return code, out
	}
	check := func(stmt string, wantCode int, want string) {
		t.Helper()
		if code, got := run(stmt); code != wantCode || got != want {
			t.Errorf("%s\n got %d:\n%s\nwant %d:\n%s", stmt, code, got, wantCode, want)
		}
	}

	// A write reports what it changed without includeCounters.
	check(`CREATE (:P {name:'Ann', age:30, tag:'a,b'}), (:P {name:'Bob'}), (:Q {x:1})`, 202,
		"rows[0]:\ncounters:\n  nodesCreated: 3\n  propertiesSet: 5\n  labelsAdded: 3\n")

	// Flat scalars: one table, null for missing.
	check(`MATCH (p:P) RETURN p.name AS name, p.age AS age, p.tag AS tag ORDER BY name`, 202,
		"rows[2]{name,age,tag}:\n  Ann,30,\"a,b\"\n  Bob,null,null\n")

	// Nodes become columns; the union of properties, null where absent.
	code, got := run(`MATCH (p:P) RETURN p ORDER BY p.name`)
	if code != 202 {
		t.Fatalf("nodes: %d %s", code, got)
	}
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if lines[0] != "rows[2]{p._id,p._labels,p.age,p.name,p.tag}:" {
		t.Errorf("node header: %q", lines[0])
	}
	if !regexp.MustCompile(`^  "\d+",P,30,Ann,"a,b"$`).MatchString(lines[1]) ||
		!regexp.MustCompile(`^  "\d+",P,null,Bob,null$`).MatchString(lines[2]) {
		t.Errorf("node rows: %q", lines[1:])
	}

	// Relationships, OPTIONAL MATCH nulls and lists.
	check(`CREATE (a:A {n:1})-[:R {w:2}]->(b:B {n:2})`, 202,
		"rows[0]:\ncounters:\n  nodesCreated: 2\n  relationshipsCreated: 1\n  propertiesSet: 3\n  labelsAdded: 2\n")
	code, got = run(`MATCH (a:A)-[r:R]->(b:B) RETURN r`)
	if code != 202 || !regexp.MustCompile(`^rows\[1\]\{r\._id,r\._type,r\._start,r\._end,r\.w\}:\n  "\d+",R,"\d+","\d+",2\n$`).MatchString(got) {
		t.Errorf("relationship: %d %s", code, got)
	}
	check(`MATCH (a:A) OPTIONAL MATCH (a)-[:NOPE]->(x) RETURN a.n AS n, x.n AS xn`, 202,
		"rows[1]{n,xn}:\n  1,null\n")
	check(`RETURN [1,2] AS l, {k:'v'} AS m, 'é' AS s`, 202,
		"rows[1]{l,m,s}:\n  \"[1,2]\",\"{\\\"k\\\":\\\"v\\\"}\",é\n")

	// No rows keeps the columns.
	check(`MATCH (p:P) WHERE p.name = 'nobody' RETURN p.name AS name, p.age AS age`, 202,
		"rows[0]{name,age}:\n")
	check(`RETURN 1 AS n`, 202, "rows[1]{n}:\n  1\n")
}

func TestToonErrors(t *testing.T) {
	srv, _ := newTestServer(t)
	const q = "/db/neo4j/query/v2"

	// A runtime failure is 202 in Plain JSON but an error status here.
	code, ct, out := toonDo(t, srv.URL, q, `{"statement":"RETURN 1/0 AS x"}`)
	if code != http.StatusBadRequest || !strings.HasPrefix(ct, "text/toon") ||
		!strings.HasPrefix(out, "errors[1]{code,message}:\n  Neo.ClientError.Statement.ArithmeticError,") {
		t.Errorf("runtime error: %d %s %q", code, ct, out)
	}
	if code, _, _ := do(t, "POST", srv.URL+q, "", "application/json", `{"statement":"RETURN 1/0 AS x"}`, testUser, testPass); code != http.StatusAccepted {
		t.Errorf("plain JSON runtime error changed: %d", code)
	}
	code, _, out = toonDo(t, srv.URL, q, `{"statement":"MATCH ("}`)
	if code != http.StatusBadRequest || !strings.Contains(out, "Neo.ClientError.Statement.SyntaxError") {
		t.Errorf("syntax error: %d %q", code, out)
	}
	code, _, out = toonDo(t, srv.URL, q, `{}`)
	if code != http.StatusBadRequest || !strings.HasPrefix(out, "errors[1]{code,message}:\n") {
		t.Errorf("missing statement: %d %q", code, out)
	}
}

func TestToonTransactions(t *testing.T) {
	srv, _ := newTestServer(t)
	const q = "/db/neo4j/query/v2"

	code, _, out := toonDo(t, srv.URL, q+"/tx", `{}`)
	m := regexp.MustCompile(`(?m)^transaction:\n  id: (\S+)\n  expires: "[^"]+"\nrows\[0\]:\n$`).FindStringSubmatch(out)
	if code != http.StatusAccepted || m == nil {
		t.Fatalf("open: %d %q", code, out)
	}
	tx := q + "/tx/" + m[1]
	code, _, out = toonDo(t, srv.URL, tx, `{"statement":"CREATE (:T {n:1}) RETURN 1 AS one"}`)
	if code != http.StatusAccepted || !strings.Contains(out, "rows[1]{one}:\n  1\n") || !strings.Contains(out, "nodesCreated: 1") {
		t.Errorf("run: %d %q", code, out)
	}
	code, _, out = toonDo(t, srv.URL, tx+"/commit", ``)
	if code != http.StatusAccepted || out != "committed: true\n" {
		t.Errorf("commit: %d %q", code, out)
	}
	if _, _, out = toonDo(t, srv.URL, q, `{"statement":"MATCH (t:T) RETURN count(t) AS n"}`); out != "rows[1]{n}:\n  1\n" {
		t.Errorf("after commit: %q", out)
	}
}
