package main

import (
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "strings"
)

type Host struct {
    ID        string `json:"id"`
    Hostname  string `json:"hostname"`
    OsVersion string `json:"os_version"`
    Status    string `json:"status"`
}

type Software struct {
    Name            string   `json:"name"`
    Version         string   `json:"version"`
    Vulnerabilities []string `json:"vulnerabilities"`
}

func main() {
    http.HandleFunc("/api/v1/fleet/", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        
        path := strings.TrimPrefix(r.URL.Path, "/api/v1/fleet")
        
        switch {
        case path == "/hosts" || path == "/hosts/":
            hosts := []Host{
                {ID: "host-001", Hostname: "demo-laptop", OsVersion: "macOS 15.0", Status: "online"},
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
            json.NewEncoder(w).Encode(map[string]string{"status": "wipe_command_sent"})
            
        case strings.HasPrefix(path, "/hosts/"):
            hostID := strings.TrimPrefix(strings.TrimSuffix(path, "/"), "/hosts/")
            json.NewEncoder(w).Encode(map[string]interface{}{
                "host": Host{ID: hostID, Hostname: "demo-laptop", OsVersion: "macOS 15.0", Status: "online"},
            })
            
        case path == "/setup_experience/enrollment_tokens" || strings.HasPrefix(path, "/setup_experience/enrollment_tokens"):
            if r.Method == "POST" {
                json.NewEncoder(w).Encode(map[string]string{
                    "token": "fleet-mock-enrollment-token-abc123",
                })
            } else {
                json.NewEncoder(w).Encode(map[string]interface{}{
                    "enrollment_tokens": []map[string]string{
                        {"token": "fleet-mock-enrollment-token-abc123", "name": "Default"},
                    },
                })
            }
            
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
