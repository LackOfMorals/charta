package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// baseArgs run a transient server on a free port with credentials u:p.
var baseArgs = []string{"--listen", "127.0.0.1:0", "--db", ":memory:", "--username", "u", "--password", "p"}

// clearEnv blanks every GRAINGRAPH_QUERY_* variable so a developer's shell
// cannot leak into a test.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "GRAINGRAPH_QUERY_") {
			t.Setenv(k, "")
		}
	}
}

// restoreLogger undoes the global slog configuration run() installs.
func restoreLogger(t *testing.T) {
	t.Helper()
	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old) })
}

type server struct {
	addr string
	stop func() error // cancels and waits for run to return; idempotent
}

// start runs the server with args and waits until it accepts connections.
func start(t *testing.T, args ...string) *server {
	t.Helper()
	clearEnv(t)
	restoreLogger(t)
	ctx, cancel := context.WithCancel(context.Background())
	ready, done := make(chan string, 1), make(chan error, 1)
	go func() {
		done <- run(ctx, args, io.Discard, func(a string) { ready <- a })
	}()
	var (
		once    sync.Once
		stopErr error
	)
	s := &server{stop: func() error {
		once.Do(func() {
			cancel()
			select {
			case stopErr = <-done:
			case <-time.After(30 * time.Second):
				stopErr = errors.New("run did not return after cancel")
			}
		})
		return stopErr
	}}
	t.Cleanup(func() { _ = s.stop() })
	select {
	case s.addr = <-ready:
	case err := <-done:
		t.Fatalf("server failed to start: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("server did not become ready")
	}
	return s
}

func (s *server) url(path string) string { return "http://" + s.addr + path }

var client = &http.Client{Timeout: 10 * time.Second}

// post sends a Query API request with basic auth u:p.
func post(t *testing.T, url, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", url, strings.NewReader(body))
	req.SetBasicAuth("u", "p")
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func errCode(body map[string]any) string {
	errs, _ := body["errors"].([]any)
	if len(errs) == 0 {
		return ""
	}
	first, _ := errs[0].(map[string]any)
	s, _ := first["code"].(string)
	return s
}

func get(t *testing.T, url string) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func TestVersionFlag(t *testing.T) {
	clearEnv(t)
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--version"}, &out, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "graingraph-query ") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestStartupErrors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		want  string // substring of the error
		usage bool
	}{
		{"no credentials", []string{"--listen", "127.0.0.1:0", "--db", ":memory:"}, "no basic-auth credentials", false},
		{"username without password", []string{"--db", ":memory:", "--username", "u"}, "no basic-auth credentials", false},
		{"tls cert without key", append([]string{"--tls-cert", "c.pem"}, baseArgs...), "must be set together", false},
		{"negative tx timeout", append([]string{"--tx-timeout", "-1s"}, baseArgs...), "must not be negative", false},
		{"bad public url", append([]string{"--public-url", "nope"}, baseArgs...), "--public-url", false},
		{"bad log level", append([]string{"--log-level", "loud"}, baseArgs...), "--log-level", false},
		{"bad log format", append([]string{"--log-format", "xml"}, baseArgs...), "--log-format", false},
		{"unwritable log file", append([]string{"--log-file", "/nonexistent-dir/x.log"}, baseArgs...), "log file", false},
		{"unknown flag", append([]string{"--bogus"}, baseArgs...), "bogus", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			restoreLogger(t)
			err := run(context.Background(), tc.args, io.Discard, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
			if isUsageError(err) != tc.usage {
				t.Fatalf("isUsageError = %v, want %v", isUsageError(err), tc.usage)
			}
		})
	}
}

func TestHelpIsNotAnError(t *testing.T) {
	clearEnv(t)
	err := run(context.Background(), []string{"-h"}, io.Discard, nil)
	if !errors.Is(err, flag.ErrHelp) || isUsageError(err) {
		t.Fatalf("err = %v, want flag.ErrHelp that is not a usage error", err)
	}
}

func TestListenAddressInUse(t *testing.T) {
	s := start(t, baseArgs...)
	clearEnv(t)
	err := run(context.Background(), []string{"--listen", s.addr, "--db", ":memory:", "--username", "u", "--password", "p"}, io.Discard, nil)
	// Windows words the same failure differently.
	if err == nil || !(strings.Contains(err.Error(), "address already in use") ||
		strings.Contains(err.Error(), "Only one usage of each socket address")) {
		t.Fatalf("err = %v, want address already in use", err)
	}
}

func TestEnvironmentConfiguresServer(t *testing.T) {
	clearEnv(t)
	t.Setenv("GRAINGRAPH_QUERY_LISTEN", "127.0.0.1:0")
	t.Setenv("GRAINGRAPH_QUERY_DB", ":memory:")
	t.Setenv("GRAINGRAPH_QUERY_USERNAME", "envuser")
	t.Setenv("GRAINGRAPH_QUERY_PASSWORD", "envpass")
	t.Setenv("GRAINGRAPH_QUERY_DATABASE_NAME", "envdb")
	restoreLogger(t)

	ctx, cancel := context.WithCancel(context.Background())
	ready, done := make(chan string, 1), make(chan error, 1)
	go func() { done <- run(ctx, nil, io.Discard, func(a string) { ready <- a }) }()
	t.Cleanup(func() { cancel(); <-done })
	var addr string
	select {
	case addr = <-ready:
	case err := <-done:
		t.Fatalf("start: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("not ready")
	}

	_, disc := get(t, "http://"+addr+"/")
	if disc["database"] != "envdb" {
		t.Fatalf("discovery database = %v, want envdb", disc["database"])
	}
	req, _ := http.NewRequest("POST", "http://"+addr+"/db/envdb/query/v2", strings.NewReader(`{"statement":"RETURN 1"}`))
	req.SetBasicAuth("envuser", "envpass")
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("env credentials rejected: status %d", resp.StatusCode)
	}
}

func TestFlagOverridesEnvironment(t *testing.T) {
	clearEnv(t)
	t.Setenv("GRAINGRAPH_QUERY_DATABASE_NAME", "fromenv")
	restoreLogger(t)
	// start() clears the environment, so run directly.
	ctx, cancel := context.WithCancel(context.Background())
	ready, done := make(chan string, 1), make(chan error, 1)
	go func() {
		done <- run(ctx, append([]string{"--database-name", "fromflag"}, baseArgs...), io.Discard, func(a string) { ready <- a })
	}()
	t.Cleanup(func() { cancel(); <-done })
	addr := <-ready
	if _, disc := get(t, "http://"+addr+"/"); disc["database"] != "fromflag" {
		t.Fatalf("database = %v, want fromflag", disc["database"])
	}
}

func TestPublicURLAndCORSFlags(t *testing.T) {
	s := start(t, append([]string{"--public-url", "https://graph.example.com/", "--cors-origins", "https://app.example"}, baseArgs...)...)

	req, _ := http.NewRequest("GET", s.url("/"), nil)
	req.Header.Set("Origin", "https://app.example")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var disc map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&disc)
	resp.Body.Close()
	if disc["query"] != "https://graph.example.com/db/{databaseName}/query/v2" {
		t.Errorf("query = %v", disc["query"])
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Errorf("allow-origin = %q", got)
	}
	req.Header.Set("Origin", "https://evil.example")
	resp, _ = client.Do(req)
	resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("disallowed origin got allow-origin %q", got)
	}
}

func TestCORSNoneDisablesHeaders(t *testing.T) {
	s := start(t, append([]string{"--cors-origins", "none"}, baseArgs...)...)
	req, _ := http.NewRequest("GET", s.url("/"), nil)
	req.Header.Set("Origin", "https://app.example")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("allow-origin = %q with CORS disabled", got)
	}
}

func TestLogFlags(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "q.log")
	s := start(t, append([]string{"--log-format", "json", "--log-file", logPath, "--log-statements=false"}, baseArgs...)...)
	post(t, s.url("/db/neo4j/query/v2"), `{"statement":"RETURN 'findme' AS x"}`)
	_ = s.stop() // flushes and closes the file

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		var line map[string]any
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatalf("log line is not JSON: %q", sc.Text())
		}
		if line["msg"] == "request" {
			found = true
			if line["user"] != "u" || line["status"] != float64(202) {
				t.Errorf("request line = %v", line)
			}
		}
	}
	if !found {
		t.Fatalf("no request line in log file:\n%s", data)
	}
	if strings.Contains(string(data), "findme") {
		t.Error("statement text logged despite --log-statements=false")
	}
}

func TestLogLevelFiltersRequests(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "q.log")
	s := start(t, append([]string{"--log-level", "warn", "--log-file", logPath}, baseArgs...)...)
	post(t, s.url("/db/neo4j/query/v2"), `{"statement":"RETURN 1"}`)      // INFO: filtered
	post(t, s.url("/db/neo4j/query/v2"), `{"statement":"RETURN RETURN"}`) // WARN: kept
	_ = s.stop()
	data, _ := os.ReadFile(logPath)
	if strings.Contains(string(data), "RETURN 1") || !strings.Contains(string(data), "RETURN RETURN") {
		t.Fatalf("log = %s", data)
	}
}

func TestReadOnlyFlag(t *testing.T) {
	db := filepath.Join(t.TempDir(), "ro.db")
	rw := start(t, "--listen", "127.0.0.1:0", "--db", db, "--username", "u", "--password", "p")
	if code, body := post(t, rw.url("/db/neo4j/query/v2"), `{"statement":"CREATE (:P {n:1})"}`); code != http.StatusAccepted {
		t.Fatalf("seed write: %d %v", code, body)
	}
	if err := rw.stop(); err != nil {
		t.Fatal(err)
	}

	ro := start(t, "--listen", "127.0.0.1:0", "--db", db, "--username", "u", "--password", "p", "--read-only")
	code, body := post(t, ro.url("/db/neo4j/query/v2"), `{"statement":"MATCH (p:P) RETURN count(p) AS c"}`)
	if code != http.StatusAccepted {
		t.Fatalf("read: %d %v", code, body)
	}
	code, body = post(t, ro.url("/db/neo4j/query/v2"), `{"statement":"CREATE (:P {n:2})"}`)
	if code != http.StatusBadRequest || !strings.Contains(errCode(body), "ReadOnly") {
		t.Fatalf("write on read-only: %d %v", code, body)
	}
}

func TestTxTimeoutFlagReapsIdleTransaction(t *testing.T) {
	s := start(t, append([]string{"--tx-timeout", "300ms"}, baseArgs...)...)
	_, open := post(t, s.url("/db/neo4j/query/v2/tx"), `{}`)
	id := open["transaction"].(map[string]any)["id"].(string)

	// While the transaction is open it holds the database, so an auto-commit
	// query fails fast; once it is reaped the query succeeds. (Polling inside
	// the transaction would keep it alive.)
	deadline := time.Now().Add(10 * time.Second)
	for {
		code, body := post(t, s.url("/db/neo4j/query/v2"), `{"statement":"RETURN 1"}`)
		if code == http.StatusAccepted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("idle transaction not reaped: %d %v", code, body)
		}
		time.Sleep(200 * time.Millisecond)
	}
	code, body := post(t, s.url("/db/neo4j/query/v2/tx/"+id), `{"statement":"RETURN 1"}`)
	if code != http.StatusNotFound {
		t.Fatalf("reaped transaction still reachable: %d %v", code, body)
	}
}

func TestImportDirFlag(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "d.csv"), []byte("a,b\n1,2\n3,4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	with := start(t, append([]string{"--import-dir", dir}, baseArgs...)...)
	code, body := post(t, with.url("/db/neo4j/query/v2"),
		`{"statement":"LOAD CSV WITH HEADERS FROM 'file:///d.csv' AS r RETURN count(r) AS n"}`)
	if code != http.StatusAccepted || body["data"] == nil {
		t.Fatalf("LOAD CSV with --import-dir: %d %v", code, body)
	}
	without := start(t, baseArgs...)
	code, body = post(t, without.url("/db/neo4j/query/v2"),
		`{"statement":"LOAD CSV WITH HEADERS FROM 'file:///d.csv' AS r RETURN count(r) AS n"}`)
	if body["data"] != nil {
		t.Fatalf("LOAD CSV worked without --import-dir: %d %v", code, body)
	}
}

func selfSignedCert(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	_ = os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	_ = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600)
	return certFile, keyFile
}

func TestTLSFlags(t *testing.T) {
	cert, key := selfSignedCert(t)
	s := start(t, append([]string{"--tls-cert", cert, "--tls-key", key}, baseArgs...)...)

	pool := x509.NewCertPool()
	pemBytes, _ := os.ReadFile(cert)
	pool.AppendCertsFromPEM(pemBytes)
	tlsClient := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool},
	}}
	resp, err := tlsClient.Get("https://" + s.addr + "/")
	if err != nil {
		t.Fatalf("https request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.TLS == nil {
		t.Fatalf("status %d, TLS %v", resp.StatusCode, resp.TLS != nil)
	}
	// Plain HTTP to a TLS port must not succeed.
	if r, err := client.Get("http://" + s.addr + "/"); err == nil {
		r.Body.Close()
		if r.StatusCode == http.StatusOK {
			t.Fatal("plain HTTP answered 200 on a TLS listener")
		}
	}
}

// --- graceful shutdown ---

func TestShutdownStopsListening(t *testing.T) {
	s := start(t, baseArgs...)
	if err := s.stop(); err != nil {
		t.Fatalf("run returned %v on cancellation, want nil", err)
	}
	if conn, err := net.DialTimeout("tcp", s.addr, time.Second); err == nil {
		conn.Close()
		t.Fatal("still accepting connections after shutdown")
	}
}

// slowRequest opens a connection, sends the headers and the first part of
// a body, and leaves the request unfinished until finish is called.
type slowRequest struct {
	conn net.Conn
	rest string
}

func startSlowRequest(t *testing.T, addr string) *slowRequest {
	t.Helper()
	body := `{"statement":"RETURN 42 AS answer"}`
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	head := fmt.Sprintf("POST /db/neo4j/query/v2 HTTP/1.1\r\nHost: x\r\nAuthorization: Basic dTpw\r\n"+
		"Content-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", len(body))
	if _, err := conn.Write([]byte(head + body[:10])); err != nil {
		t.Fatal(err)
	}
	return &slowRequest{conn: conn, rest: body[10:]}
}

func (r *slowRequest) finish(t *testing.T) (status int, body string) {
	t.Helper()
	if _, err := r.conn.Write([]byte(r.rest)); err != nil {
		t.Fatalf("send rest of request: %v", err)
	}
	_ = r.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(r.conn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestShutdownLetsInFlightRequestFinish(t *testing.T) {
	s := start(t, baseArgs...)
	slow := startSlowRequest(t, s.addr)
	time.Sleep(200 * time.Millisecond) // let the server accept it

	done := make(chan error, 1)
	go func() { done <- s.stop() }()

	select {
	case err := <-done:
		t.Fatalf("run returned (%v) while a request was still in flight", err)
	case <-time.After(500 * time.Millisecond):
	}

	status, body := slow.finish(t)
	if status != http.StatusAccepted || !strings.Contains(body, "42") {
		t.Fatalf("in-flight request: %d %s", status, body)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after the in-flight request finished")
	}
}

func TestShutdownGivesUpAfterTimeout(t *testing.T) {
	old := shutdownTimeout
	shutdownTimeout = 300 * time.Millisecond
	t.Cleanup(func() { shutdownTimeout = old })

	s := start(t, baseArgs...)
	_ = startSlowRequest(t, s.addr) // never finished
	time.Sleep(200 * time.Millisecond)

	begin := time.Now()
	if err := s.stop(); err != nil {
		t.Fatalf("run returned %v, want nil", err)
	}
	if d := time.Since(begin); d > 5*time.Second {
		t.Fatalf("shutdown took %v with a 300ms timeout", d)
	}
}

func TestShutdownRollsBackOpenTransaction(t *testing.T) {
	db := filepath.Join(t.TempDir(), "tx.db")
	args := []string{"--listen", "127.0.0.1:0", "--db", db, "--username", "u", "--password", "p"}

	s := start(t, args...)
	if code, body := post(t, s.url("/db/neo4j/query/v2"), `{"statement":"CREATE (:Kept)"}`); code != http.StatusAccepted {
		t.Fatalf("seed: %d %v", code, body)
	}
	code, body := post(t, s.url("/db/neo4j/query/v2/tx"), `{"statement":"CREATE (:Doomed)"}`)
	if code != http.StatusAccepted || body["transaction"] == nil {
		t.Fatalf("open tx: %d %v", code, body)
	}
	if err := s.stop(); err != nil {
		t.Fatalf("shutdown with an open transaction: %v", err)
	}

	s2 := start(t, args...)
	count := func(label string) float64 {
		code, body := post(t, s2.url("/db/neo4j/query/v2"), `{"statement":"MATCH (n:`+label+`) RETURN count(n) AS c"}`)
		if code != http.StatusAccepted {
			t.Fatalf("count %s: %d %v", label, code, body)
		}
		return body["data"].(map[string]any)["values"].([]any)[0].([]any)[0].(float64)
	}
	if k, d := count("Kept"), count("Doomed"); k != 1 || d != 0 {
		t.Fatalf("after restart: Kept=%v Doomed=%v, want 1 and 0", k, d)
	}
}

func TestSkillSubcommand(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"skill", "--url", "https://g.example.com"}, &out, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "---\nname: graingraph-query\n") || !strings.Contains(out.String(), "https://g.example.com/db/neo4j/query/v2") {
		t.Errorf("unexpected output: %.200q", out.String())
	}

	out.Reset()
	if err := run(context.Background(), []string{"skill", "--list"}, &out, nil); err != nil || !strings.HasPrefix(out.String(), "graingraph-query\t") {
		t.Errorf("--list: %v %q", err, out.String())
	}

	err := run(context.Background(), []string{"skill", "nope"}, &out, nil)
	if !isUsageError(err) {
		t.Errorf("unknown skill: %v", err)
	}
}
