package api

import (
	"net/http"
	"net/url"
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
		if origin != "" && !allowed && !sameOrigin(r, origin) &&
			(r.Method == http.MethodPost || r.Method == http.MethodDelete) {
			// A browser attaches cached Basic credentials to a cross-origin
			// request like a cookie, so a foreign page could otherwise run a
			// statement as the user without a preflight. Non-browser clients
			// send no Origin and are unaffected.
			writeError(w, http.StatusForbidden, clientError(CodeRequestInvalid, "cross-origin request from %q is not allowed", origin))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin reports whether the Origin header names the host that was
// requested.
func sameOrigin(r *http.Request, origin string) bool {
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
}
