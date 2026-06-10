package middleware

import (
	"bytes"
	"context"
	"net/http"
	"sync"
	"time"
)

// Timeout returns a Middleware that cancels the request context after the
// given duration. If the handler does not finish in time, the client receives
// 503 Service Unavailable.
//
// Handlers must respect context cancellation by checking ctx.Done() or passing
// the context to downstream calls (database queries, HTTP clients, etc.) for
// the timeout to take effect.
//
// Example — 5-second timeout per request:
//
//	middleware.Timeout(5 * time.Second)
func Timeout(timeout time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			r = r.WithContext(ctx)

			// done receives the signal that the handler finished normally.
			done := make(chan struct{})

			// tw wraps w to guard against writing to the response after the
			// timeout has fired, and we have already written 503.
			tw := &timeoutWriter{ResponseWriter: w}

			go func() {
				defer close(done)
				next.ServeHTTP(tw, r)
			}()

			select {
			case <-done:
				// Handler finished in time — flush its response.
				tw.mu.Lock()
				defer tw.mu.Unlock()
				if !tw.timedOut {
					w.WriteHeader(tw.code)
					_, _ = w.Write(tw.buf.Bytes())
				}

			case <-ctx.Done():
				tw.mu.Lock()
				tw.timedOut = true
				tw.mu.Unlock()

				http.Error(w, "request timeout", http.StatusServiceUnavailable)
			}
		})
	}
}

// timeoutWriter buffers the handler's response so it can be discarded if the
// deadline fires before the handler returns.
type timeoutWriter struct {
	http.ResponseWriter

	mu          sync.Mutex
	buf         bytes.Buffer
	code        int
	timedOut    bool
	wroteHeader bool
}

func (tw *timeoutWriter) WriteHeader(code int) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.timedOut || tw.wroteHeader {
		return
	}
	tw.code = code
	tw.wroteHeader = true
}

func (tw *timeoutWriter) Write(b []byte) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.timedOut {
		return 0, context.DeadlineExceeded
	}
	if !tw.wroteHeader {
		tw.code = http.StatusOK
		tw.wroteHeader = true
	}
	return tw.buf.Write(b)
}
