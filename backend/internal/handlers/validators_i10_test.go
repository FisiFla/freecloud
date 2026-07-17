package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

func TestI10_RemoteLockWithMessage_RejectsControlDeviceID(t *testing.T) {
	// Production: RemoteLockWithMessage → ValidateHostID before body/Fleet.
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(map[string]string{"message": "hi"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/x/lock-message", bytes.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "dev\x00ice")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = withOrgAdminCtx(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.RemoteLockWithMessage(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for control device id, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestI10_MoveHostToTeam_RejectsOverlongTeamID(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(MoveHostRequest{HostIDs: []string{"h1"}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams/9999999999/hosts", bytes.NewReader(body))
	req = withTeamID(req, "9999999999")
	req = withOrgAdminCtx(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.MoveHostToTeam(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for overlong team id, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestI10_RemoteLockWithMessage_RejectsLongMessage(t *testing.T) {
	// Production: message length gate before Fleet IssueLockWithMessage.
	db := &fakeDB{queryRowFn: ownershipFoundQueryRowFn(nil)}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(map[string]string{"message": string(make([]byte, maxLockMessageLen+1))})
	// fill with 'x' (make gives NUL which would trip control-char first)
	msg := make([]byte, maxLockMessageLen+1)
	for i := range msg {
		msg[i] = 'x'
	}
	body, _ = json.Marshal(map[string]string{"message": string(msg)})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/h1/lock-message", bytes.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "h1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = withOrgAdminCtx(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.RemoteLockWithMessage(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for long message, got %d: %s", rec.Code, rec.Body.String())
	}
}
