package auth

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func header(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

func TestCheck(t *testing.T) {
	a := New("alice", "pa:ss")
	for _, tc := range []struct {
		name, user, pass string
		ok               bool
	}{
		{"valid, colon in password", "alice", "pa:ss", true},
		{"wrong password", "alice", "Pa:ss", false},
		{"prefix of password", "alice", "pa:s", false},
		{"longer than password", "alice", "pa:sspa:ss", false},
		{"empty password", "alice", "", false},
		{"wrong user", "mallory", "pa:ss", false},
	} {
		err := a.Check(header(tc.user, tc.pass))
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
	if err := a.Check("!!!not base64!!!"); err == nil {
		t.Error("malformed base64 accepted")
	}
	if err := a.Check(base64.StdEncoding.EncodeToString([]byte("no-colon"))); err == nil {
		t.Error("credentials without a colon accepted")
	}
}

func TestMiddleware(t *testing.T) {
	a := New("alice", "secret")
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest("GET", "/db/x", nil)
	req.SetBasicAuth("alice", "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("authorised: status=%d", rec.Code)
	}

	for _, authz := range []string{"", "Bearer abc", "Basic ", "Basic " + header("alice", "nope")} {
		req := httptest.NewRequest("GET", "/db/x", nil)
		if authz != "" {
			req.Header.Set("Authorization", authz)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("Authorization %q: status = %d, want 401", authz, rec.Code)
		}
		if rec.Header().Get("WWW-Authenticate") == "" {
			t.Fatalf("Authorization %q: missing WWW-Authenticate", authz)
		}
		var body struct {
			Errors []struct{ Code, Message string } `json:"errors"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Errors) != 1 {
			t.Fatalf("Authorization %q: body is not a Neo4j error document: %v (%q)", authz, err, rec.Body.String())
		}
		if body.Errors[0].Code != "Neo.ClientError.Security.Unauthorized" {
			t.Fatalf("code = %q", body.Errors[0].Code)
		}
	}
}
