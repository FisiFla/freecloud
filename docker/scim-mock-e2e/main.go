// scim-mock-e2e is a minimal SCIM 2.0 Users server used ONLY by the A1
// provisioning round-trip e2e test (TestE2E_Admin_ProvisioningRoundTrip). It
// stands in for a downstream SaaS tenant so the outbound SCIMConnector has a
// genuinely separate target to provision into — pointing it back at this
// backend's own inbound SCIM endpoint (a self-loop) doesn't work because the
// employee being provisioned already exists there by email, which 409s on
// create.
//
// This is NOT a contract-fidelity mock (see A4's httptest-based Slack/GitHub
// contract tests for that) — it just accepts a bearer token and tracks
// created/patched users in memory so the e2e test can assert a remote ID was
// assigned.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

type user struct {
	ID       string                 `json:"id"`
	UserName string                 `json:"userName"`
	Active   bool                   `json:"active"`
	Raw      map[string]interface{} `json:"-"`
}

var (
	mu      sync.Mutex
	users   = map[string]*user{}
	nextID  int64
	authTok string
)

func main() {
	authTok = os.Getenv("SCIM_MOCK_TOKEN")
	if authTok == "" {
		log.Fatal("SCIM_MOCK_TOKEN must be set")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/Users", handleUsers)
	mux.HandleFunc("/Users/", handleUserByID)

	log.Println("scim-mock-e2e listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", withAuth(mux)))
}

func withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+authTok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		id := strconv.FormatInt(atomic.AddInt64(&nextID, 1), 10)
		userName, _ := body["userName"].(string)
		u := &user{ID: id, UserName: userName, Active: true, Raw: body}
		users[id] = u
		mu.Unlock()

		resp := map[string]interface{}{
			"schemas":  []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
			"id":       id,
			"userName": userName,
			"active":   true,
		}
		w.Header().Set("Content-Type", "application/scim+json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(resp)
	case http.MethodGet:
		mu.Lock()
		defer mu.Unlock()
		var resources []map[string]interface{}
		for _, u := range users {
			resources = append(resources, map[string]interface{}{
				"id": u.ID, "userName": u.UserName, "active": u.Active,
			})
		}
		w.Header().Set("Content-Type", "application/scim+json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"schemas":      []string{"urn:ietf:params:scim:api:messages:2.0:ListResponse"},
			"totalResults": len(resources),
			"Resources":    resources,
		})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func handleUserByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/Users/")
	mu.Lock()
	u, ok := users[id]
	mu.Unlock()

	switch r.Method {
	case http.MethodGet:
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/scim+json")
		json.NewEncoder(w).Encode(map[string]interface{}{"id": u.ID, "userName": u.UserName, "active": u.Active})
	case http.MethodPatch:
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var patch map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// Best-effort: look for {"op":"replace","path":"active","value":false}.
		if ops, ok := patch["Operations"].([]interface{}); ok {
			for _, opRaw := range ops {
				op, _ := opRaw.(map[string]interface{})
				if op["path"] == "active" {
					mu.Lock()
					if v, ok := op["value"].(bool); ok {
						u.Active = v
					}
					mu.Unlock()
				}
			}
		}
		w.Header().Set("Content-Type", "application/scim+json")
		json.NewEncoder(w).Encode(map[string]interface{}{"id": u.ID, "userName": u.UserName, "active": u.Active})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
