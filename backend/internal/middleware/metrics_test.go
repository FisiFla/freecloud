package middleware

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// flusherRecorder is a ResponseWriter that records whether Flush was called.
type flusherRecorder struct {
	http.ResponseWriter
	flushed bool
}

func (f *flusherRecorder) Flush() { f.flushed = true }

// hijackableRecorder is a ResponseWriter that supports hijacking.
type hijackableRecorder struct {
	http.ResponseWriter
	hijacked bool
}

func (h *hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijacked = true
	return nil, nil, nil
}

// TestStatusRecorderFlushes proves the metrics wrapper forwards Flush to the
// underlying writer, so streaming responses are not broken by the metrics
// middleware (issue #138).
func TestStatusRecorderFlushes(t *testing.T) {
	rec := &flusherRecorder{ResponseWriter: httptest.NewRecorder()}
	sr := &statusRecorder{ResponseWriter: rec, status: http.StatusOK}

	sr.Flush()

	if !rec.flushed {
		t.Error("expected Flush to reach the underlying ResponseWriter")
	}
}

// TestStatusRecorderHijacks proves the metrics wrapper forwards Hijack to
// the underlying writer, so connection hijacking (WebSockets) still works
// behind the metrics middleware (issue #138).
func TestStatusRecorderHijacks(t *testing.T) {
	rec := &hijackableRecorder{ResponseWriter: httptest.NewRecorder()}
	sr := &statusRecorder{ResponseWriter: rec, status: http.StatusOK}

	if _, _, err := sr.Hijack(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rec.hijacked {
		t.Error("expected Hijack to reach the underlying ResponseWriter")
	}
}

// TestStatusRecorderHijackUnsupported proves the wrapper reports a clear
// error when the underlying writer cannot be hijacked.
func TestStatusRecorderHijackUnsupported(t *testing.T) {
	sr := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
	if _, _, err := sr.Hijack(); err == nil {
		t.Error("expected an error for a non-hijackable ResponseWriter")
	}
}

// TestStatusRecorderRecordsFirstStatus proves the recorded status is the
// FIRST one written, matching what the client actually received.
func TestStatusRecorderRecordsFirstStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := &statusRecorder{ResponseWriter: rec, status: http.StatusOK}

	sr.WriteHeader(http.StatusNotFound)
	sr.WriteHeader(http.StatusInternalServerError)

	if sr.status != http.StatusNotFound {
		t.Errorf("expected the first status 404, got %d", sr.status)
	}
}

// TestStatusRecorderUnwrap proves the wrapper exposes the underlying
// writer, so http.ResponseController can reach it.
func TestStatusRecorderUnwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := &statusRecorder{ResponseWriter: rec, status: http.StatusOK}
	if sr.Unwrap() != rec {
		t.Error("expected Unwrap to return the underlying ResponseWriter")
	}
}

// TestActorIDMiddlewareRejectsSpoofedHeaderWhenAppEnvUnset is the regression
// test for issue #134: an UNSET APP_ENV counts as production, so the
// spoofable X-Actor-ID header must NOT be accepted even when
// ALLOW_ACTOR_HEADER=true.
func TestActorIDMiddlewareRejectsSpoofedHeaderWhenAppEnvUnset(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("ALLOW_ACTOR_HEADER", "true")

	h := ActorIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("X-Actor-ID", "attacker")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with APP_ENV unset, got %d", rec.Code)
	}
}

// TestActorIDMiddlewareRejectsSpoofedHeaderInProduction proves the same for
// an explicit production APP_ENV.
func TestActorIDMiddlewareRejectsSpoofedHeaderInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("ALLOW_ACTOR_HEADER", "true")

	h := ActorIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("X-Actor-ID", "attacker")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 in production, got %d", rec.Code)
	}
}

// TestActorIDMiddlewareAcceptsHeaderInDevelopment proves the dev/test escape
// hatch still works.
func TestActorIDMiddlewareAcceptsHeaderInDevelopment(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("ALLOW_ACTOR_HEADER", "true")

	var actor string
	h := ActorIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor = GetActorID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("X-Actor-ID", "dev-actor")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 in development, got %d", rec.Code)
	}
	if actor != "dev-actor" {
		t.Errorf("expected the X-Actor-ID header to be used in development, got %q", actor)
	}
}

// TestActorIDMiddlewarePrefersJWTClaims proves a real JWT identity wins over
// the header.
func TestActorIDMiddlewarePrefersJWTClaims(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("ALLOW_ACTOR_HEADER", "true")

	var actor string
	h := ActorIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor = GetActorID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("X-Actor-ID", "dev-actor")
	ctx := SetClaims(req.Context(), &JWTClaims{PreferredUsername: "alice"})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if actor != "alice" {
		t.Errorf("expected the JWT identity to win, got %q", actor)
	}
}
