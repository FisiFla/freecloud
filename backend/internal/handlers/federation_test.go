package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

// federationID is a stable UUID for test rows.
const federationID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
const federationComponentID = "kc-comp-1234"

// unmarshalFedData decodes the APIResponse envelope and re-marshals the data
// field into dst. Tests use this instead of unmarshaling the raw body directly.
func unmarshalFedData(t *testing.T, body []byte, dst any) {
	t.Helper()
	var env APIResponse
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("failed to parse envelope: %v", err)
	}
	if !env.Success {
		t.Fatalf("API returned success=false: %s", env.Error)
	}
	raw, err := json.Marshal(env.Data)
	if err != nil {
		t.Fatalf("failed to re-marshal data: %v", err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatalf("failed to unmarshal data into target: %v", err)
	}
}

// stubFederationRow returns a fake DB row that scans as a single federation source.
func stubFederationRow() fakeRow {
	return fakeRow{scanFn: func(dest ...any) error {
		now := time.Now()
		vals := []any{
			federationID, "Corp AD", "ldap", "ad",
			`{"connectionUrl":"ldap://dc.example.com","bindDn":"cn=svc","usersDn":"ou=users"}`,
			federationComponentID,
			"", // last_sync_at
			"", // last_sync_status
			now, now,
		}
		for i, d := range dest {
			if i >= len(vals) {
				break
			}
			switch p := d.(type) {
			case *string:
				if v, ok := vals[i].(string); ok {
					*p = v
				}
			case *time.Time:
				if v, ok := vals[i].(time.Time); ok {
					*p = v
				}
			}
		}
		return nil
	}}
}

// --- CreateFederationSource ---

func TestCreateFederationSource_MissingBindPassword(t *testing.T) {
	h := &Handler{
		keycloak:         &fakeKeycloak{},
		db:               &fakeDB{},
		logger:           zap.NewNop(),
		ldapBindPassword: "",
	}
	body, _ := json.Marshal(map[string]string{
		"name": "Corp AD", "connectionUrl": "ldap://dc.example.com",
		"bindDn": "cn=svc", "usersDn": "ou=users",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/sources", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.CreateFederationSource(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCreateFederationSource_MissingField(t *testing.T) {
	h := &Handler{
		keycloak:         &fakeKeycloak{},
		db:               &fakeDB{},
		logger:           zap.NewNop(),
		ldapBindPassword: "secret",
	}
	body, _ := json.Marshal(map[string]string{"name": "Corp AD"}) // missing required fields
	req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/sources", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.CreateFederationSource(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCreateFederationSource_Success(t *testing.T) {
	h := &Handler{
		keycloak:         &fakeKeycloak{},
		ldapBindPassword: "secret",
		logger:           zap.NewNop(),
		db: &fakeDB{
			queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
				return stubFederationRow()
			},
		},
	}
	body, _ := json.Marshal(map[string]string{
		"name": "Corp AD", "connectionUrl": "ldap://dc.example.com",
		"bindDn": "cn=svc", "usersDn": "ou=users",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/sources", bytes.NewReader(body))
	req = withOrgContext(req)
	rr := httptest.NewRecorder()
	h.CreateFederationSource(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201 got %d: %s", rr.Code, rr.Body.String())
	}
	var out FederationSource
	unmarshalFedData(t, rr.Body.Bytes(), &out)
	if out.KeycloakComponentID != federationComponentID {
		t.Errorf("unexpected component id: %q", out.KeycloakComponentID)
	}
}

// --- ListFederationSources ---

func TestListFederationSources_Empty(t *testing.T) {
	h := &Handler{
		keycloak: &fakeKeycloak{},
		logger:   zap.NewNop(),
		db: &fakeDB{
			queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
				return &fakeQueryRows{}, nil
			},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/sources", nil)
	req = withOrgContext(req)
	rr := httptest.NewRecorder()
	h.ListFederationSources(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	var out []FederationSource
	unmarshalFedData(t, rr.Body.Bytes(), &out)
	if len(out) != 0 {
		t.Errorf("expected empty slice got %d", len(out))
	}
}

func TestListFederationSources_NilDB(t *testing.T) {
	h := &Handler{keycloak: &fakeKeycloak{}, logger: zap.NewNop(), db: nil}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/sources", nil)
	rr := httptest.NewRecorder()
	h.ListFederationSources(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
}

// --- GetFederationSource ---

func TestGetFederationSource_NotFound(t *testing.T) {
	h := &Handler{
		keycloak: &fakeKeycloak{},
		logger:   zap.NewNop(),
		db:       &fakeDB{},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/sources/"+federationID, nil)
	req = chiCtxWithID(req, "id", federationID)
	req = withOrgContext(req)
	rr := httptest.NewRecorder()
	h.GetFederationSource(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got %d", rr.Code)
	}
}

func TestGetFederationSource_Found(t *testing.T) {
	h := &Handler{
		keycloak: &fakeKeycloak{},
		logger:   zap.NewNop(),
		db: &fakeDB{
			queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
				return stubFederationRow()
			},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/sources/"+federationID, nil)
	req = chiCtxWithID(req, "id", federationID)
	req = withOrgContext(req)
	rr := httptest.NewRecorder()
	h.GetFederationSource(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d: %s", rr.Code, rr.Body.String())
	}
	var out FederationSource
	unmarshalFedData(t, rr.Body.Bytes(), &out)
	if out.ID != federationID {
		t.Errorf("unexpected id %q", out.ID)
	}
}

// --- DeleteFederationSource ---

func TestDeleteFederationSource_NotFound(t *testing.T) {
	h := &Handler{
		keycloak: &fakeKeycloak{},
		logger:   zap.NewNop(),
		db:       &fakeDB{},
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/federation/sources/"+federationID, nil)
	req = chiCtxWithID(req, "id", federationID)
	req = withOrgContext(req)
	rr := httptest.NewRecorder()
	h.DeleteFederationSource(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got %d", rr.Code)
	}
}

func TestDeleteFederationSource_Success(t *testing.T) {
	h := &Handler{
		keycloak: &fakeKeycloak{},
		logger:   zap.NewNop(),
		db: &fakeDB{
			queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
				return fakeRow{scanFn: func(dest ...any) error {
					if p, ok := dest[0].(*string); ok {
						*p = federationComponentID
					}
					return nil
				}}
			},
			execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
				return pgconn.CommandTag{}, nil
			},
		},
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/federation/sources/"+federationID, nil)
	req = chiCtxWithID(req, "id", federationID)
	req = withOrgContext(req)
	rr := httptest.NewRecorder()
	h.DeleteFederationSource(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d: %s", rr.Code, rr.Body.String())
	}
	var out map[string]interface{}
	unmarshalFedData(t, rr.Body.Bytes(), &out)
	if out["deleted"] != true {
		t.Errorf("expected deleted=true got %v", out)
	}
}

// --- TestFederationConnection ---

func TestTestFederationConnection_Success(t *testing.T) {
	h := &Handler{
		keycloak:         &fakeKeycloak{},
		ldapBindPassword: "secret",
		logger:           zap.NewNop(),
		db: &fakeDB{
			queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
				return fakeRow{scanFn: func(dest ...any) error {
					vals := []any{
						federationID, federationComponentID,
						`{"connectionUrl":"ldap://dc.example.com","bindDn":"cn=svc","usersDn":"ou=users"}`,
					}
					for i, d := range dest {
						if i >= len(vals) {
							break
						}
						if p, ok := d.(*string); ok {
							if v, ok2 := vals[i].(string); ok2 {
								*p = v
							}
						}
					}
					return nil
				}}
			},
		},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/sources/"+federationID+"/test", nil)
	req = chiCtxWithID(req, "id", federationID)
	req = withOrgContext(req)
	rr := httptest.NewRecorder()
	h.TestFederationConnection(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d: %s", rr.Code, rr.Body.String())
	}
	var out map[string]interface{}
	unmarshalFedData(t, rr.Body.Bytes(), &out)
	if out["success"] != true {
		t.Errorf("expected success=true got %v", out)
	}
}

// --- TriggerFederationSync ---

func TestTriggerFederationSync_NotFound(t *testing.T) {
	h := &Handler{
		keycloak: &fakeKeycloak{},
		logger:   zap.NewNop(),
		db:       &fakeDB{},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/sources/"+federationID+"/sync", nil)
	req = chiCtxWithID(req, "id", federationID)
	req = withOrgContext(req)
	rr := httptest.NewRecorder()
	h.TriggerFederationSync(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got %d", rr.Code)
	}
}

func TestTriggerFederationSync_Success(t *testing.T) {
	h := &Handler{
		keycloak: &fakeKeycloak{},
		logger:   zap.NewNop(),
		db: &fakeDB{
			queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
				return fakeRow{scanFn: func(dest ...any) error {
					if p, ok := dest[0].(*string); ok {
						*p = federationComponentID
					}
					return nil
				}}
			},
			execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
				return pgconn.CommandTag{}, nil
			},
		},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/sources/"+federationID+"/sync", nil)
	req = chiCtxWithID(req, "id", federationID)
	req = withOrgContext(req)
	rr := httptest.NewRecorder()
	h.TriggerFederationSync(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d: %s", rr.Code, rr.Body.String())
	}
	var out map[string]interface{}
	unmarshalFedData(t, rr.Body.Bytes(), &out)
	if out["synced"] != true {
		t.Errorf("expected synced=true got %v", out)
	}
}

// --- compilation smoke-test for all 7 handler method signatures ---

func TestFederationHandlerSignatures(t *testing.T) {
	_ = (&Handler{keycloak: &fakeKeycloak{}, logger: zap.NewNop()}).CreateFederationSource
	_ = (&Handler{keycloak: &fakeKeycloak{}, logger: zap.NewNop()}).ListFederationSources
	_ = (&Handler{keycloak: &fakeKeycloak{}, logger: zap.NewNop()}).GetFederationSource
	_ = (&Handler{keycloak: &fakeKeycloak{}, logger: zap.NewNop()}).UpdateFederationSource
	_ = (&Handler{keycloak: &fakeKeycloak{}, logger: zap.NewNop()}).DeleteFederationSource
	_ = (&Handler{keycloak: &fakeKeycloak{}, logger: zap.NewNop()}).TestFederationConnection
	_ = (&Handler{keycloak: &fakeKeycloak{}, logger: zap.NewNop()}).TriggerFederationSync
}
