package middleware

import (
	"net/http"
	"sync"
	"time"
)

// RateLimitConfig holds the configuration for the rate limiter.
type RateLimitConfig struct {
	// RequestsPerInterval is the maximum number of requests allowed per interval.
	RequestsPerInterval int

	// Interval is the sliding window duration for counting requests.
	Interval time.Duration

	// KeyFunc extracts a key from the request to identify a client.
	// Defaults to ClientAddr if nil: the client IP resolved by ClientIP, or
	// the connection's peer address without ClientIP. Behind a reverse proxy
	// add ClientIP with the proxy trusted, or all clients share one limit.
	KeyFunc func(r *http.Request) string
}

// bucket tracks the request count for a single key within a sliding window.
type bucket struct {
	mu        sync.Mutex
	count     int
	windowEnd time.Time
}

// allow returns true if the request is within the rate limit.
func (b *bucket) allow(limit int, interval time.Duration) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	if now.After(b.windowEnd) {
		// Start a new window.
		b.count = 0
		b.windowEnd = now.Add(interval)
	}

	if b.count >= limit {
		return false
	}

	b.count++
	return true
}

// limiter holds all per-client buckets and periodically cleans up stale ones.
type limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	cfg     RateLimitConfig
}

func newLimiter(cfg RateLimitConfig) *limiter {
	l := &limiter{
		buckets: make(map[string]*bucket),
		cfg:     cfg,
	}
	// Background goroutine to evict expired buckets.
	go l.cleanup()
	return l
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{}
		l.buckets[key] = b
	}
	l.mu.Unlock()

	return b.allow(l.cfg.RequestsPerInterval, l.cfg.Interval)
}

// cleanup removes buckets whose window has expired to prevent unbounded growth.
func (l *limiter) cleanup() {
	ticker := time.NewTicker(l.cfg.Interval * 2)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		l.mu.Lock()
		for key, b := range l.buckets {
			b.mu.Lock()
			expired := now.After(b.windowEnd)
			b.mu.Unlock()
			if expired {
				delete(l.buckets, key)
			}
		}
		l.mu.Unlock()
	}
}

// RateLimiter returns a Middleware that limits the number of requests per client
// using a fixed window counter. Clients that exceed the limit receive 429 Too
// Many Requests.
//
// Example — 100 requests per minute per IP:
//
//	middleware.RateLimiter(middleware.RateLimitConfig{
//	    RequestsPerInterval: 100,
//	    Interval:            time.Minute,
//	})
func RateLimiter(cfg RateLimitConfig) Middleware {
	if cfg.RequestsPerInterval <= 0 {
		cfg.RequestsPerInterval = 100
	}
	if cfg.Interval <= 0 {
		cfg.Interval = time.Minute
	}
	if cfg.KeyFunc == nil {
		cfg.KeyFunc = ClientAddr
	}

	l := newLimiter(cfg)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := cfg.KeyFunc(r)
			if !l.allow(key) {
				w.Header().Set("Retry-After", cfg.Interval.String())
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
