package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recoverer returns a Middleware that recovers from panics, logs the error and
// stack trace using the provided slog.Logger, and responds with 500 Internal
// Server Error.
// If logger is nil, slog.Default() is used.
func Recoverer(logger *slog.Logger) Middleware {
	if logger == nil {
		logger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					// Do not recover from http.ErrAbortHandler — it is used by
					// the stdlib to abort a handler intentionally (e.g. client
					// disconnected) and must propagate so the server can clean up.
					if rec == http.ErrAbortHandler {
						panic(rec)
					}

					logger.ErrorContext(r.Context(), "panic recovered",
						slog.Any("error", rec),
						slog.String("stack", string(debug.Stack())),
						slog.String("method", r.Method),
						slog.String("path", r.URL.Path),
					)

					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
