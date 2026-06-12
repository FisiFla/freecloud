package middleware

import (
	"context"
	"net/http"
)

// contextKey is a private type for context keys to avoid collisions.
type contextKey string

const (
	// ActorIDKey is the context key for the actor ID extracted from the request.
	ActorIDKey contextKey = "actor_id"
)

// ActorIDMiddleware extracts the X-Actor-ID header from incoming requests
// and stores it in the request context. If the header is missing, "system"
// is used as the default actor.
func ActorIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actorID := r.Header.Get("X-Actor-ID")
		if actorID == "" {
			actorID = "system"
		}
		ctx := context.WithValue(r.Context(), ActorIDKey, actorID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetActorID retrieves the actor ID from the context. Returns "unknown" if not set.
func GetActorID(ctx context.Context) string {
	if id, ok := ctx.Value(ActorIDKey).(string); ok {
		return id
	}
	return "unknown"
}
