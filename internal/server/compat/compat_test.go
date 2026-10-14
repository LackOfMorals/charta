// Package compat checks graingraph-query against a real Neo4j server.
//
// It sends identical Query API requests to a reference Neo4j and to an
// in-process graingraph-query and requires the responses to match: HTTP
// status, content type and body, after scrubbing values that are
// legitimately different per server (element IDs, bookmarks, timings,
// transaction IDs, error message wording).
//
// The test is skipped unless a reference server is configured:
//
//	GRAINGRAPH_QUERY_COMPAT_NEO4J_URL       e.g. http://localhost:7474
//	GRAINGRAPH_QUERY_COMPAT_NEO4J_USER      e.g. neo4j
//	GRAINGRAPH_QUERY_COMPAT_NEO4J_PASSWORD
//	GRAINGRAPH_QUERY_COMPAT_NEO4J_DB        database name (default "neo4j")
//
//	go test ./internal/compat -v
//
// The reference database is never modified: statements that write run
// inside an explicit transaction that is always rolled back, on private
// "Cq*" labels. Nothing is committed and no schema is created.
package compat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/LackOfMorals/graingraph/internal/server/api"
	"github.com/LackOfMorals/graingraph/internal/server/auth"
	"github.com/LackOfMorals/graingraph/internal/server/store"
)

const (
	jsonCT   = "application/json"
	typedCT  = "application/vnd.neo4j.query"
	typed11  = "application/vnd.neo4j.query.v1.1"
	typed12  = "application/vnd.neo4j.query.v1.2"
	jsonlCT  = "application/jsonl"
	typedJL  = "application/vnd.neo4j.query+jsonl"
	queryURL = "/db/{db}/query/v2"
)

// target is one server under test.
type target struct {
	name       string
	base       string
	user, pass string
	db         string
}

type response struct {
	Status int
	CType  string
	Body   any   // decoded JSON body (nil when empty or JSONL)
	Lines  []any // decoded JSONL lines
	Raw    string
}

func (tg target) do(t *testing.T, method, path string, c request) response {
	t.Helper()
	path = strings.ReplaceAll(path, "{db}", firstNonEmpty(c.db, tg.db))
	var body io.Reader
	if c.body != "" {
		body = strings.NewReader(c.body)
	}
	req, err := http.NewRequest(method, tg.base+path, body)
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case c.badAuth:
		req.SetBasicAuth(tg.user, tg.pass+"-wrong")
	case !c.noAuth:
		req.SetBasicAuth(tg.user, tg.pass)
	}
	if c.accept != "" {
		req.Header.Set("Accept", c.accept)
	}
	if c.contentType != "" {
		req.Header.Set("Content-Type", c.contentType)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("%s: %s %s: %v", tg.name, method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := response{Status: resp.StatusCode, Raw: string(raw)}
	out.CType, _, _ = strings.Cut(strings.ToLower(resp.Header.Get("Content-Type")), ";")
	out.CType = strings.TrimSpace(out.CType)
	text := strings.TrimSpace(string(raw))
	switch {
	case text == "":
	case strings.HasSuffix(out.CType, "jsonl"):
		for _, line := range strings.Split(text, "\n") {
			out.Lines = append(out.Lines, decode(t, tg.name, line))
		}
	default:
		out.Body = decode(t, tg.name, text)
	}
	return out
}

// decode parses JSON keeping numbers as text, so 3 and 3.0 differ.
func decode(t *testing.T, who, s string) any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("%s: response is not JSON: %v\n%s", who, err, s)
	}
	return v
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// request describes one HTTP call.
type request struct {
	accept, contentType, body string
	db                        string
	noAuth, badAuth           bool
}

// volatile keys hold values that legitimately differ between servers.
// Their presence is still compared.
var volatile = map[string]string{
	"elementId": "<id>", "startNodeElementId": "<id>", "endNodeElementId": "<id>",
	"_element_id": "<id>", "_start_node_element_id": "<id>", "_end_node_element_id": "<id>",
	"resultAvailableAfter": "<ms>", "resultConsumedAfter": "<ms>",
	"expires": "<time>",
}

// scrub replaces volatile values with placeholders so two responses can
// be compared structurally.
func scrub(v any, parentKey string) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			switch {
			case volatile[k] != "":
				out[k] = volatile[k]
			case k == "bookmarks":
				out[k] = "<bookmarks>"
			case k == "id" && parentKey == "transaction":
				out[k] = "<tx>"
			case k == "message":
				if s, _ := val.(string); s != "" {
					out[k] = "<message>"
				} else {
					out[k] = val
				}
			default:
				out[k] = scrub(val, k)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = scrub(val, parentKey)
		}
		return out
	}
	return v
}

func (r response) scrubbed() response {
	out := response{Status: r.Status, CType: r.CType}
	if r.Body != nil {
		out.Body = scrub(r.Body, "")
	}
	for _, l := range r.Lines {
		out.Lines = append(out.Lines, scrub(l, ""))
	}
	return out
}

func pretty(r response) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%d %s\n", r.Status, r.CType)
	enc := json.NewEncoder(&b)
	if r.Body != nil {
		_ = enc.Encode(r.Body)
	}
	for _, l := range r.Lines {
		_ = enc.Encode(l)
	}
	return strings.TrimSpace(b.String())
}

// requireSame fails the test when the two responses differ.
func requireSame(t *testing.T, step string, ref, got response) {
	t.Helper()
	r, g := ref.scrubbed(), got.scrubbed()
	if reflect.DeepEqual(r, g) {
		return
	}
	t.Errorf("%s: responses differ\n--- neo4j\n%s\n--- graingraph-query\n%s", step, clip(pretty(r)), clip(pretty(g)))
}

func clip(s string) string {
	if len(s) > 1500 {
		return s[:1500] + "…"
	}
	return s
}

// servers starts graingraph-query in-process and returns it with the
// reference server, both configured with the same credentials.
func servers(t *testing.T) (ref, cq target) {
	t.Helper()
	url := os.Getenv("GRAINGRAPH_QUERY_COMPAT_NEO4J_URL")
	if url == "" {
		t.Skip("GRAINGRAPH_QUERY_COMPAT_NEO4J_URL not set; skipping comparison with a real Neo4j")
	}
	ref = target{
		name: "neo4j", base: strings.TrimRight(url, "/"),
		user: firstNonEmpty(os.Getenv("GRAINGRAPH_QUERY_COMPAT_NEO4J_USER"), "neo4j"),
		pass: os.Getenv("GRAINGRAPH_QUERY_COMPAT_NEO4J_PASSWORD"),
		db:   firstNonEmpty(os.Getenv("GRAINGRAPH_QUERY_COMPAT_NEO4J_DB"), "neo4j"),
	}
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h := api.NewHandler(st, auth.New(ref.user, ref.pass), ref.db, 5*time.Minute)
	srv := httptest.NewServer(h.Routes())
	t.Cleanup(srv.Close)
	cq = target{name: "graingraph-query", base: srv.URL, user: ref.user, pass: ref.pass, db: ref.db}
	return ref, cq
}

// ---------------------------------------------------------------------
// Single requests that need no data.

type singleCase struct {
	name   string
	method string
	path   string
	req    request
	// known documents a deliberate divergence: the case still runs, but
	// a difference is logged instead of failing the test.
	known string
}

func j(stmt string) string { return `{"statement":` + quote(stmt) + `}` }

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestSingleRequests(t *testing.T) {
	ref, cq := servers(t)

	post := func(name, accept, ct, body string) singleCase {
		return singleCase{name: name, method: "POST", path: queryURL,
			req: request{accept: accept, contentType: ct, body: body}}
	}
	cases := []singleCase{
		// --- successful results, every response format
		post("literals plain", "", jsonCT, j(`RETURN 1 AS i, 2.5 AS f, 'héllo ✓' AS s, true AS b, null AS z, [1,[2,null]] AS l, {a:1,b:{c:[]}} AS m`)),
		post("literals typed", typedCT, jsonCT, j(`RETURN 1 AS i, 2.5 AS f, 'x' AS s, true AS b, null AS z, [1,2] AS l, {a:1} AS m`)),
		post("literals typed v1.1", typed11, jsonCT, j(`RETURN 1 AS i, 2.5 AS f, [1] AS l`)),
		post("literals typed v1.2", typed12, jsonCT, j(`RETURN 1 AS i, 2.5 AS f, [1] AS l`)),
		post("temporal plain", "", jsonCT, j(`RETURN date('2020-02-29') AS d, datetime('2020-01-02T03:04:05Z') AS dt, localtime('10:11:12') AS lt, duration('P1Y2M3DT4H') AS du`)),
		post("temporal typed", typedCT, jsonCT, j(`RETURN date('2020-02-29') AS d, datetime('2020-01-02T03:04:05Z') AS dt, localtime('10:11:12') AS lt, duration('P1Y2M3DT4H') AS du`)),
		post("point plain", "", jsonCT, j(`RETURN point({x:1.0, y:2.0}) AS p`)),
		post("point typed", typedCT, jsonCT, j(`RETURN point({x:1.0, y:2.0}) AS p`)),
		post("empty result", "", jsonCT, j(`UNWIND [] AS x RETURN x`)),
		post("many columns and rows", "", jsonCT, j(`UNWIND range(1,5) AS i RETURN i, i*i AS sq, toString(i) AS s`)),
		post("counters on a read", "", jsonCT, `{"statement":"RETURN 1 AS x","includeCounters":true}`),
		post("jsonl", jsonlCT, jsonCT, j(`UNWIND range(1,3) AS i RETURN i`)),
		{name: "jsonl typed", method: "POST", path: queryURL,
			req:   request{accept: typedJL, contentType: jsonCT, body: j(`UNWIND range(1,2) AS i RETURN i, date('2020-01-01') AS d`)},
			known: "Neo4j 2026.07 answers 406 for Typed JSON over JSONL; graingraph-query supports it as an extension"},
		post("accept list prefers neo4j typed", typedCT+", application/json;q=0.5", jsonCT, j(`RETURN 1 AS i`)),
		post("accept wildcard", "*/*", jsonCT, j(`RETURN 1 AS i`)),
		post("query field alias absent", "", jsonCT, `{"statement":"RETURN 1 AS i","includeCounters":false}`),

		// --- parameters
		post("plain parameters", "", jsonCT, `{"statement":"UNWIND $nums AS x RETURN x*2 AS d, $name AS n, $flag AS f, $nested AS ne, $nothing AS z","parameters":{"nums":[1,2,3],"name":"héllo","flag":true,"nested":{"a":[1,{"b":null}],"c":1.5},"nothing":null}}`),
		post("typed parameters", typedCT, typedCT, `{"statement":"RETURN $i AS i, $d AS d, $f AS f, $l AS l","parameters":{"i":{"$type":"Integer","_value":"41"},"d":{"$type":"Date","_value":"2020-02-29"},"f":{"$type":"Float","_value":"2.5"},"l":{"$type":"List","_value":[{"$type":"Integer","_value":"1"},{"$type":"String","_value":"x"}]}}}`),
		post("big integer parameter", "", jsonCT, `{"statement":"RETURN $i AS i","parameters":{"i":9007199254740993}}`),

		// --- statement errors (before any result)
		post("syntax error", "", jsonCT, j(`RETRUN 1`)),
		post("syntax error jsonl", jsonlCT, jsonCT, j(`RETRUN 1`)),
		post("syntax error typed", typedCT, jsonCT, j(`RETRUN 1`)),
		post("unknown function", "", jsonCT, j(`RETURN nope(1)`)),
		post("missing parameter", "", jsonCT, j(`RETURN $x AS x`)),
		post("undefined variable", "", jsonCT, j(`RETURN y`)),
		post("incomplete statement", "", jsonCT, j(`MATCH (n) RETURN`)),
		// --- errors while producing a result
		post("division by zero", "", jsonCT, j(`RETURN 1/0 AS x`)),
		post("division by zero jsonl", jsonlCT, jsonCT, j(`RETURN 1/0 AS x`)),

		// --- request errors
		post("empty body", "", jsonCT, ``),
		post("empty object", "", jsonCT, `{}`),
		post("empty statement", "", jsonCT, `{"statement":""}`),
		post("malformed json", "", jsonCT, `{bad`),
		{name: "statement not a string", method: "POST", path: queryURL,
			req:   request{contentType: jsonCT, body: `{"statement":1}`},
			known: "Neo4j reports a non-string statement as Statement.SyntaxError; graingraph-query as Request.Invalid"},
		post("empty statement typed", typedCT, jsonCT, `{"statement":""}`),
		post("empty statement jsonl", jsonlCT, jsonCT, `{"statement":""}`),
		post("malformed json typed", typedCT, jsonCT, `{bad`),
		post("malformed json jsonl", jsonlCT, jsonCT, `{bad`),
		post("unsupported content type jsonl", jsonlCT, "text/plain", j(`RETURN 1`)),
		{name: "unknown database typed", method: "POST", path: queryURL, req: request{accept: typedCT, db: "nosuchdb", contentType: jsonCT, body: j(`RETURN 1`)}},
		{name: "unknown database jsonl", method: "POST", path: queryURL, req: request{accept: jsonlCT, db: "nosuchdb", contentType: jsonCT, body: j(`RETURN 1`)}},
		{name: "bad credentials typed", method: "POST", path: queryURL, req: request{accept: typedCT, badAuth: true, contentType: jsonCT, body: j(`RETURN 1`)}},
		{name: "wrong method typed", method: "GET", path: queryURL, req: request{accept: typedCT}},
		{name: "commit unknown tx typed", method: "POST", path: queryURL + "/tx/doesnotexist/commit", req: request{accept: typedCT, contentType: jsonCT}},
		{name: "commit unknown tx jsonl", method: "POST", path: queryURL + "/tx/doesnotexist/commit", req: request{accept: jsonlCT, contentType: jsonCT}},
		{name: "rollback unknown tx typed", method: "DELETE", path: queryURL + "/tx/doesnotexist", req: request{accept: typedCT}},
		{name: "rollback unknown tx jsonl", method: "DELETE", path: queryURL + "/tx/doesnotexist", req: request{accept: jsonlCT}},
		post("unsupported accept", "text/html", jsonCT, j(`RETURN 1`)),
		post("unsupported content type", "", "text/plain", j(`RETURN 1`)),
		{name: "wrong method", method: "GET", path: queryURL},
		{name: "unknown database", method: "POST", path: queryURL, req: request{db: "nosuchdb", contentType: jsonCT, body: j(`RETURN 1`)}},
		{name: "bad credentials", method: "POST", path: queryURL, req: request{badAuth: true, contentType: jsonCT, body: j(`RETURN 1`)}},
		{name: "no credentials", method: "POST", path: queryURL, req: request{noAuth: true, contentType: jsonCT, body: j(`RETURN 1`)}},

		// --- transactions that do not exist
		{name: "run in unknown tx", method: "POST", path: queryURL + "/tx/doesnotexist", req: request{contentType: jsonCT, body: j(`RETURN 1`)}},
		{name: "commit unknown tx", method: "POST", path: queryURL + "/tx/doesnotexist/commit", req: request{contentType: jsonCT}},
		{name: "rollback unknown tx", method: "DELETE", path: queryURL + "/tx/doesnotexist"},
		{name: "run in unknown tx typed", method: "POST", path: queryURL + "/tx/doesnotexist", req: request{accept: typedCT, contentType: jsonCT, body: j(`RETURN 1`)}},
		{name: "run in unknown tx jsonl", method: "POST", path: queryURL + "/tx/doesnotexist", req: request{accept: jsonlCT, contentType: jsonCT, body: j(`RETURN 1`)}},
		{name: "open tx on unknown database", method: "POST", path: "/db/{db}/query/v2/tx", req: request{db: "nosuchdb", contentType: jsonCT, body: `{}`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := ref.do(t, c.method, c.path, c.req)
			g := cq.do(t, c.method, c.path, c.req)
			if c.known != "" {
				if !reflect.DeepEqual(r.scrubbed(), g.scrubbed()) {
					t.Logf("known divergence (%s)\n--- neo4j\n%s\n--- graingraph-query\n%s", c.known, clip(pretty(r.scrubbed())), clip(pretty(g.scrubbed())))
				}
				return
			}
			requireSame(t, c.name, r, g)
		})
	}
}

// ---------------------------------------------------------------------
// Transaction scenarios. Each is a sequence of steps run against both
// servers; step N of one must match step N of the other. Writes happen
// only inside transactions that end in a rollback.

type step struct {
	name   string
	method string
	path   string // "{tx}" is replaced with the transaction ID of this server
	req    request
}

// txID extracts transaction.id from a response body.
func txID(r response) string {
	find := func(v any) string {
		m, _ := v.(map[string]any)
		tx, _ := m["transaction"].(map[string]any)
		id, _ := tx["id"].(string)
		return id
	}
	if id := find(r.Body); id != "" {
		return id
	}
	// JSONL: the transaction is announced in the Summary (or Header) body.
	for _, l := range r.Lines {
		m, _ := l.(map[string]any)
		if id := find(m["_body"]); id != "" {
			return id
		}
	}
	return ""
}

// runScenario executes steps against tg. The first step must open the
// transaction. Any transaction still open at the end is rolled back.
func runScenario(t *testing.T, tg target, steps []step) []response {
	t.Helper()
	var (
		id  string
		out []response
	)
	t.Cleanup(func() {
		if id != "" {
			tg.do(t, "DELETE", queryURL+"/tx/"+id, request{}) // best effort; 404 once finished
		}
	})
	for _, s := range steps {
		r := tg.do(t, s.method, strings.ReplaceAll(s.path, "{tx}", id), s.req)
		if id == "" {
			id = txID(r)
		}
		out = append(out, r)
	}
	return out
}

// compareScenario runs steps on both servers and compares them one by
// one. known maps a step name to the reason a difference is accepted for
// it; such differences are logged, not failed.
func compareScenario(t *testing.T, ref, cq target, steps []step, known ...map[string]string) {
	t.Helper()
	accepted := map[string]string{}
	for _, k := range known {
		for name, why := range k {
			accepted[name] = why
		}
	}
	r := runScenario(t, ref, steps)
	g := runScenario(t, cq, steps)
	for i, s := range steps {
		label := fmt.Sprintf("step %d (%s)", i+1, s.name)
		if why, ok := accepted[s.name]; ok {
			if !reflect.DeepEqual(r[i].scrubbed(), g[i].scrubbed()) {
				t.Logf("%s: known divergence (%s)\n--- neo4j\n%s\n--- graingraph-query\n%s", label, why, clip(pretty(r[i].scrubbed())), clip(pretty(g[i].scrubbed())))
			}
			continue
		}
		requireSame(t, label, r[i], g[i])
	}
}

const (
	open   = "/db/{db}/query/v2/tx"
	inTx   = "/db/{db}/query/v2/tx/{tx}"
	commit = "/db/{db}/query/v2/tx/{tx}/commit"
)

func run(name, stmt string) step {
	return step{name, "POST", inTx, request{contentType: jsonCT, body: j(stmt)}}
}
func runWith(name, accept, ct, body string) step {
	return step{name, "POST", inTx, request{accept: accept, contentType: ct, body: body}}
}

// fixture is a small Companies House-like graph on private labels.
const fixture = `
CREATE (a:CqCompany {number:'001', name:'Acme Ltd', incorporated: date('2020-01-02'), active:true, score:1.5, tags:['x','y']})
      -[:REGISTERED_AT {since:2020}]->(ad:CqAddress {postCode:'AB1 2CD'}),
      (p:CqPerson {name:'Ann'})-[:OFFICER_OF {role:'director'}]->(a),
      (b:CqCompany {number:'002', name:'Bee plc', active:false}),
      (p)-[:OFFICER_OF {role:'secretary'}]->(b),
      (q:CqPerson {name:'Bob'})-[:OFFICER_OF {role:'director'}]->(b)`

func TestTransactionWithData(t *testing.T) {
	ref, cq := servers(t)
	steps := []step{
		{"open without statement", "POST", open, request{contentType: jsonCT, body: `{}`}},
		runWithCounters("create fixture", fixture+` RETURN a.name AS name`),
		run("node counts", `MATCH (n) WHERE any(l IN labels(n) WHERE l STARTS WITH 'Cq') RETURN labels(n) AS l, count(*) AS c ORDER BY l`),
		run("node return", `MATCH (c:CqCompany) RETURN c ORDER BY c.number`),
		run("relationship return", `MATCH (:CqPerson)-[r:OFFICER_OF]->(c:CqCompany) RETURN r, c.number AS n ORDER BY n, r.role`),
		run("path return", `MATCH p=(:CqPerson {name:'Ann'})-[:OFFICER_OF]->(c:CqCompany) RETURN p ORDER BY c.number`),
		run("aggregation", `MATCH (p:CqPerson)-[:OFFICER_OF]->(c:CqCompany) WITH c, p ORDER BY p.name RETURN c.name AS company, count(p) AS officers, collect(p.name) AS names ORDER BY officers DESC, company`),
		run("optional match", `MATCH (c:CqCompany) OPTIONAL MATCH (c)-[:REGISTERED_AT]->(a:CqAddress) RETURN c.number AS n, a.postCode AS pc ORDER BY n`),
		run("string functions", `MATCH (c:CqCompany) RETURN toUpper(c.name) AS u, size(c.name) AS len, c.name STARTS WITH 'A' AS sw, c.name CONTAINS 'plc' AS ct, c.name =~ '.*Ltd' AS re ORDER BY c.number`),
		run("temporal property", `MATCH (c:CqCompany) WHERE c.incorporated IS NOT NULL RETURN c.incorporated AS d, c.incorporated.year AS y, c.incorporated + duration('P1M') AS next`),
		run("null handling", `MATCH (c:CqCompany) RETURN c.number AS n, c.score AS s, coalesce(c.score, -1) AS cs, c.score IS NULL AS isnull ORDER BY n`),
		run("exists subquery", `MATCH (c:CqCompany) WHERE NOT EXISTS { (c)-[:REGISTERED_AT]->() } RETURN c.number AS n`),
		run("call subquery", `MATCH (c:CqCompany) CALL (c) { MATCH (c)<-[:OFFICER_OF]-(p:CqPerson) RETURN count(p) AS k } RETURN c.number AS n, k ORDER BY n`),
		run("union", `MATCH (c:CqCompany) RETURN count(c) AS n UNION ALL MATCH (a:CqAddress) RETURN count(a) AS n`),
		run("with filter", `MATCH (p:CqPerson)-[:OFFICER_OF]->(:CqCompany) WITH p, count(*) AS k WHERE k > 1 RETURN p.name AS name, k`),
		run("distinct skip limit", `UNWIND [3,1,2,3,1] AS x RETURN DISTINCT x ORDER BY x SKIP 1 LIMIT 1`),
		run("list comprehension", `RETURN [x IN range(1,5) WHERE x % 2 = 1 | x*x] AS odd`),
		run("case", `MATCH (c:CqCompany) RETURN c.number AS n, CASE WHEN c.active THEN 'on' ELSE 'off' END AS state ORDER BY n`),
		runWith("typed node return", typedCT, jsonCT, j(`MATCH (c:CqCompany {number:'001'}) RETURN c, c.incorporated AS d`)),
		runWith("typed path return", typedCT, jsonCT, j(`MATCH p=(:CqPerson {name:'Ann'})-[:OFFICER_OF]->(:CqCompany {number:'001'}) RETURN p`)),
		runWith("jsonl rows", jsonlCT, jsonCT, j(`MATCH (c:CqCompany) RETURN c.number AS n ORDER BY n`)),
		runWith("jsonl nodes typed", typedJL, jsonCT, j(`MATCH (c:CqCompany) RETURN c ORDER BY c.number`)),
		runWithCounters("set a label nodes already have", `MATCH (c:CqCompany) SET c:CqCompany`),
		runWithCounters("add a label to every company", `MATCH (c:CqCompany) SET c:CqListed`),
		runWithCounters("add and remove a label in one statement", `MATCH (c:CqCompany) SET c:CqTmp REMOVE c:CqTmp`),
		runWithCounters("remove a label", `MATCH (c:CqCompany) REMOVE c:CqListed`),
		runWithCounters("create then delete labelled nodes", `CREATE (n:CqA:CqB) DELETE n`),
		runWithCounters("delete labelled nodes", `MATCH (p:CqPerson) DETACH DELETE p`),
		run("set property", `MATCH (c:CqCompany {number:'001'}) SET c.score = 2.5`),
		runWithCounters("delete with counters", `MATCH (q:CqPerson {name:'Bob'}) DETACH DELETE q`),
		run("rollback is the only exit", `RETURN 1 AS ok`),
		{"rollback", "DELETE", inTx, request{}},
		run("run after rollback", `RETURN 1`),
		{"commit after rollback", "POST", commit, request{contentType: jsonCT}},
		{"rollback again", "DELETE", inTx, request{}},
	}
	compareScenario(t, ref, cq, steps, map[string]string{
		"jsonl nodes typed": "Neo4j 2026.07 answers 406 for Typed JSON over JSONL; graingraph-query supports it as an extension",
	})
}

func runWithCounters(name, stmt string) step {
	return step{name, "POST", inTx, request{contentType: jsonCT, body: `{"statement":` + quote(stmt) + `,"includeCounters":true}`}}
}

func TestTransactionOpenWithStatement(t *testing.T) {
	ref, cq := servers(t)
	compareScenario(t, ref, cq, []step{
		{"open with statement", "POST", open, request{contentType: jsonCT, body: j(`RETURN 1 AS x`)}},
		run("run", `RETURN 2 AS y`),
		{"rollback", "DELETE", inTx, request{}},
	})
}

func TestTransactionOpenWithoutStatementJSONL(t *testing.T) {
	ref, cq := servers(t)
	compareScenario(t, ref, cq, []step{
		{"open jsonl", "POST", open, request{accept: jsonlCT, contentType: jsonCT, body: `{}`}},
		{"run jsonl", "POST", inTx, request{accept: jsonlCT, contentType: jsonCT, body: j(`RETURN 1 AS x`)}},
		{"commit jsonl", "POST", commit, request{accept: jsonlCT, contentType: jsonCT, body: `{}`}},
	})
}

func TestTransactionOpenWithStatementJSONL(t *testing.T) {
	ref, cq := servers(t)
	compareScenario(t, ref, cq, []step{
		{"open with statement jsonl", "POST", open, request{accept: jsonlCT, contentType: jsonCT, body: j(`UNWIND [1,2] AS x RETURN x`)}},
		{"rollback", "DELETE", inTx, request{}},
	})
}

func TestTransactionCommit(t *testing.T) {
	ref, cq := servers(t)
	compareScenario(t, ref, cq, []step{
		{"open", "POST", open, request{contentType: jsonCT, body: `{}`}},
		run("read", `RETURN 1 AS x`),
		{"commit", "POST", commit, request{contentType: jsonCT, body: `{}`}},
		{"commit again", "POST", commit, request{contentType: jsonCT, body: `{}`}},
		run("run after commit", `RETURN 1`),
	})
}

func TestTransactionCommitWithStatement(t *testing.T) {
	ref, cq := servers(t)
	compareScenario(t, ref, cq, []step{
		{"open", "POST", open, request{contentType: jsonCT, body: `{}`}},
		{"commit with statement", "POST", commit, request{contentType: jsonCT, body: j(`RETURN 7 AS x`)}},
	})
}

func TestFailedStatementDestroysTransaction(t *testing.T) {
	for _, bad := range []struct{ name, stmt string }{
		{"syntax error", `RETRUN 1`},
		{"missing parameter", `RETURN $nope`},
	} {
		t.Run(bad.name, func(t *testing.T) {
			ref, cq := servers(t)
			compareScenario(t, ref, cq, []step{
				{"open", "POST", open, request{contentType: jsonCT, body: `{}`}},
				run("failing statement", bad.stmt),
				run("run after failure", `RETURN 1`),
				{"commit after failure", "POST", commit, request{contentType: jsonCT}},
				{"rollback after failure", "DELETE", inTx, request{}},
			})
		})
	}
}

func TestRuntimeErrorInTransaction(t *testing.T) {
	ref, cq := servers(t)
	compareScenario(t, ref, cq, []step{
		{"open", "POST", open, request{contentType: jsonCT, body: `{}`}},
		run("division by zero", `RETURN 1/0 AS x`),
		run("run after runtime error", `RETURN 1`),
		{"rollback", "DELETE", inTx, request{}},
	})
}

func TestOpenWithFailingStatement(t *testing.T) {
	ref, cq := servers(t)
	compareScenario(t, ref, cq, []step{
		{"open with bad statement", "POST", open, request{contentType: jsonCT, body: j(`RETRUN 1`)}},
	})
}

// TestScrubbing guards the comparison itself: it must still notice real
// differences and ignore only the volatile ones.
func TestScrubbing(t *testing.T) {
	a := decode(t, "a", `{"data":{"values":[[{"elementId":"4:x:1","labels":["A"],"properties":{"n":1}}]]},"bookmarks":["FB:1"],"resultAvailableAfter":3,"transaction":{"id":"abc","expires":"t1"}}`)
	b := decode(t, "b", `{"data":{"values":[[{"elementId":"9","labels":["A"],"properties":{"n":1}}]]},"bookmarks":["FB:2"],"resultAvailableAfter":9,"transaction":{"id":"zzz","expires":"t2"}}`)
	if !reflect.DeepEqual(scrub(a, ""), scrub(b, "")) {
		t.Error("volatile values should be ignored")
	}
	for name, body := range map[string]string{
		"property value":   `{"data":{"values":[[{"elementId":"9","labels":["A"],"properties":{"n":2}}]]},"bookmarks":["FB:2"],"resultAvailableAfter":9,"transaction":{"id":"zzz","expires":"t2"}}`,
		"missing bookmark": `{"data":{"values":[[{"elementId":"9","labels":["A"],"properties":{"n":1}}]]},"resultAvailableAfter":9,"transaction":{"id":"zzz","expires":"t2"}}`,
		"missing tx":       `{"data":{"values":[[{"elementId":"9","labels":["A"],"properties":{"n":1}}]]},"bookmarks":["FB:2"],"resultAvailableAfter":9}`,
		"int vs float":     `{"data":{"values":[[{"elementId":"9","labels":["A"],"properties":{"n":1.0}}]]},"bookmarks":["FB:2"],"resultAvailableAfter":9,"transaction":{"id":"zzz","expires":"t2"}}`,
	} {
		if reflect.DeepEqual(scrub(a, ""), scrub(decode(t, name, body), "")) {
			t.Errorf("%s: difference was scrubbed away", name)
		}
	}
	e1 := decode(t, "e1", `{"errors":[{"code":"C","message":"one wording"}]}`)
	e2 := decode(t, "e2", `{"errors":[{"code":"C","message":"another wording"}]}`)
	e3 := decode(t, "e3", `{"errors":[{"code":"D","message":"another wording"}]}`)
	if !reflect.DeepEqual(scrub(e1, ""), scrub(e2, "")) || reflect.DeepEqual(scrub(e1, ""), scrub(e3, "")) {
		t.Error("error messages should be ignored but error codes compared")
	}
}

// Counters follow Neo4j's query statistics: each change counted as it happens,
// not netted out. These statements run in sequence on a shared fixture, so the
// state each one sees is the same on both servers.
func TestStatementStatistics(t *testing.T) {
	ref, cq := servers(t)
	steps := []step{
		{"open", "POST", open, request{contentType: jsonCT, body: `{}`}},
		runWithCounters("create fixture", fixture),
		runWithCounters("set a property to its current value", `MATCH (c:CqCompany) SET c.name = c.name`),
		runWithCounters("set a key twice", `MATCH (c:CqCompany) SET c.name = 'X', c.name = 'Y'`),
		runWithCounters("remove a property", `MATCH (c:CqCompany {number: '001'}) REMOVE c.score`),
		runWithCounters("remove a property that is absent", `MATCH (c:CqCompany) REMOVE c.nothing`),
		runWithCounters("set a property to null", `MATCH (c:CqCompany) SET c.zz = null`),
		runWithCounters("set null on an existing property", `MATCH (c:CqCompany {number: '001'}) SET c.tags = null`),
		runWithCounters("merge a map, including a null", `MATCH (c:CqCompany {number: '001'}) SET c += {note: 'n', name: null}`),
		runWithCounters("replace all properties", `MATCH (c:CqCompany {number: '002'}) SET c = {number: '002', fresh: true}`),
		runWithCounters("node created and deleted in one statement", `CREATE (n:CqTmp {a: 1, b: null}) DELETE n`),
		runWithCounters("node created, changed and deleted", `CREATE (n:CqTmp {a: 1}) SET n.b = 2 REMOVE n.a DELETE n`),
		runWithCounters("relationship property set to its value", `MATCH ()-[r:REGISTERED_AT]->() SET r.since = r.since`),
		runWithCounters("relationship property removed", `MATCH ()-[r:REGISTERED_AT]->() REMOVE r.since`),
		runWithCounters("relationship created", `MATCH (a:CqCompany {number: '001'}) CREATE (a)-[:CqTmpRel {w: 1}]->(a)`),
		runWithCounters("relationship deleted", `MATCH ()-[r:CqTmpRel]->() DELETE r`),
		runWithCounters("relationship created and deleted", `MATCH (a:CqCompany {number: '001'}) CREATE (a)-[r:CqTmpRel {w: 1}]->(a) DELETE r`),
		runWithCounters("detach delete people", `MATCH (p:CqPerson) DETACH DELETE p`),
		runWithCounters("detach delete companies", `MATCH (c:CqCompany) DETACH DELETE c`),
		runWithCounters("merge that creates", `MERGE (n:CqM {k: 1}) ON CREATE SET n.c = 5`),
		runWithCounters("merge that matches", `MERGE (n:CqM {k: 1}) ON CREATE SET n.c = 5`),
		{"rollback", "DELETE", inTx, request{}},
	}
	compareScenario(t, ref, cq, steps)
}
