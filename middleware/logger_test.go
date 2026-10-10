package middleware

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoggerFlushReachesUnderlyingWriter(t *testing.T) {
	var flushErr error
	h := Logger(slog.New(slog.NewTextHandler(io.Discard, nil)))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("data: hello\n\n"))
		flushErr = http.NewResponseController(w).Flush()
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/events", nil))

	if flushErr != nil {
		t.Fatalf("Flush through Logger: %v", flushErr)
	}
	if !rec.Flushed {
		t.Error("the recorder was not flushed")
	}
	if got := rec.Body.String(); got != "data: hello\n\n" {
		t.Errorf("body = %q", got)
	}
}
