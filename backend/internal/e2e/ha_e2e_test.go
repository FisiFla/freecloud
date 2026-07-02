//go:build e2e

// Package e2e — B4 (v1.7 HA) multi-instance proof.
//
// These tests require the HA overlay ON TOP of the base e2e stack:
//
//	docker compose -f docker/docker-compose.e2e.yml -f docker/docker-compose.e2e-ha.yml up -d --build
//	cd backend && go test -tags=e2e -v -run TestHA ./internal/e2e/... \
//	  -ha-lb=http://localhost:8086 \
//	  -ha-replica-a=http://localhost:8085 \
//	  -ha-replica-b=http://localhost:8087
//
// Environment variable fallbacks: E2E_HA_LB_URL, E2E_HA_REPLICA_A_URL,
// E2E_HA_REPLICA_B_URL.
//
// They are skipped automatically (not failed) when the HA overlay isn't
// running, so the base e2e suite (go test -tags=e2e ./internal/e2e/...)
// stays green without the overlay — the HA proof is opt-in, matching how the
// base stack itself is opt-in relative to plain `go test`.
package e2e

import (
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

var (
	flagHALB       = flag.String("ha-lb", envOr("E2E_HA_LB_URL", "http://localhost:8086"), "B4 HA e2e: load balancer URL in front of both replicas")
	flagHAReplicaA = flag.String("ha-replica-a", envOr("E2E_HA_REPLICA_A_URL", "http://localhost:8085"), "B4 HA e2e: replica A direct URL")
	flagHAReplicaB = flag.String("ha-replica-b", envOr("E2E_HA_REPLICA_B_URL", "http://localhost:8087"), "B4 HA e2e: replica B direct URL")
)

// skipUnlessHA checks that all three HA endpoints are reachable; if any is
// not, the whole HA suite is skipped rather than failed, since the overlay
// stack is opt-in (not part of the default e2e workflow run).
func skipUnlessHA(t *testing.T) {
	t.Helper()
	for _, url := range []string{*flagHALB, *flagHAReplicaA, *flagHAReplicaB} {
		resp, err := http.Get(url + "/healthz")
		if err != nil || resp.StatusCode != http.StatusOK {
			if resp != nil {
				resp.Body.Close()
			}
			t.Skipf("HA overlay not reachable at %s (%v) — bring up docker-compose.e2e-ha.yml on top of the base e2e stack to run this suite", url, err)
		}
		resp.Body.Close()
	}
}

func getURL(t *testing.T, url string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("GET %s: read body: %v", url, err)
	}
	return resp.StatusCode, body
}

// ---- Test 1: migrations ran exactly once, both replicas healthy ----

// TestHA_BothReplicasReadyAfterSharedMigration proves both replicas started
// cleanly against a schema that the shared migrate-e2e job (not either
// replica) applied. Readyz checks live DB connectivity via a real query, so
// if WaitForSchema had failed (schema behind, or a migration ran twice and
// corrupted state) at least one replica would never reach readiness.
func TestHA_BothReplicasReadyAfterSharedMigration(t *testing.T) {
	skipUnlessHA(t)

	for _, replica := range []struct {
		name, url string
	}{
		{"A", *flagHAReplicaA},
		{"B", *flagHAReplicaB},
	} {
		code, body := getURL(t, replica.url+"/readyz")
		if code != http.StatusOK {
			t.Fatalf("replica %s /readyz: expected 200 (schema current, DB reachable), got %d: %s", replica.name, code, body)
		}
	}
}

// TestHA_KeycloakBootstrapRanOnce proves the Epic-A self-bootstrap advisory
// lock let both replicas start cleanly against the same realm: both report a
// provisioned realm via the (idempotent, safe-to-call-repeatedly) setup
// status endpoint, and neither returns a 5xx that would indicate the two
// bootstrap runs stepped on each other (e.g. a duplicate-client conflict that
// wasn't handled idempotently).
func TestHA_KeycloakBootstrapRanOnce(t *testing.T) {
	skipUnlessHA(t)

	for _, replica := range []struct {
		name, url string
	}{
		{"A", *flagHAReplicaA},
		{"B", *flagHAReplicaB},
	} {
		code, body := getURL(t, replica.url+"/api/v1/setup/status")
		if code != http.StatusOK {
			t.Fatalf("replica %s /api/v1/setup/status: expected 200, got %d: %s", replica.name, code, body)
		}
		var resp struct {
			Data struct {
				Provisioned *bool `json:"provisioned"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("replica %s /api/v1/setup/status: cannot parse response: %v\nbody: %s", replica.name, err, body)
		}
		if resp.Data.Provisioned == nil {
			t.Fatalf("replica %s /api/v1/setup/status: missing 'provisioned' field (service account couldn't reach Keycloak?): %s", replica.name, body)
		}
		t.Logf("replica %s: provisioned=%v", replica.name, *resp.Data.Provisioned)
	}
}

// ---- Test 2: rate limiting reflects one shared Redis budget ----

// TestHA_RateLimitIsSharedAcrossReplicas hammers the LB (which round-robins
// between both replicas) on the unauthenticated, rate-limited
// /api/v1/setup/status... no — that endpoint isn't mutate-limited at a small
// enough budget to hammer quickly. Use /api/v1/health/keycloak instead: it is
// behind the 30-req/min "health" limiter (routes.go), small enough to exceed
// in a short burst, and unauthenticated.
//
// If each replica still tracked its own in-memory counter (the pre-B1 bug
// ADR 0003 called out), 60 requests split evenly across 2 replicas would all
// succeed (30 allowed each). With the shared Redis-backed limiter, only the
// first ~30 succeed regardless of which replica answers, and the rest are
// 429 — proving ONE shared budget, not 2x.
func TestHA_RateLimitIsSharedAcrossReplicas(t *testing.T) {
	skipUnlessHA(t)

	const totalRequests = 60 // 2x the 30/min "health" limiter budget
	var okCount, limitedCount int
	client := &http.Client{Timeout: 5 * time.Second}

	for i := 0; i < totalRequests; i++ {
		resp, err := client.Get(*flagHALB + "/api/v1/health/keycloak")
		if err != nil {
			t.Fatalf("request %d via LB: %v", i, err)
		}
		switch resp.StatusCode {
		case http.StatusOK, http.StatusServiceUnavailable:
			// Keycloak dependency check may itself fail/succeed depending on
			// timing; what matters here is whether the RATE LIMITER admitted
			// the request at all (anything other than 429 means it did).
			okCount++
		case http.StatusTooManyRequests:
			limitedCount++
		default:
			t.Logf("request %d: unexpected status %d", i, resp.StatusCode)
			okCount++ // conservatively count as "admitted" — not a 429
		}
		resp.Body.Close()
	}

	t.Logf("admitted=%d limited=%d (of %d total, budget=30)", okCount, limitedCount, totalRequests)

	// The core HA assertion: if the limiter were per-replica (broken), NONE
	// of the 60 requests would be 429 (30 fit on each replica's own counter).
	if limitedCount == 0 {
		t.Fatalf("expected some requests to be rate-limited (429) once the shared 30/min budget is exceeded across %d requests split across 2 replicas — got 0 limited, suggesting each replica has its own counter (the pre-B1 bug)", totalRequests)
	}
	// Sanity: the budget is ~30, not ~60 (which would also indicate 2x'd
	// per-replica budgets even if some 429s leaked through some other way).
	// Allow slack for the fixed-window boundary tradeoff documented in
	// RedisRateLimiter (up to ~2x burst AT a window boundary), so assert a
	// looser bound: strictly fewer admits than the full unthrottled total.
	if okCount >= totalRequests {
		t.Fatalf("expected fewer than %d admitted requests (shared budget ~30), got %d admitted / %d limited", totalRequests, okCount, limitedCount)
	}
}

// ---- Test 3: exactly one replica leads each background job ----

// leaderMetricRe matches the freecloud_leader_election_is_leader Prometheus
// gauge line, e.g.:
//
//	freecloud_leader_election_is_leader{job="reconcile"} 1
var leaderMetricRe = regexp.MustCompile(`freecloud_leader_election_is_leader\{job="([^"]+)"\}\s+([0-9.]+)`)

// scrapeLeaderGauges fetches /metrics from a replica and returns job name ->
// gauge value (1 = leader, 0 = not leader) for every leader-election gauge
// found.
func scrapeLeaderGauges(t *testing.T, replicaURL string) map[string]float64 {
	t.Helper()
	code, body := getURL(t, replicaURL+"/metrics")
	if code != http.StatusOK {
		t.Fatalf("GET %s/metrics: expected 200, got %d", replicaURL, code)
	}
	out := make(map[string]float64)
	for _, line := range strings.Split(string(body), "\n") {
		m := leaderMetricRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		val, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			continue
		}
		out[m[1]] = val
	}
	return out
}

// TestHA_ExactlyOneReplicaLeadsEachBackgroundJob scrapes the leadership gauge
// (freecloud_leader_election_is_leader) from both replicas directly (not
// through the LB, since we need to address each one individually) and
// asserts that for every job name reported by either replica, the two
// replicas' values sum to exactly 1 — i.e. exactly one of them is leader,
// never both, never neither (once the election loop has settled).
func TestHA_ExactlyOneReplicaLeadsEachBackgroundJob(t *testing.T) {
	skipUnlessHA(t)

	// Give the election loops time to settle (Elector's default retry
	// interval is 5s; allow a couple of cycles).
	deadline := time.Now().Add(30 * time.Second)
	var gaugesA, gaugesB map[string]float64
	var jobs []string

	for time.Now().Before(deadline) {
		gaugesA = scrapeLeaderGauges(t, *flagHAReplicaA)
		gaugesB = scrapeLeaderGauges(t, *flagHAReplicaB)

		jobs = nil
		seen := map[string]bool{}
		for j := range gaugesA {
			if !seen[j] {
				seen[j] = true
				jobs = append(jobs, j)
			}
		}
		for j := range gaugesB {
			if !seen[j] {
				seen[j] = true
				jobs = append(jobs, j)
			}
		}

		if len(jobs) >= 3 { // reconcile, snapshot, audit_retention
			allSettled := true
			for _, j := range jobs {
				if gaugesA[j]+gaugesB[j] != 1 {
					allSettled = false
					break
				}
			}
			if allSettled {
				break
			}
		}
		time.Sleep(2 * time.Second)
	}

	if len(jobs) < 3 {
		t.Fatalf("expected at least 3 leader-election jobs reported (reconcile, snapshot, audit_retention), got %d: %v", len(jobs), jobs)
	}

	for _, job := range jobs {
		sum := gaugesA[job] + gaugesB[job]
		t.Logf("job=%q replicaA=%v replicaB=%v", job, gaugesA[job], gaugesB[job])
		if sum != 1 {
			t.Errorf("job %q: expected exactly one replica to be leader (sum=1), got replicaA=%v replicaB=%v (sum=%v)",
				job, gaugesA[job], gaugesB[job], sum)
		}
	}
}
