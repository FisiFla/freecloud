package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Host struct {
	ID             string `json:"id"`
	Hostname       string `json:"hostname"`
	OsVersion      string `json:"os_version"`
	Status         string `json:"status"`
	DiskEncryption bool   `json:"disk_encryption"`
	Firewall       bool   `json:"firewall"`
}

type Software struct {
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	Vulnerabilities []string `json:"vulnerabilities"`
}

// enrollCounter makes each issued enrollment token (and simulated host) unique
// so repeated onboardings don't collide on the token primary key.
var enrollCounter int64

// In-memory Fleet teams for CreateTeam → ListTeams multi-tenant e2e.
var (
	teamMu     sync.Mutex
	teamNextID = 1
	teams      = []map[string]interface{}{
		{"id": 1, "name": "Default", "description": "Default team"},
	}
)

func listTeams() []map[string]interface{} {
	teamMu.Lock()
	defer teamMu.Unlock()
	out := make([]map[string]interface{}, len(teams))
	copy(out, teams)
	return out
}

func createTeam(name, description string) map[string]interface{} {
	teamMu.Lock()
	defer teamMu.Unlock()
	teamNextID++
	tm := map[string]interface{}{
		"id":          teamNextID,
		"name":        name,
		"description": description,
	}
	teams = append(teams, tm)
	return tm
}

func main() {
	http.HandleFunc("/api/v1/fleet/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		path := strings.TrimPrefix(r.URL.Path, "/api/v1/fleet")

		switch {
		case path == "/status":
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		case path == "/hosts" || path == "/hosts/":
			hosts := []Host{
				{ID: "host-001", Hostname: "demo-laptop", OsVersion: "macOS 15.0", Status: "online", DiskEncryption: true, Firewall: true},
				{ID: "host-002", Hostname: "demo-server", OsVersion: "Ubuntu 24.04", Status: "online"},
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"hosts": hosts})

		case strings.HasPrefix(path, "/hosts/") && strings.HasSuffix(path, "/software"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"software": []Software{
					{Name: "Firefox", Version: "125.0", Vulnerabilities: []string{}},
					{Name: "Slack", Version: "4.39.0", Vulnerabilities: []string{}},
				},
			})

		case strings.HasPrefix(path, "/hosts/") && strings.HasSuffix(path, "/lock"):
			json.NewEncoder(w).Encode(map[string]string{"status": "lock_command_sent"})

		case strings.HasPrefix(path, "/hosts/") && strings.HasSuffix(path, "/wipe"):
			hostID := strings.TrimSuffix(strings.TrimPrefix(path, "/hosts/"), "/wipe")
			json.NewEncoder(w).Encode(map[string]string{"status": "wipe_command_sent", "host_id": hostID})

		case strings.HasPrefix(path, "/hosts/"):
			hostID := strings.TrimPrefix(strings.TrimSuffix(path, "/"), "/hosts/")
			h := Host{ID: hostID, Hostname: "demo-laptop", OsVersion: "macOS 15.0", Status: "online"}
			if hostID == "host-001" {
				h.DiskEncryption = true
				h.Firewall = true
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"host": h})

		case path == "/setup_experience/enrollment_tokens" || strings.HasPrefix(path, "/setup_experience/enrollment_tokens"):
			if r.Method == "POST" {
				n := atomic.AddInt64(&enrollCounter, 1)
				token := fmt.Sprintf("fleet-mock-token-%d", n)
				json.NewEncoder(w).Encode(map[string]string{"token": token})
				// Simulate a host checking in shortly after, so the FreeCloud
				// device↔user mapping gets populated end-to-end.
				go simulateEnrollment(token, fmt.Sprintf("host-mock-%d", n))
			} else {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"enrollment_tokens": []map[string]string{
						{"token": "fleet-mock-enrollment-token-abc123", "name": "Default"},
					},
				})
			}

		case path == "/policies" || path == "/policies/":
			if r.Method == "GET" {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"policies": []map[string]interface{}{
						{"id": "pol-001", "name": "Disk Encryption Required", "description": "Ensure disk encryption is enabled"},
					},
				})
			} else {
				json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			}

		case path == "/teams" || path == "/teams/":
			// Persist teams so CreateTeam → ListTeams e2e (fleet_team_orgs) works.
			if r.Method == "GET" {
				json.NewEncoder(w).Encode(map[string]interface{}{"teams": listTeams()})
			} else if r.Method == "POST" {
				var body struct {
					Name        string `json:"name"`
					Description string `json:"description"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body.Name == "" {
					body.Name = "e2e-team"
				}
				tm := createTeam(body.Name, body.Description)
				json.NewEncoder(w).Encode(map[string]interface{}{"team": tm})
			} else {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}

		case strings.HasPrefix(path, "/teams/") && strings.HasSuffix(path, "/policies"):
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		case path == "/hosts/transfer/teams":
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		default:
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
		}
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	port := "8080"
	log.Printf("FleetDM mock server starting on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

// simulateEnrollment mimics a device enrolling with FleetDM shortly after a
// token is issued: it calls the FreeCloud enrollment callback (HMAC-signed with
// FLEET_WEBHOOK_SECRET) so the device↔user mapping is populated for end-to-end
// testing. It is a no-op unless BACKEND_URL and FLEET_WEBHOOK_SECRET are set.
func simulateEnrollment(token, hostID string) {
	backend := os.Getenv("BACKEND_URL")
	secret := os.Getenv("FLEET_WEBHOOK_SECRET")
	if backend == "" || secret == "" {
		return
	}

	payload, _ := json.Marshal(map[string]string{
		"enrollment_token": token,
		"host_id":          hostID,
		"hostname":         hostID + ".local",
		"os_version":       "macOS 15.0",
	})
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	url := strings.TrimSuffix(backend, "/") + "/api/v1/fleet/enrollment-callback"

	// Retry a few times in case the backend isn't accepting connections yet.
	for i := 0; i < 10; i++ {
		time.Sleep(2 * time.Second)
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Fleet-Signature", sig)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("enrollment callback attempt %d for %s failed: %v", i+1, hostID, err)
			continue
		}
		resp.Body.Close()
		log.Printf("enrollment callback for %s -> %d", hostID, resp.StatusCode)
		if resp.StatusCode < 500 {
			return
		}
	}
}
