package middleware

import (
	"context"
	"net/http"
	"strings"
)

// contextKey is an unexported type for context keys in this package.
type contextKey string

const claimsKey contextKey = "claims"

// Claims holds the authenticated principal's identity and roles.
// Embed or extend this struct to carry additional application-specific fields.
type Claims struct {
	Subject string
	Roles   []string
}

// TokenVerifier is a function that validates a raw bearer token and returns
// the associated Claims. Return a non-nil error to reject the request.
type TokenVerifier func(ctx context.Context, token string) (*Claims, error)

// Authenticator returns a Middleware that enforces bearer token authentication.
// On success the verified Claims are stored in the request context and can be
// retrieved with ClaimsFromContext. On failure it responds with 401.
func Authenticator(verify TokenVerifier) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				http.Error(w, "missing or malformed authorization header", http.StatusUnauthorized)
				return
			}

			claims, err := verify(r.Context(), token)
			if err != nil {
				http.Error(w, "unauthorized: "+err.Error(), http.StatusUnauthorized)
				return
			}

			r = r.WithContext(context.WithValue(r.Context(), claimsKey, claims))
			next.ServeHTTP(w, r)
		})
	}
}

// Authorizer returns a Middleware that checks whether the authenticated
// principal (from Claims in context) holds at least one of the required roles.
// Must be used after Authenticator in the middleware chain.
// Responds with 403 Forbidden if none of the required roles are present.
func Authorizer(requiredRoles ...string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := ClaimsFromContext(r.Context())
			if !ok {
				// No claims in context — Authenticator was not applied first.
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}

			if !hasRole(claims.Roles, requiredRoles) {
				http.Error(w, "forbidden: insufficient role", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ClaimsFromContext retrieves the Claims stored in ctx by the Authenticator
// middleware. Returns (nil, false) if no claims are present.
func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(claimsKey).(*Claims)
	return c, ok
}

// bearerToken extracts the token string from the Authorization header.
// Expects the format: "Bearer <token>".
func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", false
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return "", false
	}
	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", false
	}
	return token, true
}

// hasRole returns true if the principal holds at least one of the required roles.
func hasRole(principalRoles, requiredRoles []string) bool {
	roleSet := make(map[string]struct{}, len(principalRoles))
	for _, r := range principalRoles {
		roleSet[r] = struct{}{}
	}
	for _, r := range requiredRoles {
		if _, ok := roleSet[r]; ok {
			return true
		}
	}
	return false
}
