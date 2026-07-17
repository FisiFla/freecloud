package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

func TestI03_MoveHostToTeam_RejectsOverBatch(t *testing.T) {
	// Production path: > maxHostIDsPerMove rejected before ownership/Fleet loops.
	ids := make([]string, maxHostIDsPerMove+1)
	for i := range ids {
		ids[i] = "host-" + string(rune('a'+(i%26))) + string(rune('0'+i%10))
	}
	// Prefer unique-ish ids without depending on fmt in tight loop
	for i := range ids {
		ids[i] = "h" + itoa(i)
	}
	db := &fakeDB{queryRowFn: ownershipFoundQueryRowFn(nil)}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(MoveHostRequest{HostIDs: ids})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams/1/hosts", bytes.NewReader(body))
	req = withTeamID(req, "1")
	req = withOrgAdminCtx(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.MoveHostToTeam(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for over-batch, got %d: %s", rec.Code, rec.Body.String())
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
