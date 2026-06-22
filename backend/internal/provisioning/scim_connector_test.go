package provisioning

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSCIMConnector_ProvisionUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/Users" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("unmarshal body: %v", err)
		}
		if payload["userName"] != "alice@example.com" {
			t.Errorf("userName=%v want alice@example.com", payload["userName"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"remote-user-1","userName":"alice@example.com"}`))
	}))
	defer srv.Close()

	c := NewSCIMConnector(srv.URL, "test-token")
	user := ProvisionableUser{Email: "alice@example.com", FirstName: "Alice", LastName: "Example"}
	remoteID, err := c.ProvisionUser(context.Background(), user)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if remoteID != "remote-user-1" {
		t.Errorf("remoteID=%q want remote-user-1", remoteID)
	}
}

func TestSCIMConnector_DeprovisionUser(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || !strings.HasSuffix(r.URL.Path, "/Users/rid-1") {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewSCIMConnector(srv.URL, "test-token")
	if err := c.DeprovisionUser(context.Background(), "rid-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var patch scimPatchOp
	if err := json.Unmarshal(capturedBody, &patch); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	if len(patch.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(patch.Operations))
	}
	op := patch.Operations[0]
	if op.Op != "replace" || op.Path != "active" {
		t.Errorf("op=%+v want replace active", op)
	}
	// Value should be false (JSON bool)
	if v, ok := op.Value.(bool); !ok || v {
		t.Errorf("op.Value=%v want false", op.Value)
	}
}

func TestSCIMConnector_UpdateUser(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || !strings.HasSuffix(r.URL.Path, "/Users/rid-2") {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := NewSCIMConnector(srv.URL, "test-token")
	user := ProvisionableUser{FirstName: "Bob", LastName: "Smith", Department: "Eng"}
	if err := c.UpdateUser(context.Background(), "rid-2", user); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var patch scimPatchOp
	if err := json.Unmarshal(capturedBody, &patch); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	if len(patch.Operations) == 0 {
		t.Fatal("expected at least one operation")
	}
}

func TestSCIMConnector_InvalidBaseURL(t *testing.T) {
	c := NewSCIMConnector("http://127.0.0.1:1", "tok")
	user := ProvisionableUser{Email: "x@y.com"}
	_, err := c.ProvisionUser(context.Background(), user)
	if err == nil {
		t.Fatal("expected connection error, got nil")
	}
}

func TestSCIMConnector_FullFlow(t *testing.T) {
	// Tracks the sequence of calls: create → update → deactivate.
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/Users":
			calls = append(calls, "create")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"id":"flow-id"}`))
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/Users/flow-id"):
			body, _ := io.ReadAll(r.Body)
			var patch scimPatchOp
			json.Unmarshal(body, &patch)
			// Distinguish update vs deactivate by checking the op value.
			if len(patch.Operations) > 0 {
				if v, ok := patch.Operations[0].Value.(bool); ok && !v {
					calls = append(calls, "deactivate")
				} else {
					calls = append(calls, "update")
				}
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewSCIMConnector(srv.URL, "tok")
	user := ProvisionableUser{Email: "z@example.com", FirstName: "Z", LastName: "X"}

	rid, err := c.ProvisionUser(context.Background(), user)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if rid != "flow-id" {
		t.Fatalf("rid=%q want flow-id", rid)
	}

	if err := c.UpdateUser(context.Background(), rid, user); err != nil {
		t.Fatalf("update: %v", err)
	}

	if err := c.DeprovisionUser(context.Background(), rid); err != nil {
		t.Fatalf("deprovision: %v", err)
	}

	want := []string{"create", "update", "deactivate"}
	if len(calls) != len(want) {
		t.Fatalf("calls=%v want %v", calls, want)
	}
	for i, c := range calls {
		if c != want[i] {
			t.Errorf("calls[%d]=%q want %q", i, c, want[i])
		}
	}
}
