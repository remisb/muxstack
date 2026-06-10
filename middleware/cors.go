// │   ├── cors.go          # CORS headers
// │   ├── auth.go          # Authentication / Authorization
// │   ├── ratelimit.go     # Rate limiting
// │   └── timeout.go       # Request timeout
// provide cors middleware code for file cors.go
// provide ratelimit middleware code for file ratelimit.go
// provide timeout middleware code for file timeout.go
package middleware

import (
	"net/http"
	"slices"
	"strings"
)

// CORSConfig holds the configuration for the CORS middleware.
type CORSConfig struct {
	// AllowedOrigins is a list of origins that are allowed.
	// Use ["*"] to allow any origin.
	AllowedOrigins []string

	// AllowedMethods is a list of HTTP methods allowed for CORS requests.
	// Defaults to: GET, POST, PUT, PATCH, DELETE, OPTIONS.
	AllowedMethods []string

	// AllowedHeaders is a list of HTTP headers allowed in CORS requests.
	// Defaults to: Accept, Authorization, Content-Type, X-Request-ID.
	AllowedHeaders []string

	// ExposedHeaders is a list of headers the browser is allowed to read
	// from the response.
	ExposedHeaders []string

	// AllowCredentials indicates whether the request can include user
	// credentials (cookies, HTTP auth, client-side TLS certificates).
	AllowCredentials bool

	// MaxAge is the value (in seconds) for the Access-Control-Max-Age header.
	// 0 means the header is not set.
	MaxAge string
}

// DefaultCORSConfig returns a CORSConfig with permissive defaults suitable
// for development. Tighten AllowedOrigins before going to production.
func DefaultCORSConfig() CORSConfig {
	return CORSConfig{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{
			http.MethodGet,
			http.MethodPost,
			http.MethodPut,
			http.MethodPatch,
			http.MethodDelete,
			http.MethodOptions,
		},
		AllowedHeaders: []string{
			"Accept",
			"Authorization",
			"Content-Type",
			"X-Request-ID",
		},
	}
}

// CORS returns a Middleware that adds CORS headers based on the provided
// CORSConfig. Preflight OPTIONS requests are handled and short-circuited.
func CORS(cfg CORSConfig) Middleware {
	allowedMethods := strings.Join(cfg.AllowedMethods, ", ")
	allowedHeaders := strings.Join(cfg.AllowedHeaders, ", ")
	exposedHeaders := strings.Join(cfg.ExposedHeaders, ", ")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Not a CORS request — pass through.
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			if !isOriginAllowed(origin, cfg.AllowedOrigins) {
				http.Error(w, "CORS origin not allowed", http.StatusForbidden)
				return
			}

			header := w.Header()
			header.Set("Access-Control-Allow-Origin", origin)
			header.Set("Vary", "Origin")

			if cfg.AllowCredentials {
				header.Set("Access-Control-Allow-Credentials", "true")
			}

			if exposedHeaders != "" {
				header.Set("Access-Control-Expose-Headers", exposedHeaders)
			}

			// Preflight request — respond and short-circuit.
			if r.Method == http.MethodOptions {
				header.Set("Access-Control-Allow-Methods", allowedMethods)
				header.Set("Access-Control-Allow-Headers", allowedHeaders)
				if cfg.MaxAge != "" {
					header.Set("Access-Control-Max-Age", cfg.MaxAge)
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// isOriginAllowed checks whether the given origin is permitted.
func isOriginAllowed(origin string, allowed []string) bool {
	return slices.Contains(allowed, "*") || slices.Contains(allowed, origin)
}
