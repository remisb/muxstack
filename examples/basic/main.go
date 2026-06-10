package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/remisb/muxstack/middleware"
)

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /panic", handlePanic)
	mux.HandleFunc("GET /slow", handleSlow)
	mux.HandleFunc("GET /", handleHome)

	//logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	//handler := middleware.Chain(
	//	mux,
	//	middleware.Logger(logger),
	//	middleware.Recoverer(logger),
	//)

	// Development — allow all origins
	handler := middleware.Chain(
		mux,
		middleware.CORS(middleware.DefaultCORSConfig()),
		middleware.Logger(logger),
		middleware.Recoverer(logger),
		middleware.RateLimiter(middleware.RateLimitConfig{
			RequestsPerInterval: 2,
			Interval:            time.Second,
			KeyFunc: func(r *http.Request) string {
				if key := r.Header.Get("X-API-Key"); key != "" {
					return key
				}
				return remoteIP(r) // fallback
			},
		}),
		//middleware.Timeout(5*time.Second), // ← wraps all handlers
	)

	// Production — restrict to specific origins
	//
	//  handler := middleware.Chain(
	//	  mux,
	//	  middleware.CORS(middleware.CORSConfig{
	//		AllowedOrigins: []string{"https://example.com"},
	//		AllowedMethods: []string{"GET", "POST", "DELETE"},
	//		AllowedHeaders: []string{"Authorization", "Content-Type"},
	//		AllowCredentials: true,
	//		MaxAge:           "600",
	//	})

	// Custom: 10 req/sec per API key header
	//
	//  handler := middleware.Chain(mux, middleware.RateLimiter(middleware.RateLimitConfig{
	//	  RequestsPerInterval: 10,
	//	  Interval:            time.Second,
	//	  KeyFunc: func(r *http.Request) string {
	//		if key := r.Header.Get("X-API-Key"); key != "" {
	//			return key
	//		}
	//		return remoteIP(r) // fallback
	//	  },
	//  }))

	srv := &http.Server{
		Addr:         ":8080",
		Handler:      handler,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in a goroutine
	go func() {
		slog.Info(fmt.Sprintf("Server listening on %s", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Server error: %v", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown on SIGINT / SIGTERM
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("Forced shutdown: %v", err)
		os.Exit(1)
	}
	slog.Info("Server stopped")
}

func handleHome(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "Welcome to muxstack!")
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintln(w, `{"status":"ok"}`)
}

func handlePanic(w http.ResponseWriter, r *http.Request) {
	panic("This is a test panic")
}

func handleSlow(w http.ResponseWriter, r *http.Request) {
	time.Sleep(2 * time.Second)
	fmt.Fprintln(w, "slow response")

	workCh := make(chan string, 1)

	select {
	case <-r.Context().Done():
		return
	case result := <-workCh:
		fmt.Fprintln(w, result)

		// process result
	}
}

// remoteIP extracts the IP address from r.RemoteAddr, stripping the port.
func remoteIP(r *http.Request) string {
	addr := r.RemoteAddr
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i]
		}
	}
	return addr
}
