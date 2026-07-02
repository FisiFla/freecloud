package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/keycloak"
	"github.com/Nerzal/gocloak/v13"
)

// buildBulkMultipart creates a multipart request with the CSV in a "file" field.
func buildBulkMultipart(t *testing.T, csvContent string) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	fw, err := w.CreateFormFile("file", "employees.csv")
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(fw, csvContent)
	w.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboard/bulk", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

// TestBulkOnboardJSON tests the JSON array path: 2 valid rows → 200 all succeeded.
func TestBulkOnboardJSON(t *testing.T) {
	h := setupTestHandler(t)
	rows := []BulkOnboardRow{
		{FirstName: "Alice", LastName: "A", Email: "alice@example.com", Department: "Eng", Role: "SWE"},
		{FirstName: "Bob", LastName: "B", Email: "bob@example.com", Department: "Eng", Role: "SWE"},
	}
	body, _ := json.Marshal(rows)
	req := withDefaultOrg(httptest.NewRequest(http.MethodPost, "/api/v1/onboard/bulk",
		bytes.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.BulkOnboard(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data BulkOnboardResponse `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if env.Data.Succeeded != 2 {
		t.Errorf("expected 2 succeeded, got %d", env.Data.Succeeded)
	}
}

// TestBulkOnboardCSV tests the CSV multipart path.
func TestBulkOnboardCSV(t *testing.T) {
	h := setupTestHandler(t)
	csv := "firstName,lastName,email,department,role\nCarol,C,carol@example.com,Sales,AE\n"
	req := withDefaultOrg(buildBulkMultipart(t, csv))
	rec := httptest.NewRecorder()
	h.BulkOnboard(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestBulkOnboardSkipsDuplicate proves that an existing email is reported as
// skipped-duplicate (not re-created) and the response is 207.
func TestBulkOnboardSkipsDuplicate(t *testing.T) {
	db := &fakeDB{
		// Idempotency lookup: first call returns existing ID, others return no rows.
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				// Pretend email already exists.
				if sp, ok := dest[0].(*string); ok {
					*sp = "existing-kc-id"
				}
				return nil
			}}
		},
	}
	logger := zap.NewNop()
	kc := &fakeKeycloak{}
	fl := &fakeFleet{}
	h := NewHandler(db, kc, fl, logger)

	rows := []BulkOnboardRow{
		{FirstName: "Dup", LastName: "D", Email: "dup@example.com"},
	}
	body, _ := json.Marshal(rows)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboard/bulk", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.BulkOnboard(rec, req)
	// 207 because at least one row was skipped.
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207, got %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data BulkOnboardResponse `json:"data"`
	}
	json.NewDecoder(rec.Body).Decode(&env)
	if env.Data.Skipped != 1 {
		t.Errorf("expected 1 skipped, got %d", env.Data.Skipped)
	}
}

// TestBulkOnboardPartialFailure: one valid + one invalid row → 207 with mixed results.
func TestBulkOnboardPartialFailure(t *testing.T) {
	h := setupTestHandler(t)
	rows := []BulkOnboardRow{
		{FirstName: "Good", LastName: "G", Email: "good@example.com"},
		{FirstName: "", LastName: "Bad", Email: "bad@example.com"}, // missing firstName
	}
	body, _ := json.Marshal(rows)
	req := withDefaultOrg(httptest.NewRequest(http.MethodPost, "/api/v1/onboard/bulk", bytes.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.BulkOnboard(rec, req)
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207, got %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data BulkOnboardResponse `json:"data"`
	}
	json.NewDecoder(rec.Body).Decode(&env)
	if env.Data.Succeeded != 1 || env.Data.Failed != 1 {
		t.Errorf("expected 1 succeeded + 1 failed, got %+v", env.Data)
	}
}

// TestBulkOnboardEmptyBody returns 400.
func TestBulkOnboardEmptyBody(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboard/bulk",
		strings.NewReader("[]"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.BulkOnboard(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// TestBulkOnboardTooManyRows returns 400 when over maxBulkRows.
func TestBulkOnboardTooManyRows(t *testing.T) {
	h := setupTestHandler(t)
	rows := make([]BulkOnboardRow, maxBulkRows+1)
	for i := range rows {
		rows[i] = BulkOnboardRow{
			FirstName: "F", LastName: "L",
			Email: fmt.Sprintf("u%d@example.com", i),
		}
	}
	body, _ := json.Marshal(rows)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboard/bulk", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.BulkOnboard(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// TestBulkOnboardKCFailure: Keycloak failure on a row → that row is "failed",
// others succeed.
func TestBulkOnboardKCFailure(t *testing.T) {
	calls := 0
	kc := &fakeKeycloak{
		createUserFn: func(ctx context.Context, firstName, lastName, email, department string) (*keycloak.CreateUserResult, error) {
			calls++
			if calls == 1 {
				return nil, fmt.Errorf("keycloak unavailable")
			}
			uid := "kc-ok"
			user := &gocloak.User{ID: &uid, FirstName: &firstName, LastName: &lastName, Email: &email}
			return &keycloak.CreateUserResult{User: user, PasswordSet: true}, nil
		},
	}
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
		beginFn: func(ctx context.Context) (pgx.Tx, error) {
			return &fakeTx{
				execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
					return pgconn.CommandTag{}, nil
				},
			}, nil
		},
	}
	h := NewHandler(db, kc, &fakeFleet{}, zap.NewNop())

	rows := []BulkOnboardRow{
		{FirstName: "Fail", LastName: "F", Email: "fail@example.com"},
		{FirstName: "Ok", LastName: "O", Email: "ok@example.com"},
	}
	body, _ := json.Marshal(rows)
	req := withDefaultOrg(httptest.NewRequest(http.MethodPost, "/api/v1/onboard/bulk", bytes.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.BulkOnboard(rec, req)
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207, got %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data BulkOnboardResponse `json:"data"`
	}
	json.NewDecoder(rec.Body).Decode(&env)
	if env.Data.Failed != 1 || env.Data.Succeeded != 1 {
		t.Errorf("expected 1 failed + 1 succeeded, got %+v", env.Data)
	}
}

// TestParseCSV unit-tests the CSV parser directly.
func TestParseCSV(t *testing.T) {
	csv := "firstName,lastName,email,department,role\nAlice,Smith,alice@x.com,Eng,SWE\n"
	rows, err := parseCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("parseCSV error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Email != "alice@x.com" {
		t.Errorf("expected email alice@x.com, got %s", rows[0].Email)
	}
}

// TestParseCSVMissingRequiredColumn returns error when 'email' column absent.
func TestParseCSVMissingRequiredColumn(t *testing.T) {
	csv := "firstName,lastName\nAlice,Smith\n"
	_, err := parseCSV(strings.NewReader(csv))
	if err == nil {
		t.Error("expected error for missing email column")
	}
}

