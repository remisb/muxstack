package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func mustProxies(t *testing.T, list ...string) []netip.Prefix {
	t.Helper()
	p, err := ParseTrustedProxies(list)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// resolved runs r through ClientIP and returns what ClientAddr saw.
func resolved(cfg ClientIPConfig, r *http.Request) string {
	var got string
	h := ClientIP(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = ClientAddr(r) }))
	h.ServeHTTP(httptest.NewRecorder(), r)
	return got
}

func TestClientIP(t *testing.T) {
	cfg := ClientIPConfig{TrustedProxies: mustProxies(t, "127.0.0.1", "::1", "10.0.0.0/8")}
	tests := []struct {
		name   string
		remote string
		xff    []string
		want   string
	}{
		{"direct client", "203.0.113.7:5000", nil, "203.0.113.7"},
		{"untrusted peer cannot spoof", "203.0.113.7:5000", []string{"198.51.100.1"}, "203.0.113.7"},
		{"trusted proxy names the client", "127.0.0.1:40000", []string{"203.0.113.7"}, "203.0.113.7"},
		{"ipv6 loopback proxy", "[::1]:40000", []string{"2001:db8::5"}, "2001:db8::5"},
		{"mapped loopback is loopback", "[::ffff:127.0.0.1]:40000", []string{"203.0.113.7"}, "203.0.113.7"},
		{"client-supplied entries left of the client are ignored", "127.0.0.1:40000", []string{"6.6.6.6, 203.0.113.7"}, "203.0.113.7"},
		{"trusted hops are skipped", "127.0.0.1:40000", []string{"203.0.113.7, 10.1.2.3"}, "203.0.113.7"},
		{"repeated headers are one list", "127.0.0.1:40000", []string{"6.6.6.6", "203.0.113.7"}, "203.0.113.7"},
		{"garbage stops at the last trusted hop", "127.0.0.1:40000", []string{"not-an-ip"}, "127.0.0.1"},
		{"no header: the proxy itself", "127.0.0.1:40000", nil, "127.0.0.1"},
		{"all hops trusted: the leftmost", "127.0.0.1:40000", []string{"10.9.9.9, 10.1.1.1"}, "10.9.9.9"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tt.remote
			for _, h := range tt.xff {
				r.Header.Add("X-Forwarded-For", h)
			}
			if got := resolved(cfg, r); got != tt.want {
				t.Errorf("ClientAddr = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClientIPTrustsNobodyByDefault(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("X-Forwarded-For", "203.0.113.7")
	if got := resolved(ClientIPConfig{}, r); got != "127.0.0.1" {
		t.Errorf("ClientAddr = %q, want the peer", got)
	}
}

func TestClientAddrWithoutMiddleware(t *testing.T) {
	for remote, want := range map[string]string{
		"192.0.2.1:1234":   "192.0.2.1",
		"[2001:db8::1]:80": "2001:db8::1", // no brackets, unlike the old remoteIP
		"@":                "@",           // unix socket peers are passed through
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if got := ClientAddr(r); got != want {
			t.Errorf("ClientAddr(%q) = %q, want %q", remote, got, want)
		}
	}
}

func TestParseTrustedProxies(t *testing.T) {
	got := mustProxies(t, "127.0.0.1", "10.0.0.0/8", " ::1 ", "172.16.5.4/12", "::ffff:192.0.2.1")
	want := []string{"127.0.0.1/32", "10.0.0.0/8", "::1/128", "172.16.0.0/12", "192.0.2.1/32"}
	for i, p := range got {
		if p.String() != want[i] {
			t.Errorf("prefix %d = %s, want %s", i, p, want[i])
		}
	}
	if _, err := ParseTrustedProxies([]string{"localhost"}); err == nil {
		t.Error("a host name should be rejected")
	}
}

// Behind a trusted proxy the default rate-limit key is the client, so
// clients do not share one budget; an untrusted peer cannot dodge its own.
func TestRateLimiterKeysOnClientIP(t *testing.T) {
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
		ClientIP(ClientIPConfig{TrustedProxies: mustProxies(t, "127.0.0.1")}),
		RateLimiter(RateLimitConfig{RequestsPerInterval: 2, Interval: time.Minute}),
	)
	hit := func(remote, xff string) int {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	for range 2 {
		hit("127.0.0.1:40000", "203.0.113.1")
	}
	if code := hit("127.0.0.1:40000", "203.0.113.1"); code != http.StatusTooManyRequests {
		t.Fatalf("first client, third request = %d, want 429", code)
	}
	if code := hit("127.0.0.1:40000", "203.0.113.2"); code != http.StatusOK {
		t.Fatalf("second client behind the proxy = %d, want 200", code)
	}
	for range 2 {
		hit("198.51.100.9:5000", "")
	}
	if code := hit("198.51.100.9:5000", "203.0.113.99"); code != http.StatusTooManyRequests {
		t.Fatalf("spoofed header from an untrusted peer = %d, want 429", code)
	}
}

func TestLoggerLogsClientIP(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
		ClientIP(ClientIPConfig{TrustedProxies: mustProxies(t, "127.0.0.1")}),
		Logger(logger),
	)
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("X-Forwarded-For", "203.0.113.7")
	h.ServeHTTP(httptest.NewRecorder(), r)
	out := buf.String()
	if !strings.Contains(out, "client_ip=203.0.113.7") || !strings.Contains(out, "remote_addr=127.0.0.1:40000") {
		t.Errorf("log line = %q", out)
	}
}
