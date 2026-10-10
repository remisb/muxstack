package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// headerChurner sets headers in a loop until its context ends, then keeps
// going for a while so it overlaps with Timeout writing the 503. It closes
// finished when it returns.
func headerChurner(finished chan<- struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		for i := 0; ; i++ {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Iter", strconv.Itoa(i))
			if r.Context().Err() != nil {
				break
			}
			time.Sleep(100 * time.Microsecond)
		}
		for i := 0; i < 1000; i++ {
			w.Header().Set("X-After", strconv.Itoa(i))
			w.Header().Del("Content-Type")
		}
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("late"))
	})
}

func TestTimeoutPassesResponseThrough(t *testing.T) {
	h := Timeout(time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Add("X-Multi", "a")
		w.Header().Add("X-Multi", "b")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":`))
		_, _ = w.Write([]byte(`true}`))
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := rec.Header().Values("X-Multi"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("X-Multi = %q, want [a b]", got)
	}
	if got := rec.Body.String(); got != `{"ok":true}` {
		t.Errorf("body = %q", got)
	}
}

func TestTimeoutDefaultStatus(t *testing.T) {
	tests := []struct {
		name string
		h    http.HandlerFunc
	}{
		{"no write", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Only", "1")
		}},
		{"write without WriteHeader", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("hi"))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			Timeout(time.Second)(tt.h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", rec.Code)
			}
		})
	}
}

func TestTimeoutDeadline(t *testing.T) {
	finished := make(chan struct{})
	h := Timeout(5 * time.Millisecond)(headerChurner(finished))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	// Read the real writer's headers while the handler is still mutating its
	// own; -race reports this if they share a map.
	for i := 0; i < 100; i++ {
		_ = rec.Header().Get("X-After")
	}
	<-finished

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	if got := rec.Body.String(); got != "request timeout\n" {
		t.Errorf("body = %q", got)
	}
	if got := rec.Header().Get("X-Iter"); got != "" {
		t.Errorf("handler header leaked into timeout response: X-Iter = %q", got)
	}
}

func TestTimeoutClientGone(t *testing.T) {
	finished := make(chan struct{})
	h := Timeout(time.Minute)(headerChurner(finished))

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	time.AfterFunc(5*time.Millisecond, cancel)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	for i := 0; i < 100; i++ {
		_ = rec.Header().Get("X-After")
	}
	<-finished

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
}

func TestTimeoutPanicPropagates(t *testing.T) {
	h := Timeout(time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	defer func() {
		if p := recover(); p != "boom" {
			t.Errorf("recovered %v, want boom", p)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	t.Error("ServeHTTP returned without panicking")
}
