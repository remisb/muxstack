package middleware

import (
	"net/http"
	"slices"
	"strconv"
)

// Middleware wraps an http.Handler.
type Middleware func(http.Handler) http.Handler

// Chain applies middlewares to h in left-to-right order so that the first
// middleware in the list is the outermost (executed first on a request).
//
// It panics early — at construction time — if h or any middleware is nil,
// making mis-configurations visible immediately rather than at request time.
func Chain(h http.Handler, m ...Middleware) http.Handler {
	if h == nil {
		panic("middleware.Chain: base handler must not be nil")
	}

	// Fast path: nothing to wrap.
	if len(m) == 0 {
		return h
	}

	for i, mw := range slices.Backward(m) {
		if mw == nil {
			panic("middleware.Chain: middleware at index " + strconv.Itoa(i) + " must not be nil")
		}
		h = mw(h)
	}
	return h
}

// Stack is an immutable, reusable ordered list of middlewares.
// Build it once and apply it to many handlers via Then / ThenFunc.
//
//	base := middleware.NewStack(
//	    middleware.Logger(logger),
//	    middleware.Recoverer(logger),
//	)
//	http.Handle("/api/", base.Then(apiHandler))
//	http.Handle("/",     base.Then(staticHandler))
type Stack struct {
	middlewares []Middleware
}

// NewStack creates a Stack from the provided middlewares.
func NewStack(m ...Middleware) Stack {
	// Defensive copy so the caller cannot mutate the slice later.
	ms := make([]Middleware, len(m))
	copy(ms, m)
	return Stack{middlewares: ms}
}

// Append returns a new Stack with additional middlewares appended.
// The receiver is not modified (immutable composition).
func (s Stack) Append(m ...Middleware) Stack {
	ms := make([]Middleware, len(s.middlewares)+len(m))
	copy(ms, s.middlewares)
	copy(ms[len(s.middlewares):], m)
	return Stack{middlewares: ms}
}

// Then applies the middleware stack to h and returns the wrapped handler.
func (s Stack) Then(h http.Handler) http.Handler {
	return Chain(h, s.middlewares...)
}

// ThenFunc is a convenience wrapper around Then for http.HandlerFunc values.
func (s Stack) ThenFunc(fn http.HandlerFunc) http.Handler {
	return s.Then(fn)
}
