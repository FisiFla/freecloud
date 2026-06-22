package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FisiFla/freecloud/backend/internal/keycloak"
	"go.uber.org/zap"
)

// ---- policyParam parsing ----

func TestPolicyParam(t *testing.T) {
	cases := []struct {
		policy string
		name   string
		want   int
	}{
		{"length(12) and upperCase(1)", "length", 12},
		{"length(12) and upperCase(1)", "upperCase", 1},
		{"length(12) and upperCase(1)", "digits", 0},          // absent → 0
		{"", "length", 0},                                      // empty policy
		{"length(0) and upperCase(1)", "length", 0},            // explicit zero
		{"specialChars(3) and digits(2)", "specialChars", 3},
		{"forceExpiredPasswordChange(90)", "forceExpiredPasswordChange", 90},
		{"passwordHistory(5)", "passwordHistory", 5},
	}
	for _, tc := range cases {
		t.Run(tc.name+"="+tc.policy, func(t *testing.T) {
			got := policyParam(tc.policy, tc.name)
			if got != tc.want {
				t.Errorf("policyParam(%q, %q) = %d; want %d", tc.policy, tc.name, got, tc.want)
			}
		})
	}
}

// ---- buildPolicyString ----

func TestBuildPolicyString(t *testing.T) {
	cases := []struct {
		name string
		req  UpdateAccountPolicyRequest
		want []string // all of these substrings must appear in the result
		absent []string // none of these should appear
	}{
		{
			name: "all fields set",
			req: UpdateAccountPolicyRequest{
				MinLength: 12, UpperCase: 1, LowerCase: 1,
				Digits: 1, SpecialChars: 1,
				PasswordHistory: 5, PasswordExpireDays: 90,
			},
			want: []string{"length(12)", "upperCase(1)", "lowerCase(1)", "digits(1)", "specialChars(1)", "passwordHistory(5)", "forceExpiredPasswordChange(90)"},
		},
		{
			name: "zeroes omitted",
			req:  UpdateAccountPolicyRequest{MinLength: 8},
			want: []string{"length(8)"},
			absent: []string{"upperCase", "lowerCase", "digits", "specialChars", "passwordHistory", "forceExpiredPasswordChange"},
		},
		{
			name: "all zeroes → empty string",
			req:  UpdateAccountPolicyRequest{},
			want: []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildPolicyString(tc.req)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("buildPolicyString result %q missing %q", got, w)
				}
			}
			for _, a := range tc.absent {
				if strings.Contains(got, a) {
					t.Errorf("buildPolicyString result %q should not contain %q", got, a)
				}
			}
		})
	}
}

// ---- validateAccountPolicy ----

func TestValidateAccountPolicy(t *testing.T) {
	valid := UpdateAccountPolicyRequest{
		MinLength: 12, UpperCase: 1, LowerCase: 1, Digits: 1, SpecialChars: 1,
		PasswordHistory: 5, PasswordExpireDays: 90,
		BruteForceProtected: true, FailureFactor: 5,
		WaitIncrementSeconds: 60, MaxFailureWaitSeconds: 900,
		QuickLoginCheckMilliSeconds: 1000, MinimumQuickLoginWaitSeconds: 60,
		MaxDeltaTimeSeconds: 43200,
	}

	t.Run("valid request", func(t *testing.T) {
		errs := validateAccountPolicy(valid)
		if len(errs) != 0 {
			t.Errorf("expected no errors, got %+v", errs)
		}
	})

	cases := []struct {
		name  string
		mutate func(r *UpdateAccountPolicyRequest)
		field string
	}{
		{"minLength negative", func(r *UpdateAccountPolicyRequest) { r.MinLength = -1 }, "minLength"},
		{"minLength too high", func(r *UpdateAccountPolicyRequest) { r.MinLength = 257 }, "minLength"},
		{"upperCase negative", func(r *UpdateAccountPolicyRequest) { r.UpperCase = -1 }, "upperCase"},
		{"lowerCase negative", func(r *UpdateAccountPolicyRequest) { r.LowerCase = -1 }, "lowerCase"},
		{"digits negative", func(r *UpdateAccountPolicyRequest) { r.Digits = -1 }, "digits"},
		{"specialChars negative", func(r *UpdateAccountPolicyRequest) { r.SpecialChars = -1 }, "specialChars"},
		{"passwordHistory too high", func(r *UpdateAccountPolicyRequest) { r.PasswordHistory = 101 }, "passwordHistory"},
		{"passwordExpireDays too high", func(r *UpdateAccountPolicyRequest) { r.PasswordExpireDays = 3651 }, "passwordExpireDays"},
		{"failureFactor negative", func(r *UpdateAccountPolicyRequest) { r.FailureFactor = -1 }, "failureFactor"},
		{"waitIncrementSeconds too high", func(r *UpdateAccountPolicyRequest) { r.WaitIncrementSeconds = 86401 }, "waitIncrementSeconds"},
		{"maxFailureWaitSeconds negative", func(r *UpdateAccountPolicyRequest) { r.MaxFailureWaitSeconds = -1 }, "maxFailureWaitSeconds"},
		{"quickLoginCheckMilliSeconds negative", func(r *UpdateAccountPolicyRequest) { r.QuickLoginCheckMilliSeconds = -1 }, "quickLoginCheckMilliSeconds"},
		{"minimumQuickLoginWaitSeconds negative", func(r *UpdateAccountPolicyRequest) { r.MinimumQuickLoginWaitSeconds = -1 }, "minimumQuickLoginWaitSeconds"},
		{"maxDeltaTimeSeconds too high", func(r *UpdateAccountPolicyRequest) { r.MaxDeltaTimeSeconds = 2592001 }, "maxDeltaTimeSeconds"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := valid // copy
			tc.mutate(&req)
			errs := validateAccountPolicy(req)
			if len(errs) == 0 {
				t.Fatalf("expected validation error for %s", tc.field)
			}
			found := false
			for _, e := range errs {
				if e.Field == tc.field {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected error on field %q, got %+v", tc.field, errs)
			}
		})
	}
}

// ---- HTTP handler tests ----

func TestGetAccountPolicy(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, nil, zap.NewNop())
	r := newTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/account-policy", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success bool `json:"success"`
		Data    AccountPolicyResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Success {
		t.Error("expected success=true")
	}
	if resp.Data.MinLength != 12 {
		t.Errorf("minLength: want 12, got %d", resp.Data.MinLength)
	}
	if !resp.Data.BruteForceProtected {
		t.Error("expected bruteForceProtected=true")
	}
}

func TestGetAccountPolicyKeycloakError(t *testing.T) {
	kc := &fakeKeycloak{
		getRealmPolicyFn: func(_ context.Context) (*keycloak.RealmPolicyResult, error) {
			return nil, &testErr{"keycloak down"}
		},
	}
	h := NewHandler(nil, kc, nil, zap.NewNop())
	r := newTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/account-policy", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestUpdateAccountPolicy(t *testing.T) {
	var captured keycloak.UpdateRealmPolicyRequest
	kc := &fakeKeycloak{
		updateRealmPolicyFn: func(_ context.Context, req keycloak.UpdateRealmPolicyRequest) error {
			captured = req
			return nil
		},
	}
	h := NewHandler(nil, kc, nil, zap.NewNop())
	r := newTestRouter(h)

	body := `{
		"minLength": 14,
		"upperCase": 2,
		"lowerCase": 1,
		"digits": 1,
		"specialChars": 1,
		"passwordHistory": 3,
		"passwordExpireDays": 0,
		"bruteForceProtected": true,
		"failureFactor": 10,
		"waitIncrementSeconds": 120,
		"maxFailureWaitSeconds": 600,
		"quickLoginCheckMilliSeconds": 2000,
		"minimumQuickLoginWaitSeconds": 30,
		"maxDeltaTimeSeconds": 86400
	}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/account-policy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(captured.PasswordPolicy, "length(14)") {
		t.Errorf("policy missing length(14): %q", captured.PasswordPolicy)
	}
	if !strings.Contains(captured.PasswordPolicy, "upperCase(2)") {
		t.Errorf("policy missing upperCase(2): %q", captured.PasswordPolicy)
	}
	if !strings.Contains(captured.PasswordPolicy, "passwordHistory(3)") {
		t.Errorf("policy missing passwordHistory(3): %q", captured.PasswordPolicy)
	}
	// PasswordExpireDays=0 → absent
	if strings.Contains(captured.PasswordPolicy, "forceExpiredPasswordChange") {
		t.Errorf("policy should not contain forceExpiredPasswordChange: %q", captured.PasswordPolicy)
	}
	if captured.FailureFactor != 10 {
		t.Errorf("failureFactor: want 10, got %d", captured.FailureFactor)
	}
}

func TestUpdateAccountPolicyValidationErrors(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, nil, zap.NewNop())
	r := newTestRouter(h)

	// minLength out of range
	body := `{"minLength": -5}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/account-policy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateAccountPolicyBadJSON(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, nil, zap.NewNop())
	r := newTestRouter(h)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/account-policy", strings.NewReader("{bad json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// testErr is a minimal error type for triggering error paths in tests.
type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }
