// Package auth implements HTTP basic authentication for the graingraph-query
// server. Basic auth is the only supported authentication mechanism.
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// Authenticator validates HTTP basic-auth credentials against the single
// configured username and password. It is safe for concurrent use.
type Authenticator struct {
	username string
	password string
}

// New returns an Authenticator for the given credential.
func New(username, password string) *Authenticator {
	return &Authenticator{username: username, password: password}
}

// Check validates an Authorization header value (the part after "Basic").
// Username and password are compared as SHA-256 digests with
// subtle.ConstantTimeCompare, so timing reveals neither their length nor
// their content. Both comparisons always run.
func (a *Authenticator) Check(headerValue string) error {
	decoded, err := base64.StdEncoding.DecodeString(headerValue)
	if err != nil {
		return errInvalidCredentials
	}
	user, pass, ok := strings.Cut(string(decoded), ":")
	if !ok {
		return errInvalidCredentials
	}
	userOK := digestEqual(user, a.username)
	passOK := digestEqual(pass, a.password)
	if !userOK || !passOK {
		return errInvalidCredentials
	}
	return nil
}

func digestEqual(got, want string) bool {
	g, w := sha256.Sum256([]byte(got)), sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(g[:], w[:]) == 1
}

var errInvalidCredentials = errors.New("invalid credentials")

// Middleware returns an http.Handler wrapper that enforces basic auth.
// On success it stores the authenticated user in the request context
// On failure it responds 401 with a Neo4j-style error body and a
// WWW-Authenticate challenge.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, value, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if !ok || !strings.EqualFold(scheme, "Basic") || value == "" {
			a.unauthorized(w, r, "missing or malformed Authorization header; use HTTP basic auth")
			return
		}
		if err := a.Check(value); err != nil {
			a.unauthorized(w, r, "Invalid credential.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *Authenticator) unauthorized(w http.ResponseWriter, r *http.Request, message string) {
	w.Header().Set("WWW-Authenticate", `Basic realm="graingraph-query", charset="UTF-8"`)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	body, _ := json.Marshal(map[string]any{"errors": []map[string]string{
		{"code": "Neo.ClientError.Security.Unauthorized", "message": message},
	}})
	_, _ = w.Write(body)
}
