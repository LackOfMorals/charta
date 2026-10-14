// Package api implements the Neo4j Query API (v2) HTTP contract on top
// of an embedded graingraph database.
//
// Endpoints:
//
//	GET    /                                   discovery
//	POST   /db/{databaseName}/query/v2         run a query (implicit tx)
//	POST   /db/{databaseName}/query/v2/tx      open an explicit transaction
//	POST   /db/{databaseName}/query/v2/tx/{id} run a query in a transaction
//	POST   /db/{databaseName}/query/v2/tx/{id}/commit
//	DELETE /db/{databaseName}/query/v2/tx/{id} roll back a transaction
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/graingraph/graingraph/internal/server/auth"
	"github.com/graingraph/graingraph/internal/server/config"
	"github.com/graingraph/graingraph/internal/server/store"
)

// emulatedNeo4jVersion is the Neo4j version discovery reports. Neo4j tools
// gate features on it (Browser looks for 4.4.0 or later); 42.0.0 is above any
// such threshold and cannot be mistaken for a real release.
const emulatedNeo4jVersion = "42.0.0"

// emulatedNeo4jEdition is the edition reported. Neo4j Bloom refuses to start
// unless the server says it is Enterprise.
const emulatedNeo4jEdition = "enterprise"

// Handler serves the Query API.
type Handler struct {
	store     *store.Store
	auth      *auth.Authenticator
	database  string
	version   string
	txTimeout time.Duration
	// publicURL, when set, is the externally visible base URL advertised
	// by discovery. When empty it is derived from the request.
	publicURL string
	// logStatements includes Cypher statement text in request logs.
	logStatements bool
	// corsOrigins are the origins allowed by CORS ("*": any; empty: none).
	corsOrigins []string
	// sysTx tracks explicit transactions on the system database.
	sysTx systemTxs
	// idSeed makes the database IDs reported by SHOW DATABASES stable per
	// database file (see SetIDSeed). Empty: derived from the database name.
	idSeed string
	// created is when the handler was built, reported as the database creation date.
	created time.Time
}

// NewHandler builds a Handler. txTimeout is the default idle timeout
// for explicit transactions (zero: none).
func NewHandler(st *store.Store, a *auth.Authenticator, database string, txTimeout time.Duration) *Handler {
	return &Handler{
		store:         st,
		auth:          a,
		database:      database,
		version:       config.Version,
		txTimeout:     txTimeout,
		logStatements: true,
		corsOrigins:   []string{"*"},
		created:       time.Now(),
	}
}

// SetPublicURL sets the externally visible base URL (for example
// "https://graph.example.com") that discovery advertises, instead of
// deriving it from the request's Host and X-Forwarded-Proto headers,
// which a client can set to anything.
func (h *Handler) SetPublicURL(u string) {
	h.publicURL = strings.TrimRight(u, "/")
}

// SetIDSeed sets the value the database IDs in SHOW DATABASES are derived
// from, typically the absolute path of the database file, so that the IDs
// survive restarts and differ between databases.
func (h *Handler) SetIDSeed(seed string) { h.idSeed = seed }

// SetLogStatements controls whether request logs include the text of
// Cypher statements (parameters are never logged).
func (h *Handler) SetLogStatements(on bool) { h.logStatements = on }

// SetCORSOrigins sets the origins allowed by CORS: "*" for any, or an
// explicit list. An empty list disables CORS headers.
func (h *Handler) SetCORSOrigins(origins []string) { h.corsOrigins = origins }

// Routes returns the root handler: the unauthenticated discovery
// endpoint plus the basic-auth-protected /db/... tree.
func (h *Handler) Routes() http.Handler {
	sub := http.NewServeMux()
	route := func(method, path string, fn http.HandlerFunc) {
		sub.HandleFunc(method+" "+path, h.withDatabase(fn))
		// A method-less pattern catches every other method on the path,
		// replacing the mux's plain-text 405 with a Neo4j-style error.
		sub.HandleFunc(path, methodNotAllowed(method))
	}
	route("POST", "/db/{databaseName}/query/v2", h.query)
	route("POST", "/db/{databaseName}/query/v2/tx", h.txOpen)
	route("POST", "/db/{databaseName}/query/v2/tx/{transactionID}", h.txRun)
	route("POST", "/db/{databaseName}/query/v2/tx/{transactionID}/commit", h.txCommit)
	// DELETE shares a path with txRun, so register it without the helper.
	sub.HandleFunc("DELETE /db/{databaseName}/query/v2/tx/{transactionID}", h.withDatabase(h.txRollback))
	sub.HandleFunc("/db/", notFound)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.discovery)
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("GET /skills", h.skillList)
	mux.HandleFunc("GET /skills/{name}", h.skillGet)
	mux.Handle("/db/", h.auth.Middleware(sub))
	mux.HandleFunc("/", notFound)
	return observe(withCORS(mux, h.corsOrigins), h.logStatements)
}

// baseURL is the externally visible base URL: the configured public URL,
// else one derived from the request.
func (h *Handler) baseURL(r *http.Request) string {
	if h.publicURL != "" {
		return h.publicURL
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// withDatabase answers 404 unless the {databaseName} path segment names
// the served database (compared case-insensitively, as Neo4j does).
func (h *Handler) withDatabase(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("databaseName")
		// The system database answers a few read-only commands (see system.go).
		if strings.EqualFold(name, systemDatabase) {
			next(w, r)
			return
		}
		if !strings.EqualFold(name, h.database) {
			h.reject(w, r, clientError(CodeDatabaseNotFound, "Database does not exist. Database name: '%s'.", name).withStatus(http.StatusNotFound))
			return
		}
		next(w, r)
	}
}

// methodNotAllowed answers 405 with an Allow header and a JSON error.
func methodNotAllowed(allow string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", allow)
		writeError(w, http.StatusMethodNotAllowed,
			clientError(CodeRequestInvalid, "Method %s is not allowed on this endpoint.", r.Method))
	}
}

// notFound answers 404 with a JSON error.
func notFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, clientError(CodeRequestInvalid, "No such endpoint: %s", r.URL.Path))
}

// health implements GET /health: an unauthenticated liveness check. It
// deliberately does not query the database, which would block behind an
// open explicit transaction (graingraph serialises on one connection).
func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// discovery implements GET /: an unauthenticated endpoint-advertisement
// in the Neo4j discovery shape, adapted for a single embedded database.
func (h *Handler) discovery(w http.ResponseWriter, r *http.Request) {
	base := h.baseURL(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"query":                   base + "/db/{databaseName}/query/v2",
		"query_api":               base + "/db/{databaseName}/query/v2",
		"query_api_major_version": "2",
		"query_api_versions":      []string{"2.0"},
		"transaction":             base + "/db/{databaseName}/query/v2/tx",
		"skills":                  base + "/skills",
		// Clients such as Neo4j Desktop and Browser read the server's version
		// and edition from discovery.
		"neo4j_version": emulatedNeo4jVersion,
		"neo4j_edition": emulatedNeo4jEdition,
		"database":      h.database,
		"server":        "graingraph-query/" + h.version,
		"auth_config":   map[string]any{"oidc_providers": []any{}},
	})
}

// writeJSON marshals v before touching the response, so a value that
// cannot be encoded produces a clean 500 instead of a status line
// followed by a truncated body.
func writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		status = http.StatusInternalServerError
		data, _ = json.Marshal(docOf(serverError(CodeGeneralError, "encode response: %v", err)))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}

// reject answers a request-level failure in the format the client asked
// for: Typed JSON, or a lone JSONL Error event. An unusable Accept header
// falls back to plain JSON, as the request is being refused anyway.
func (h *Handler) reject(w http.ResponseWriter, r *http.Request, ae *apiError) {
	rt, err := parseAccept(r.Header.Get("Accept"))
	if err != nil {
		rt = responseType{}
	}
	if rt.jsonl {
		streamErrorOnly(w, rt, ae)
		return
	}
	writeErrorAs(w, rt.contentType(), ae.Status, ae)
}

// negotiate parses the Accept header; on failure it answers 406 and
// reports ok=false.
func (h *Handler) negotiate(w http.ResponseWriter, r *http.Request) (responseType, bool) {
	rt, err := parseAccept(r.Header.Get("Accept"))
	if err != nil {
		writeError(w, http.StatusNotAcceptable, clientError(CodeRequestInvalid, "%v", err))
		return responseType{}, false
	}
	return rt, true
}

// decodeBody decodes the request body; on failure it answers 400 and
// reports ok=false.
func (h *Handler) decodeBody(w http.ResponseWriter, r *http.Request) (*QueryRequest, bool) {
	// MaxBytesReader fails the read once the limit is exceeded (and makes
	// the server close the connection) rather than silently truncating.
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	q, err := decodeQueryRequest(r.Body, r.Header.Get("Content-Type"))
	if err != nil {
		status := http.StatusBadRequest
		var unsupported *errUnsupportedMedia
		switch {
		case errors.Is(err, errBodyTooLarge):
			status = http.StatusRequestEntityTooLarge
		case errors.As(err, &unsupported):
			status = http.StatusUnsupportedMediaType
		}
		ae := clientError(CodeRequestInvalid, "%v", err).withStatus(status)
		if status == http.StatusUnsupportedMediaType {
			// Rejected before content negotiation, like a bad Accept.
			writeError(w, status, ae)
		} else {
			h.reject(w, r, ae)
		}
		return nil, false
	}
	if slog.Default().Enabled(r.Context(), slog.LevelDebug) {
		// Parameter names and Go types only, never values.
		kinds := make(map[string]string, len(q.Parameters))
		for k, v := range q.Parameters {
			kinds[k] = fmt.Sprintf("%T", v)
		}
		slog.Debug("request body", "content_type", r.Header.Get("Content-Type"), "parameters", kinds)
	}
	return q, true
}

// txOrNotFound looks up an open transaction; on failure it answers 404
// and reports ok=false.
func (h *Handler) txOrNotFound(w http.ResponseWriter, r *http.Request, id string) (*store.OpenTx, bool) {
	t, ok := h.store.Tx(id)
	if !ok {
		h.reject(w, r, clientError(CodeRequestInvalid, "Transaction with Id: %q was not found. It may have timed out and therefore rolled back.", id).withStatus(http.StatusNotFound))
		return nil, false
	}
	t.Touch()
	return t, true
}
