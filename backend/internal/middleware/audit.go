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

// ActorIDMiddleware extracts the actor identity from the request.
// It first checks for validated JWT claims (set by AuthMiddleware).
// If no JWT is present, it falls back to the X-Actor-ID header.
// If neither is available, "system" is used as the default.
func ActorIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try JWT claims first (from AuthMiddleware)
		if claims := GetClaims(r.Context()); claims != nil {
			actorID := claims.PreferredUsername
			if actorID == "" {
				actorID = claims.Sub
			}
			if actorID == "" {
				actorID = claims.Email
			}
			if actorID != "" {
				ctx := context.WithValue(r.Context(), ActorIDKey, actorID)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		// Fallback to X-Actor-ID header (dev/testing)
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
