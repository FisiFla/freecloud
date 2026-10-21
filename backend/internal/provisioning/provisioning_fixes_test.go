package provisioning

// Regression tests for the provisioning engine fixes:
//   - #167 the retry-count read error was discarded (infinite retry) and the
//     backoff shift overflowed int64.
//   - #168 ApplyAttributeMap iterated a map, so a collision between two
//     default fields had a non-deterministic winner.
//   - #169 the final "mark provisioned/deprovisioned" UPDATE error was logged
//     but nil was returned, so the caller believed the write had succeeded.
//   - #147 the outbound SCIM group lookup interpolated an unescaped group name
//     into the filter query.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

// ---- #167: retry count read + saturating backoff ----

// TestProvisioningBackoff_SaturatesInsteadOfOverflowing proves the backoff
// never wraps around to a negative duration (which would schedule the next
// retry in the past — an immediate retry loop).
func TestProvisioningBackoff_SaturatesInsteadOfOverflowing(t *testing.T) {
	cases := []struct {
		retryCount int
		want       time.Duration
	}{
		{0, 5 * time.Minute},
		{1, 10 * time.Minute},
		{2, 20 * time.Minute},
	}
	for _, tc := range cases {
		if got := provisioningBackoff(tc.retryCount); got != tc.want {
			t.Errorf("provisioningBackoff(%d)=%v want %v", tc.retryCount, got, tc.want)
		}
	}
	// 5m << 5 = 160m exceeds the 2h cap.
	if got := provisioningBackoff(5); got != 2*time.Hour {
		t.Errorf("provisioningBackoff(5)=%v want 2h", got)
	}
	// 61 is where `5 << 61` overflows int64.
	for _, rc := range []int{61, 62, 1000} {
		got := provisioningBackoff(rc)
		if got <= 0 {
			t.Errorf("provisioningBackoff(%d)=%v: backoff must not be negative", rc, got)
		}
		if got != 2*time.Hour {
			t.Errorf("provisioningBackoff(%d)=%v want the 2h cap", rc, got)
		}
	}
}

// TestEngineRecordError_ReadFailureDoesNotResetRetryCount proves that a failed
// retry-count read is surfaced instead of being treated as "retry 0" — which
// used to overwrite the stored counter and make maxRetries unreachable.
func TestEngineRecordError_ReadFailureDoesNotResetRetryCount(t *testing.T) {
	readErr := errors.New("connection reset")
	var execSQLs []string
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error { return readErr }}
		},
		execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			execSQLs = append(execSQLs, sql)
			return pgconn.NewCommandTag("UPDATE 1"), nil
		},
	}
	eng := NewEngine(db, zap.NewNop())

	err := eng.recordError(context.Background(), "app-1", "user-1", "rid-1", errors.New("boom"))
	if err == nil {
		t.Fatal("expected an error when the retry count cannot be read")
	}
	if !errors.Is(err, readErr) {
		t.Errorf("expected the read error to be wrapped, got %v", err)
	}
	for _, sql := range execSQLs {
		if strings.Contains(sql, "retry_count") {
			t.Errorf("state must not be rewritten when the retry count is unknown: %s", sql)
		}
	}
}

// ---- #169: final state UPDATE errors are surfaced ----

func TestEngineProvisionUser_FinalStateUpdateErrorIsReturned(t *testing.T) {
	updateErr := errors.New("deadlock detected")
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
		execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			if strings.Contains(sql, "status = 'provisioned'") {
				return pgconn.CommandTag{}, updateErr
			}
			return pgconn.NewCommandTag("INSERT 0 1"), nil
		},
	}
	eng := NewEngine(db, zap.NewNop())
	eng.RegisterConnector("app-1", &mockConnector{})

	err := eng.ProvisionUser(context.Background(), "app-1", ProvisionableUser{ID: "user-1", Email: "a@b.com"})
	if err == nil {
		t.Fatal("expected the state-write failure to be surfaced")
	}
	if !errors.Is(err, updateErr) {
		t.Errorf("expected the update error to be wrapped, got %v", err)
	}
}

func TestEngineDeprovisionUser_FinalStateUpdateErrorIsReturned(t *testing.T) {
	updateErr := errors.New("deadlock detected")
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				if len(dest) > 0 {
					if p, ok := dest[0].(*string); ok {
						*p = "rid-1"
					}
				}
				return nil
			}}
		},
		execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			if strings.Contains(sql, "status = 'deprovisioned'") {
				return pgconn.CommandTag{}, updateErr
			}
			return pgconn.NewCommandTag("UPDATE 1"), nil
		},
	}
	eng := NewEngine(db, zap.NewNop())
	eng.RegisterConnector("app-1", &mockConnector{})

	err := eng.DeprovisionUser(context.Background(), "app-1", "user-1")
	if err == nil {
		t.Fatal("expected the state-write failure to be surfaced")
	}
	if !errors.Is(err, updateErr) {
		t.Errorf("expected the update error to be wrapped, got %v", err)
	}
}

// ---- #168: ApplyAttributeMap is deterministic ----

// TestApplyAttributeMap_StableAcrossRuns proves the payload does not depend on
// Go's randomised map iteration order.
func TestApplyAttributeMap_StableAcrossRuns(t *testing.T) {
	user := ProvisionableUser{ID: "u1", Email: "a@b.com", FirstName: "A", LastName: "B", Department: "D"}
	// Both "userName" and "givenName" map to the same remote key: the winner
	// must be stable (and, by the documented rule, the sorted-first default).
	attrMap := map[string]string{
		"userName":  "email",
		"givenName": "email",
	}
	var first map[string]any
	for i := 0; i < 50; i++ {
		got := ApplyAttributeMap(user, attrMap)
		if first == nil {
			first = got
			continue
		}
		if len(got) != len(first) {
			t.Fatalf("run %d: payload size changed: %v vs %v", i, got, first)
		}
		for k, v := range first {
			if got[k] != v {
				t.Fatalf("run %d: key %q changed: %v vs %v", i, k, got[k], v)
			}
		}
	}
	// "department" sorts before "familyName", "givenName" and "userName", but
	// the collision is between "givenName" and "userName": "givenName" sorts
	// first and therefore wins deterministically.
	if first["email"] != "A" {
		t.Errorf("email=%v want the sorted-first default (givenName=A)", first["email"])
	}
}

// ---- #147: outbound SCIM group filter is escaped ----

func TestSCIMConnector_GroupFilterIsEscaped(t *testing.T) {
	var capturedRawQuery string
	var capturedFilter string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Groups" {
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		if r.Method == http.MethodGet {
			capturedRawQuery = r.URL.RawQuery
			capturedFilter = r.URL.Query().Get("filter")
			w.Header().Set("Content-Type", "application/json")
			// No resources: the connector then tries to create the group.
			w.Write([]byte(`{"Resources":[]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"group-1"}`))
	}))
	defer srv.Close()

	c := NewSCIMConnector(srv.URL, "tok")
	// A group name with a space and one with a quote.
	for _, name := range []string{"Sales Team", `x" or displayName co "y`} {
		_, _ = c.findOrCreateGroup(context.Background(), name)
		if capturedRawQuery == "" {
			t.Fatal("no request captured")
		}
		// The raw query must not contain a literal space (it would break the
		// request line) and must not contain a literal double quote.
		if strings.Contains(capturedRawQuery, " ") {
			t.Errorf("raw query contains a literal space: %q", capturedRawQuery)
		}
		if strings.Contains(capturedRawQuery, `"`) {
			t.Errorf("raw query contains an unescaped quote: %q", capturedRawQuery)
		}
		// The decoded filter must be the intended expression — the quote in
		// the name must not terminate the string literal early.
		if capturedFilter != `displayName eq "`+name+`"` {
			t.Errorf("filter=%q want %q", capturedFilter, `displayName eq "`+name+`"`)
		}
	}
}
