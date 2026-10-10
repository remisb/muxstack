package middleware

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
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
// The handler runs in its own goroutine and its response (headers, status and
// body) is buffered until it returns, the same way as http.TimeoutHandler.
// Streaming is therefore not supported: the writer passed to the handler does
// not implement http.Flusher, http.Hijacker or Unwrap. A panic in the handler
// is re-raised on the serving goroutine so outer middleware (e.g. Recoverer)
// still sees it.
//
// If the request context is canceled because the client went away, only the
// 503 status line is written (nobody is reading a body).
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

			// done is closed when the handler returns normally; panicCh
			// receives the value if it panics instead.
			done := make(chan struct{})
			panicCh := make(chan any, 1)

			// tw buffers the handler's response. It has its own header map so
			// the handler goroutine never touches w.Header(), which this
			// goroutine writes to on timeout.
			tw := &timeoutWriter{h: make(http.Header)}

			go func() {
				defer func() {
					if p := recover(); p != nil {
						panicCh <- p
					}
				}()
				next.ServeHTTP(tw, r)
				close(done)
			}()

			select {
			case p := <-panicCh:
				panic(p)

			case <-done:
				// Handler finished in time — copy its response to w.
				tw.mu.Lock()
				defer tw.mu.Unlock()
				maps.Copy(w.Header(), tw.h)
				if !tw.wroteHeader {
					tw.code = http.StatusOK
				}
				w.WriteHeader(tw.code)
				_, _ = w.Write(tw.buf.Bytes())

			case <-ctx.Done():
				tw.mu.Lock()
				defer tw.mu.Unlock()
				tw.err = ctx.Err()

				if errors.Is(tw.err, context.DeadlineExceeded) {
					http.Error(w, "request timeout", http.StatusServiceUnavailable)
					return
				}
				// Client disconnected or the parent context was canceled.
				w.WriteHeader(http.StatusServiceUnavailable)
			}
		})
	}
}

// timeoutWriter buffers the handler's response so it can be discarded if the
// deadline fires before the handler returns.
type timeoutWriter struct {
	h http.Header // owned by the handler goroutine until it returns

	mu          sync.Mutex
	buf         bytes.Buffer
	code        int
	err         error // non-nil once the context ended before the handler returned
	wroteHeader bool
}

func (tw *timeoutWriter) Header() http.Header { return tw.h }

func (tw *timeoutWriter) WriteHeader(code int) {
	// Same check as net/http, but panicking here (in the handler goroutine)
	// rather than later when the code is copied to the real writer.
	if code < 100 || code > 999 {
		panic(fmt.Sprintf("invalid WriteHeader code %v", code))
	}

	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.err != nil || tw.wroteHeader {
		return
	}
	tw.code = code
	tw.wroteHeader = true
}

func (tw *timeoutWriter) Write(b []byte) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.err != nil {
		return 0, tw.err
	}
	if !tw.wroteHeader {
		tw.code = http.StatusOK
		tw.wroteHeader = true
	}
	return tw.buf.Write(b)
}
