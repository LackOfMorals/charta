package api

import (
	"net/http"
	"slices"
	"strings"
)

// allowedMethods are the methods the API serves, advertised on OPTIONS.
const allowedMethods = "GET, POST, DELETE, OPTIONS"

// defaultAllowedHeaders are allowed on preflight when the browser does
// not name the headers it wants to send.
const defaultAllowedHeaders = "Authorization, Content-Type, Accept"

// withCORS answers OPTIONS requests and adds CORS headers for the
// configured origins. "*" allows any origin; that is safe because
// credentials travel in the Authorization header, which a page must set
// explicitly, never in ambient cookies. An empty list disables CORS
// headers, but OPTIONS is still answered (with Allow only).
//
// Preflights are answered before authentication, since browsers do not
// send credentials on them.
func withCORS(next http.Handler, origins []string) http.Handler {
	any := slices.Contains(origins, "*")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := origin != "" && (any || slices.Contains(origins, origin))
		if any {
			// Neo4j sends this on every response, Origin or not.
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		if allowed {
			if !any {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
			}
			w.Header().Set("Access-Control-Expose-Headers", requestIDHeader)
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Allow", allowedMethods)
			if allowed && r.Header.Get("Access-Control-Request-Method") != "" {
				w.Header().Set("Access-Control-Allow-Methods", allowedMethods)
				headers := defaultAllowedHeaders
				if req := strings.TrimSpace(r.Header.Get("Access-Control-Request-Headers")); req != "" {
					headers = req
				}
				w.Header().Set("Access-Control-Allow-Headers", headers)
				w.Header().Set("Access-Control-Max-Age", "86400")
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
