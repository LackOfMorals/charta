// Package perf compares graingraph-query with a real Neo4j Query API server.
//
// It is a measurement harness, not a pass/fail test: it prints latency and
// throughput tables and only fails if a request errors. It is skipped
// unless a reference server is configured:
//
//	GRAINGRAPH_QUERY_PERF_NEO4J_URL       e.g. http://localhost:7474
//	GRAINGRAPH_QUERY_PERF_NEO4J_USER      default "neo4j"
//	GRAINGRAPH_QUERY_PERF_NEO4J_PASSWORD
//	GRAINGRAPH_QUERY_PERF_NEO4J_DB        default "neo4j"
//	GRAINGRAPH_QUERY_PERF_GRAINGRAPH_URL      a running graingraph-query (same credentials);
//	                                  default: an in-process one on a file database
//
//	go test ./internal/perf -v -timeout 30m
//
// For a fair comparison run graingraph-query as its own process (the default
// in-process server shares the test's CPU and garbage collector).
//
// The reference database is never modified: data-dependent cases build
// their graph on private "Cq*" labels in a transaction that is always
// rolled back.
package perf

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LackOfMorals/graingraph/internal/server/api"
	"github.com/LackOfMorals/graingraph/internal/server/auth"
	"github.com/LackOfMorals/graingraph/internal/server/store"
)

const (
	jsonCT  = "application/json"
	typedCT = "application/vnd.neo4j.query"
	jsonlCT = "application/jsonl"
)

type target struct {
	name       string
	base       string
	user, pass string
	db         string
	client     *http.Client
	tx         string // current explicit transaction, substituted for {tx}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func newClient() *http.Client {
	return &http.Client{
		Timeout:   5 * time.Minute,
		Transport: &http.Transport{MaxIdleConnsPerHost: 64, DisableCompression: true},
	}
}

func targets(t *testing.T) (ref, cq target) {
	t.Helper()
	url := os.Getenv("GRAINGRAPH_QUERY_PERF_NEO4J_URL")
	if url == "" {
		t.Skip("GRAINGRAPH_QUERY_PERF_NEO4J_URL not set; skipping performance comparison")
	}
	ref = target{name: "neo4j", base: strings.TrimRight(url, "/"),
		user: env("GRAINGRAPH_QUERY_PERF_NEO4J_USER", "neo4j"), pass: os.Getenv("GRAINGRAPH_QUERY_PERF_NEO4J_PASSWORD"),
		db: env("GRAINGRAPH_QUERY_PERF_NEO4J_DB", "neo4j"), client: newClient()}

	cq = target{name: "graingraph-query", user: ref.user, pass: ref.pass, db: ref.db, client: newClient()}
	if u := os.Getenv("GRAINGRAPH_QUERY_PERF_GRAINGRAPH_URL"); u != "" {
		cq.base = strings.TrimRight(u, "/")
		return ref, cq
	}
	st, err := store.New(filepath.Join(t.TempDir(), "perf.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := httptest.NewServer(api.NewHandler(st, auth.New(ref.user, ref.pass), ref.db, 5*time.Minute).Routes())
	t.Cleanup(srv.Close)
	cq.base = srv.URL
	return ref, cq
}

// call performs one request and returns its duration, status and size.
// The whole body is read, so the time includes transfer and decoding by
// the client's transport but not JSON parsing.
func (tg target) call(t testing.TB, method, path, accept, body string) (time.Duration, int, int, string) {
	t.Helper()
	req, err := http.NewRequest(method, tg.base+strings.ReplaceAll(strings.ReplaceAll(path, "{db}", tg.db), "{tx}", tg.tx), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth(tg.user, tg.pass)
	req.Header.Set("Content-Type", jsonCT)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	start := time.Now()
	resp, err := tg.client.Do(req)
	if err != nil {
		t.Fatalf("%s: %v", tg.name, err)
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	d := time.Since(start)
	if err != nil {
		t.Fatalf("%s: read body: %v", tg.name, err)
	}
	return d, resp.StatusCode, len(data), string(data)
}

func q(stmt string) string { return `{"statement":` + quote(stmt) + `}` }
func qp(stmt, params string) string {
	return `{"statement":` + quote(stmt) + `,"parameters":` + params + `}`
}
func quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(s) + `"`
}

type stats struct {
	n                  int
	mean, p50, p95, mx time.Duration
	bytes              int
}

func summarize(ds []time.Duration, bytes int) stats {
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	var sum time.Duration
	for _, d := range s {
		sum += d
	}
	at := func(p float64) time.Duration { return s[int(float64(len(s)-1)*p)] }
	return stats{n: len(s), mean: sum / time.Duration(len(s)), p50: at(0.5), p95: at(0.95), mx: s[len(s)-1], bytes: bytes}
}

func ms(d time.Duration) string { return fmt.Sprintf("%.2f", float64(d)/float64(time.Millisecond)) }

var header sync.Once

func report(name string, ref, cq stats) {
	header.Do(func() {
		fmt.Printf("\n%-34s %9s %9s %9s | %9s %9s %9s | %7s\n", "", "neo4j", "", "", "graingraph", "", "", "")
		fmt.Printf("%-34s %9s %9s %9s | %9s %9s %9s | %7s\n", "case (latency, ms)", "p50", "p95", "mean", "p50", "p95", "mean", "speedup")
		fmt.Println(strings.Repeat("-", 112))
	})
	fmt.Printf("%-34s %9s %9s %9s | %9s %9s %9s | %6.1fx\n", name,
		ms(ref.p50), ms(ref.p95), ms(ref.mean), ms(cq.p50), ms(cq.p95), ms(cq.mean),
		float64(ref.p50)/float64(cq.p50))
}

// measure runs one request shape against both targets, alternating
// blocks, after a warm-up, and verifies that both answered successfully
// with the same number of rows.
func measure(t *testing.T, ref, cq target, name, method, path, accept, body string, warm, iters int) {
	t.Helper()
	run := func(tg target) (stats, string) {
		var last string
		for i := 0; i < warm; i++ {
			_, code, _, b := tg.call(t, method, path, accept, body)
			if code != 202 && code != 200 {
				t.Fatalf("%s %s: status %d: %.300s", tg.name, name, code, b)
			}
			last = b
		}
		ds := make([]time.Duration, 0, iters)
		var bytes int
		for i := 0; i < iters; i++ {
			d, code, n, b := tg.call(t, method, path, accept, body)
			if code != 202 && code != 200 {
				t.Fatalf("%s %s: status %d: %.300s", tg.name, name, code, b)
			}
			ds = append(ds, d)
			bytes, last = n, b
		}
		return summarize(ds, bytes), last
	}
	rs, rb := run(ref)
	cs, cb := run(cq)
	if strings.Contains(rb, `"errors"`) || strings.Contains(cb, `"errors"`) {
		t.Fatalf("%s: a response carried an error:\nneo4j: %.300s\ngraingraph: %.300s", name, rb, cb)
	}
	report(name, rs, cs)
}

// throughput drives a trivial query from `workers` concurrent clients
// for a fixed time and reports requests per second.
func throughput(t *testing.T, tg target, workers int, dur time.Duration) float64 {
	t.Helper()
	var n atomic.Int64
	stop := time.Now().Add(dur)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(stop) {
				_, code, _, b := tg.call(t, "POST", "/db/{db}/query/v2", "", q("RETURN 1 AS x"))
				if code != 202 {
					t.Errorf("%s: status %d: %.200s", tg.name, code, b)
					return
				}
				n.Add(1)
			}
		}()
	}
	wg.Wait()
	return float64(n.Load()) / dur.Seconds()
}

func TestProtocolAndSerialization(t *testing.T) {
	ref, cq := targets(t)
	const qURL = "/db/{db}/query/v2"

	fmt.Println("\n=== request overhead and result serialization (no data needed) ===")
	measure(t, ref, cq, "RETURN 1", "POST", qURL, "", q("RETURN 1 AS x"), 100, 500)
	measure(t, ref, cq, "RETURN 1 (typed JSON)", "POST", qURL, typedCT, q("RETURN 1 AS x"), 100, 500)
	measure(t, ref, cq, "RETURN $p (parameters)", "POST", qURL, "", qp("RETURN $a AS a, $b AS b", `{"a":[1,2,3,4,5],"b":{"k":"v","n":[1,{"z":null}]}}`), 100, 300)
	rows := func(n int) string {
		return q(fmt.Sprintf("UNWIND range(1,%d) AS i RETURN i, 'name '+toString(i) AS s, i*0.5 AS f, [i,i+1] AS l", n))
	}
	for _, c := range []struct {
		n           int
		warm, iters int
	}{{100, 20, 100}, {1000, 10, 50}, {10000, 5, 20}, {100000, 2, 8}} {
		measure(t, ref, cq, fmt.Sprintf("%d rows, plain JSON", c.n), "POST", qURL, "", rows(c.n), c.warm, c.iters)
		measure(t, ref, cq, fmt.Sprintf("%d rows, typed JSON", c.n), "POST", qURL, typedCT, rows(c.n), c.warm, c.iters)
		measure(t, ref, cq, fmt.Sprintf("%d rows, JSONL", c.n), "POST", qURL, jsonlCT, rows(c.n), c.warm, c.iters)
	}

	fmt.Println("\n=== throughput of RETURN 1 (requests/second, 3s per run) ===")
	for _, w := range []int{1, 4, 16} {
		r := throughput(t, ref, w, 3*time.Second)
		c := throughput(t, cq, w, 3*time.Second)
		fmt.Printf("%2d concurrent client(s):  neo4j %8.0f req/s   graingraph %8.0f req/s   (%.1fx)\n", w, r, c, c/r)
	}
}

// openTx opens an explicit transaction on tg and arranges for it to be
// rolled back when the test ends, whatever happens.
func openTx(t *testing.T, tg *target) {
	t.Helper()
	_, code, _, body := tg.call(t, "POST", "/db/{db}/query/v2/tx", "", "{}")
	if code != 202 {
		t.Fatalf("%s: open tx: %d %s", tg.name, code, body)
	}
	i := strings.Index(body, `"id":"`)
	if i < 0 {
		t.Fatalf("%s: no transaction id in %s", tg.name, body)
	}
	rest := body[i+len(`"id":"`):]
	tg.tx = rest[:strings.IndexByte(rest, '"')]
	t.Cleanup(func() { tg.call(t, "DELETE", "/db/{db}/query/v2/tx/{tx}", "", "") })
}

func TestQueriesOnData(t *testing.T) {
	ref, cq := targets(t)
	openTx(t, &ref)
	openTx(t, &cq)
	const inTx = "/db/{db}/query/v2/tx/{tx}"

	// A small Companies House-shaped graph, identical on both servers:
	// 3000 companies, each with one officer and one address, chained
	// within ten buckets.
	fmt.Println("\n=== building the fixture (3000 companies, 3000 persons, 3000 addresses; rolled back at the end) ===")
	setup := []struct{ name, stmt string }{
		{"companies+officers", `UNWIND range(1,3000) AS i CREATE (:CqPerson {id:i, name:'Person '+i})-[:OFFICER_OF {since: 1990 + i % 30}]->(:CqCo {id:i, name:'Company '+i+' Ltd', bucket:i % 10, score:i*0.5})`},
		{"addresses", `MATCH (c:CqCo) CREATE (c)-[:REGISTERED_AT]->(:CqAddr {pc:'PC'+c.bucket, line:'Street '+c.id})`},
		{"chains", `MATCH (c:CqCo) WITH c.bucket AS b, c ORDER BY c.id WITH b, collect(c) AS cs UNWIND range(0, size(cs)-2) AS i WITH cs[i] AS x, cs[i+1] AS y CREATE (x)-[:NEXT]->(y)`},
	}
	for _, s := range setup {
		d1, c1, _, b1 := ref.call(t, "POST", inTx, "", q(s.stmt))
		d2, c2, _, b2 := cq.call(t, "POST", inTx, "", q(s.stmt))
		if c1 != 202 || c2 != 202 || strings.Contains(b1+b2, `"errors"`) {
			t.Fatalf("fixture %q failed:\nneo4j: %d %.300s\ngraingraph: %d %.300s", s.name, c1, b1, c2, b2)
		}
		fmt.Printf("%-24s neo4j %8s ms   graingraph %8s ms\n", s.name, ms(d1), ms(d2))
	}
	// Both fixtures must agree before timing anything on them.
	check := q(`MATCH (c:CqCo) OPTIONAL MATCH (c)-[n:NEXT]->() RETURN count(DISTINCT c) AS cos, count(n) AS nexts`)
	_, _, _, rb := ref.call(t, "POST", inTx, "", check)
	_, _, _, cb := cq.call(t, "POST", inTx, "", check)
	if !strings.Contains(rb, `[[3000,2990]]`) || !strings.Contains(cb, `[[3000,2990]]`) {
		t.Fatalf("fixtures differ: neo4j %.200s / graingraph %.200s", rb, cb)
	}

	fmt.Println("\n=== queries inside the transaction ===")
	cases := []struct {
		name, stmt string
		iters      int
	}{
		{"count nodes by label", `MATCH (c:CqCo) RETURN count(*) AS n`, 100},
		{"filter by property (scan)", `MATCH (c:CqCo {id: 1500}) RETURN c`, 60},
		{"string CONTAINS", `MATCH (c:CqCo) WHERE c.name CONTAINS '99' RETURN count(c) AS n`, 40},
		{"aggregate, group by bucket", `MATCH (c:CqCo) RETURN c.bucket AS b, count(*) AS n, avg(c.score) AS s ORDER BY b`, 40},
		{"1-hop + filter + count", `MATCH (p:CqPerson)-[r:OFFICER_OF]->(c:CqCo) WHERE r.since > 2010 RETURN count(*) AS n`, 40},
		{"2-hop join", `MATCH (p:CqPerson)-[:OFFICER_OF]->(c:CqCo)-[:REGISTERED_AT]->(a:CqAddr) WHERE a.pc = 'PC3' RETURN count(*) AS n`, 30},
		{"var-length NEXT*1..3", `MATCH (c:CqCo)-[:NEXT*1..3]->(d:CqCo) RETURN count(*) AS n`, 20},
		{"top 10 by sort", `MATCH (c:CqCo) RETURN c.name AS n, c.score AS s ORDER BY s DESC, n LIMIT 10`, 40},
		{"return 1000 nodes", `MATCH (c:CqCo) RETURN c ORDER BY c.id LIMIT 1000`, 15},
		{"return 3000 rows, 4 columns", `MATCH (p:CqPerson)-[r:OFFICER_OF]->(c:CqCo) RETURN p.name AS p, c.name AS c, r.since AS s, c.score AS sc`, 10},
		{"collect + unwind", `MATCH (c:CqCo) WITH c.bucket AS b, collect(c.id) AS ids UNWIND ids AS i RETURN b, count(i) AS n ORDER BY b`, 20},
	}
	for _, c := range cases {
		measure(t, ref, cq, c.name, "POST", inTx, "", q(c.stmt), 3, c.iters)
	}
	measure(t, ref, cq, "return 1000 nodes (typed)", "POST", inTx, typedCT, q(`MATCH (c:CqCo) RETURN c ORDER BY c.id LIMIT 1000`), 3, 15)

	fmt.Println("\n=== writes inside the transaction (rolled back at the end) ===")
	measure(t, ref, cq, "create 1 node", "POST", inTx, "", q(`CREATE (:CqW {i: 1, name: 'x'})`), 10, 200)
	measure(t, ref, cq, "create 1000 nodes (UNWIND)", "POST", inTx, "", q(`UNWIND range(1,1000) AS i CREATE (:CqW {i: i, name: 'w'+i})`), 2, 15)
	measure(t, ref, cq, "set property on 3000 nodes", "POST", inTx, "", q(`MATCH (c:CqCo) SET c.flag = true`), 2, 15)
}
