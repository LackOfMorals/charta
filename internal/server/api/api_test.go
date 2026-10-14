package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LackOfMorals/graingraph/internal/server/auth"
	"github.com/LackOfMorals/graingraph/internal/server/store"
)

const (
	testUser = "alice"
	testPass = "secret"
)

// newTestServer starts an in-memory server with credentials alice:secret.
func newTestServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h := NewHandler(st, auth.New(testUser, testPass), "neo4j", 5*time.Minute)
	srv := httptest.NewServer(h.Routes())
	t.Cleanup(srv.Close)
	return srv, st
}

// do performs a basic-auth request and returns the status and decoded
// JSON body (when the body is non-empty).
func do(t *testing.T, method, url, accept, contentType, body, user, pass string) (int, map[string]any, http.Header) {
	t.Helper()
	var rd *bytes.Reader
	if body != "" {
		rd = bytes.NewReader([]byte(body))
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var out map[string]any
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("decode body %q: %v", data, err)
		}
	}
	return resp.StatusCode, out, resp.Header
}

// errCode and errMsg read the first entry of a Neo4j error document,
// {"errors":[{"code":...,"message":...}]}; they return "" when absent.
func errCode(body map[string]any) string { return errField(body, "code") }
func errMsg(body map[string]any) string  { return errField(body, "message") }

func errField(body map[string]any, key string) string {
	errs, _ := body["errors"].([]any)
	if len(errs) == 0 {
		return ""
	}
	first, _ := errs[0].(map[string]any)
	s, _ := first[key].(string)
	return s
}

// fieldsAndValues pulls data.fields and data.values out of a response.
func fieldsAndValues(t *testing.T, body map[string]any) ([]any, [][]any) {
	t.Helper()
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("response has no data object: %v", body)
	}
	fields, _ := data["fields"].([]any)
	raw, _ := data["values"].([]any)
	values := make([][]any, len(raw))
	for i, row := range raw {
		rr, ok := row.([]any)
		if !ok {
			t.Fatalf("row %d is not an array: %v", i, row)
		}
		values[i] = rr
	}
	return fields, values
}

func TestAuthRequired(t *testing.T) {
	srv, _ := newTestServer(t)

	code, body, hdr := do(t, "POST", srv.URL+"/db/neo4j/query/v2", "", "application/json", `{"statement":"RETURN 1"}`, "", "")
	if code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", code)
	}
	if errCode(body) != "Neo.ClientError.Security.Unauthorized" {
		t.Fatalf("code = %v, want Unauthorized", errCode(body))
	}
	if hdr.Get("WWW-Authenticate") == "" {
		t.Fatal("missing WWW-Authenticate header")
	}

	code, body, _ = do(t, "POST", srv.URL+"/db/neo4j/query/v2", "", "application/json", `{"statement":"RETURN 1"}`, testUser, "wrong")
	if code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for bad password", code)
	}

	// Discovery is unauthenticated.
	code, body, _ = do(t, "GET", srv.URL+"/", "", "", "", "", "")
	if code != http.StatusOK {
		t.Fatalf("discovery status = %d, want 200", code)
	}
	if v, _ := body["query_api_versions"].([]any); len(v) == 0 || v[0] != "2.0" {
		t.Fatalf("query_api_versions = %v", body["query_api_versions"])
	}
}

func TestQueryPlain(t *testing.T) {
	srv, _ := newTestServer(t)

	body := fmt.Sprintf(`{"statement":"CREATE (n:Person {name: $name, age: $age}) RETURN n AS p","parameters":{"name":"Alice","age":42}}`)
	code, resp, hdr := do(t, "POST", srv.URL+"/db/neo4j/query/v2", "", "application/json", body, testUser, testPass)
	if code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%v", code, resp)
	}
	if hdr.Get("Content-Type") != "application/json" {
		t.Fatalf("content type = %q", hdr.Get("Content-Type"))
	}
	if resp["queryType"] != "rw" {
		t.Errorf("queryType = %v, want rw", resp["queryType"])
	}
	if bms, _ := resp["bookmarks"].([]any); len(bms) == 0 || !strings.HasPrefix(bms[0].(string), "FB:") {
		t.Errorf("bookmarks = %v", resp["bookmarks"])
	}
	fields, values := fieldsAndValues(t, resp)
	if len(fields) != 1 || fields[0] != "p" {
		t.Fatalf("fields = %v", fields)
	}
	node, ok := values[0][0].(map[string]any)
	if !ok {
		t.Fatalf("value is not an object: %v", values[0][0])
	}
	if node["labels"] == nil || node["properties"] == nil {
		t.Fatalf("node missing labels/properties: %v", node)
	}
	if node["elementId"] == "" {
		t.Fatal("node missing elementId")
	}

	// Read it back.
	code, resp, _ = do(t, "POST", srv.URL+"/db/neo4j/query/v2", "", "application/json",
		`{"statement":"MATCH (p:Person {name: $n}) RETURN p.name AS name, p.age AS age","parameters":{"n":"Alice"}}`, testUser, testPass)
	if code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}
	if resp["queryType"] != "r" {
		t.Errorf("queryType = %v, want r", resp["queryType"])
	}
	fields, values = fieldsAndValues(t, resp)
	if len(fields) != 2 || fields[0] != "name" || fields[1] != "age" {
		t.Fatalf("fields = %v", fields)
	}
	if values[0][0] != "Alice" || values[0][1] != float64(42) {
		t.Fatalf("values = %v", values[0])
	}
}

func TestQueryTypedJSON(t *testing.T) {
	srv, _ := newTestServer(t)

	code, resp, hdr := do(t, "POST", srv.URL+"/db/neo4j/query/v2",
		"application/vnd.neo4j.query.v1.2", "application/json",
		`{"statement":"RETURN 1 AS i, 2.5 AS f, 'x' AS s, true AS b, null AS n, [1, 2] AS l, {a: 1} AS m"}`, testUser, testPass)
	if code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%v", code, resp)
	}
	if hdr.Get("Content-Type") != "application/vnd.neo4j.query.v1.2" {
		t.Fatalf("content type = %q", hdr.Get("Content-Type"))
	}
	_, values := fieldsAndValues(t, resp)
	row := values[0]
	want := []string{"Integer", "Float", "String", "Boolean", "Null", "List", "Map"}
	for i, typ := range want {
		tv, ok := row[i].(map[string]any)
		if !ok {
			t.Fatalf("value %d not an object: %v", i, row[i])
		}
		if tv["$type"] != typ {
			t.Errorf("value %d $type = %v, want %s", i, tv["$type"], typ)
		}
	}
	if row[0].(map[string]any)["_value"] != "1" {
		t.Errorf("integer _value = %v, want \"1\"", row[0].(map[string]any)["_value"])
	}
	if row[1].(map[string]any)["_value"] != "2.5" {
		t.Errorf("float _value = %v, want \"2.5\"", row[1].(map[string]any)["_value"])
	}
}

func TestQueryTypesPlain(t *testing.T) {
	srv, _ := newTestServer(t)

	stmt := `RETURN date('2015-03-26') AS d, duration({days: 14}) AS dur, point({x: 1.2, y: 3.4}) AS pt`
	code, resp, _ := do(t, "POST", srv.URL+"/db/neo4j/query/v2", "", "application/json",
		fmt.Sprintf(`{"statement":%q}`, stmt), testUser, testPass)
	if code != http.StatusAccepted {
		t.Fatalf("status = %d; body=%v", code, resp)
	}
	_, values := fieldsAndValues(t, resp)
	if values[0][0] != "2015-03-26" {
		t.Errorf("date = %v", values[0][0])
	}
	if values[0][1] != "P14D" {
		t.Errorf("duration = %v", values[0][1])
	}
	if values[0][2] != "SRID=7203;POINT (1.2 3.4)" {
		t.Errorf("point = %v", values[0][2])
	}
}

func TestPathPlain(t *testing.T) {
	srv, _ := newTestServer(t)
	do(t, "POST", srv.URL+"/db/neo4j/query/v2", "", "application/json",
		`{"statement":"CREATE (a:Person {name:'A'})-[:KNOWS]->(b:Person {name:'B'})"}`, testUser, testPass)

	code, resp, _ := do(t, "POST", srv.URL+"/db/neo4j/query/v2", "", "application/json",
		`{"statement":"MATCH p=(a:Person {name:'A'})-[:KNOWS]->(b:Person) RETURN p"}`, testUser, testPass)
	if code != http.StatusAccepted {
		t.Fatalf("status = %d; body=%v", code, resp)
	}
	_, values := fieldsAndValues(t, resp)
	path, ok := values[0][0].([]any)
	if !ok || len(path) != 3 {
		t.Fatalf("path = %v (want 3 elements)", values[0][0])
	}
	if _, ok := path[1].(map[string]any); !ok {
		t.Fatalf("middle element is not a relationship object: %v", path[1])
	}
	rel := path[1].(map[string]any)
	if rel["type"] != "KNOWS" {
		t.Errorf("rel type = %v", rel["type"])
	}
}

func TestJSONLStream(t *testing.T) {
	srv, _ := newTestServer(t)

	req, _ := http.NewRequest("POST", srv.URL+"/db/neo4j/query/v2",
		strings.NewReader(`{"statement":"CREATE (n:Person {name:'A'}) RETURN n.name AS name UNION CREATE (n:Person {name:'B'}) RETURN n.name AS name"}`))
	req.SetBasicAuth(testUser, testPass)
	req.Header.Set("Accept", "application/jsonl")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/jsonl" {
		t.Fatalf("content type = %q", ct)
	}

	var lines []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 4 { // Header + 2 Records + Summary
		t.Fatalf("got %d lines: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], `"$event":"Header"`) || !strings.Contains(lines[0], `"fields":["name"]`) {
		t.Fatalf("header line: %s", lines[0])
	}
	if !strings.Contains(lines[1], `"$event":"Record"`) || !strings.Contains(lines[1], `"A"`) {
		t.Fatalf("record line: %s", lines[1])
	}
	if !strings.Contains(lines[3], `"$event":"Summary"`) || !strings.Contains(lines[3], `"bookmarks"`) {
		t.Fatalf("summary line: %s", lines[3])
	}
}

func TestJSONLErrorEvent(t *testing.T) {
	srv, _ := newTestServer(t)

	req, _ := http.NewRequest("POST", srv.URL+"/db/neo4j/query/v2",
		strings.NewReader(`{"statement":"MATCH (n) RETURN"}`))
	req.SetBasicAuth(testUser, testPass)
	req.Header.Set("Accept", "application/jsonl")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var lines []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	last := lines[len(lines)-1]
	if !strings.Contains(last, `"$event":"Error"`) {
		t.Fatalf("last line is not an Error event: %s", last)
	}
	if !strings.Contains(last, CodeSyntaxError) {
		t.Fatalf("error code missing: %s", last)
	}
}

func TestTransactionFlow(t *testing.T) {
	srv, _ := newTestServer(t)

	// Open with a statement.
	code, resp, _ := do(t, "POST", srv.URL+"/db/neo4j/query/v2/tx", "", "application/json",
		`{"statement":"CREATE (n:Person {name:'InTx'}) RETURN n.name AS name"}`, testUser, testPass)
	if code != http.StatusAccepted {
		t.Fatalf("open status = %d; body=%v", code, resp)
	}
	tx, ok := resp["transaction"].(map[string]any)
	if !ok {
		t.Fatalf("no transaction object: %v", resp)
	}
	id := tx["id"].(string)
	if _, values := fieldsAndValues(t, resp); len(values) == 0 || values[0][0] != "InTx" {
		t.Fatalf("open values = %v", resp)
	}

	// Run another statement in the transaction.
	code, resp, _ = do(t, "POST", srv.URL+"/db/neo4j/query/v2/tx/"+id, "", "application/json",
		`{"statement":"MATCH (n:Person {name:'InTx'}) SET n.age = 30 RETURN n.age AS age"}`, testUser, testPass)
	if code != http.StatusAccepted {
		t.Fatalf("run status = %d; body=%v", code, resp)
	}
	_, vals := fieldsAndValues(t, resp)
	if vals[0][0] != float64(30) {
		t.Fatalf("run values = %v", vals[0][0])
	}

	// Commit.
	code, resp, _ = do(t, "POST", srv.URL+"/db/neo4j/query/v2/tx/"+id+"/commit", "", "application/json", "", testUser, testPass)
	if code != http.StatusAccepted {
		t.Fatalf("commit status = %d; body=%v", code, resp)
	}
	bms, _ := resp["bookmarks"].([]any)
	if len(bms) == 0 {
		t.Fatalf("commit response missing bookmarks: %v", resp)
	}

	// Data is now visible.
	_, probe, _ := do(t, "POST", srv.URL+"/db/neo4j/query/v2", "", "application/json",
		`{"statement":"MATCH (n:Person) RETURN count(n) AS c"}`, testUser, testPass)
	_, vals = fieldsAndValues(t, probe)
	if vals[0][0] != float64(1) {
		t.Fatalf("post-commit count = %v", vals[0][0])
	}

	// Committing again: unknown transaction.
	code, resp, _ = do(t, "POST", srv.URL+"/db/neo4j/query/v2/tx/"+id+"/commit", "", "application/json", "", testUser, testPass)
	if code != http.StatusNotFound {
		t.Fatalf("re-commit status = %d, want 404; body=%v", code, resp)
	}
	if errCode(resp) != CodeRequestInvalid {
		t.Fatalf("re-commit code = %v", errCode(resp))
	}
}

func TestTransactionRollback(t *testing.T) {
	srv, _ := newTestServer(t)

	_, resp, _ := do(t, "POST", srv.URL+"/db/neo4j/query/v2/tx", "", "application/json",
		`{"statement":"CREATE (n:Person {name:'Doomed'})"}`, testUser, testPass)
	id := resp["transaction"].(map[string]any)["id"].(string)

	code, resp, _ := do(t, "DELETE", srv.URL+"/db/neo4j/query/v2/tx/"+id, "", "", "", testUser, testPass)
	if code != http.StatusOK {
		t.Fatalf("rollback status = %d; body=%v", code, resp)
	}
	_, probe, _ := do(t, "POST", srv.URL+"/db/neo4j/query/v2", "", "application/json",
		`{"statement":"MATCH (n:Person) RETURN count(n) AS c"}`, testUser, testPass)
	_, vals := fieldsAndValues(t, probe)
	if vals[0][0] != float64(0) {
		t.Fatalf("rolled-back data visible: %v", vals[0][0])
	}
}

func TestTxQueryErrorDestroysTransaction(t *testing.T) {
	srv, _ := newTestServer(t)

	_, resp, _ := do(t, "POST", srv.URL+"/db/neo4j/query/v2/tx", "", "application/json", "", testUser, testPass)
	id := resp["transaction"].(map[string]any)["id"].(string)

	// As in Neo4j, a failing statement answers 400 and destroys the transaction.
	code, resp, _ := do(t, "POST", srv.URL+"/db/neo4j/query/v2/tx/"+id, "", "application/json",
		`{"statement":"MATCH (n) RETURN"}`, testUser, testPass)
	if code != http.StatusBadRequest || errCode(resp) != CodeSyntaxError {
		t.Fatalf("status = %d code = %q, want 400 %s; body=%v", code, errCode(resp), CodeSyntaxError, resp)
	}
	code, resp, _ = do(t, "POST", srv.URL+"/db/neo4j/query/v2/tx/"+id, "", "application/json",
		`{"statement":"RETURN 1"}`, testUser, testPass)
	if code != http.StatusNotFound {
		t.Fatalf("run after failure: status = %d, want 404; body=%v", code, resp)
	}
	// The database is free again.
	if code, resp, _ = do(t, "POST", srv.URL+"/db/neo4j/query/v2", "", "application/json",
		`{"statement":"RETURN 1"}`, testUser, testPass); code != http.StatusAccepted {
		t.Fatalf("query after failed tx: status = %d; body=%v", code, resp)
	}
}

func TestQueryErrors(t *testing.T) {
	srv, _ := newTestServer(t)

	// Syntax error: 400 with a Neo4j-style error document.
	code, resp, _ := do(t, "POST", srv.URL+"/db/neo4j/query/v2", "", "application/json",
		`{"statement":"MATCH (n) RETURN"}`, testUser, testPass)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if errCode(resp) != CodeSyntaxError {
		t.Fatalf("code = %v", errCode(resp))
	}
	if errMsg(resp) == "" {
		t.Fatal("missing message")
	}

	// Missing parameter.
	code, resp, _ = do(t, "POST", srv.URL+"/db/neo4j/query/v2", "", "application/json",
		`{"statement":"RETURN $missing"}`, testUser, testPass)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if errCode(resp) != CodeParameterMissing {
		t.Fatalf("code = %v", errCode(resp))
	}
	if !strings.Contains(errMsg(resp), "missing") {
		t.Fatalf("message = %v", errMsg(resp))
	}
}

func TestConstraintViolation(t *testing.T) {
	srv, _ := newTestServer(t)
	do(t, "POST", srv.URL+"/db/neo4j/query/v2", "", "application/json",
		`{"statement":"CREATE CONSTRAINT FOR (p:Person) REQUIRE p.email IS UNIQUE"}`, testUser, testPass)
	do(t, "POST", srv.URL+"/db/neo4j/query/v2", "", "application/json",
		`{"statement":"CREATE (p:Person {email:'a@b.c'})"}`, testUser, testPass)

	code, resp, _ := do(t, "POST", srv.URL+"/db/neo4j/query/v2", "", "application/json",
		`{"statement":"CREATE (p:Person {email:'a@b.c'})"}`, testUser, testPass)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if errCode(resp) != CodeConstraint {
		t.Fatalf("code = %v", errCode(resp))
	}
}

func TestBadRequestBody(t *testing.T) {
	srv, _ := newTestServer(t)

	// Malformed JSON.
	code, resp, _ := do(t, "POST", srv.URL+"/db/neo4j/query/v2", "", "application/json",
		`{"statement": }`, testUser, testPass)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if errCode(resp) != CodeRequestInvalid {
		t.Fatalf("code = %v", errCode(resp))
	}

	// Missing statement on the query endpoint.
	code, _, _ = do(t, "POST", srv.URL+"/db/neo4j/query/v2", "", "application/json",
		`{"parameters":{}}`, testUser, testPass)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}

	// Unknown Accept.
	code, resp, _ = do(t, "POST", srv.URL+"/db/neo4j/query/v2", "application/soap+xml", "application/json",
		`{"statement":"RETURN 1"}`, testUser, testPass)
	if code != http.StatusNotAcceptable {
		t.Fatalf("status = %d, want 406; body=%v", code, resp)
	}
}

func TestTypedParameters(t *testing.T) {
	srv, _ := newTestServer(t)

	body := `{"statement":"RETURN $i + 1 AS i, $s AS s, $f AS f, $d AS d",
	  "parameters":{
	    "i":{"$type":"Integer","_value":"41"},
	    "s":{"$type":"String","_value":"hi"},
	    "f":{"$type":"Float","_value":"2.5"},
	    "d":{"$type":"Date","_value":"2015-03-26"}}}`
	code, resp, _ := do(t, "POST", srv.URL+"/db/neo4j/query/v2",
		"application/vnd.neo4j.query.v1.2", "application/vnd.neo4j.query.v1.2", body, testUser, testPass)
	if code != http.StatusAccepted {
		t.Fatalf("status = %d; body=%v", code, resp)
	}
	_, values := fieldsAndValues(t, resp)
	for i, want := range []map[string]any{
		{"$type": "Integer", "_value": "42"},
		{"$type": "String", "_value": "hi"},
		{"$type": "Float", "_value": "2.5"},
		{"$type": "Date", "_value": "2015-03-26"},
	} {
		got, _ := values[0][i].(map[string]any)
		if got["$type"] != want["$type"] || got["_value"] != want["_value"] {
			t.Errorf("value %d = %v, want %v", i, values[0][i], want)
		}
	}

	// A node-typed parameter is rejected.
	body = `{"statement":"RETURN $n","parameters":{"n":{"$type":"Node","_value":{}}}}`
	code, resp, _ = do(t, "POST", srv.URL+"/db/neo4j/query/v2",
		"application/vnd.neo4j.query.v1.2", "application/vnd.neo4j.query.v1.2", body, testUser, testPass)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%v", code, resp)
	}
	if errCode(resp) != CodeRequestInvalid {
		t.Fatalf("code = %v", errCode(resp))
	}
}

func TestWrongMethod(t *testing.T) {
	srv, _ := newTestServer(t)
	code, _, _ := do(t, "GET", srv.URL+"/db/neo4j/query/v2", "", "", "", testUser, testPass)
	if code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", code)
	}
}
