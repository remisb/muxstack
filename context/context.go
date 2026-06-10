package context

import (
	"context"
	"net/http"
)

type key string

const requestIDKey key = "requestID"

func WithRequestID(r *http.Request, id string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), requestIDKey, id))
}

func RequestID(r *http.Request) string {
	v, _ := r.Context().Value(requestIDKey).(string)
	return v
}
