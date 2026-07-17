# FreeCloud Observability

FreeCloud exposes a Prometheus `/metrics` endpoint on the backend (same port as
the API). The observability stack in `docker/` adds **Prometheus** (scraping +
alert rules) and **Grafana** (pre-provisioned dashboard).

## Metrics exposed by the backend

| Metric | Type | Description |
|---|---|---|
| `http_requests_total` | Counter | HTTP requests by method, route pattern, and status code |
| `http_request_duration_seconds` | Histogram | Request latency by method and route pattern |
| `freecloud_reconcile_orphans_in_keycloak` | Gauge | Users in Keycloak with no local DB record (last reconcile run) |
| `freecloud_reconcile_orphans_in_db` | Gauge | DB users with no Keycloak record (last reconcile run) |
| `freecloud_reconcile_last_run_timestamp_seconds` | Gauge | Unix timestamp of the last reconciliation run |
| `freecloud_leader_election_is_leader` | Gauge | `1` if this process holds the named job lock, else `0` (label: `job`) |

Leader-election jobs (label values): `reconcile`, `snapshot`, `audit_retention`,
`siem`, `review_schedules`. In a healthy multi-replica deploy, for each job
exactly one replica should report `1`. Zero across all replicas means no
background work for that job; two `1`s for the same job indicates a lock bug.

The backend scrape target is `GET /metrics` (Prometheus exposition format,
unauthenticated). Restrict it at the network/reverse-proxy layer if the API is
publicly reachable.

## Grafana dashboard

`docker/observability/grafana-dashboard.json` — auto-provisioned into Grafana
when the observability stack is running.

Panels:

- **Request rate** — per-route req/s over the last 2 minutes
- **5xx error rate** — percentage of 5xx responses (thresholds: green/yellow/red)
- **P50 / P95 / P99 latency** — histogram quantiles over 2-minute windows
- **Backend / Keycloak / Fleet / Readyz health** — stat panels with color coding
- **Reconciliation drift gauges** — orphan counts in both directions
- **Last reconciliation run** — time since the last successful job run
- **Leader election** — optional: graph `freecloud_leader_election_is_leader` by
  `job` / `instance` (panel not pre-provisioned yet; scrape + PromQL is enough)

## Alert rules

`docker/observability/alerts.yml` defines the following alerts:

| Alert | Severity | Condition |
|---|---|---|
| `BackendDown` | critical | No scrape from the backend for > 1 min |
| `ReadyzDown` | critical | `/readyz` failing for > 2 min |
| `HighErrorRate` | warning | 5xx rate > 5% for 5 min |
| `CriticalErrorRate` | critical | 5xx rate > 25% for 2 min |
| `HighP99Latency` | warning | P99 latency > 2s for 5 min on any route |
| `ReconciliationDriftKeycloak` | warning | Keycloak orphans > 0 for > 30 min |
| `ReconciliationDriftDB` | warning | DB orphans > 0 for > 30 min |
| `ReconciliationJobStale` | warning | Last run timestamp > 1 hour ago |

## Running the observability stack

The observability services are in a separate Compose override so they are
opt-in (not bundled in the production stack by default).

```bash
# Overlay on top of the production stack
docker compose \
  -f docker/docker-compose.prod.yml \
  -f docker/docker-compose.observability.yml \
  --env-file .env.prod \
  up -d

# Grafana is on port 3100 (override with GF_PORT in .env.prod)
# Default admin password: set GF_ADMIN_PASSWORD in .env.prod
```

Grafana datasource (Prometheus at `http://prometheus:9090`) and the FreeCloud
dashboard are provisioned automatically from
`docker/observability/grafana-provisioning/`.

## Environment variables (add to .env.prod)

| Variable | Default | Description |
|---|---|---|
| `GF_ADMIN_PASSWORD` | `admin` | Grafana admin password (change this!) |
| `GF_PORT` | `3100` | Host port for Grafana |

## Connecting alert notifications

Grafana supports Alertmanager, PagerDuty, Slack, and many other notification
channels. After the stack is running:

1. Log into Grafana (`http://host:3100`, admin / GF_ADMIN_PASSWORD).
2. Go to **Alerting → Contact points** and add your notification channel.
3. Go to **Alerting → Notification policies** and route alerts to it.

Alternatively, configure Alertmanager separately and point Prometheus at it
by adding an `alerting` block to `docker/observability/prometheus.yml`.

## Reconciliation drift endpoint

An on-demand drift report is also available via the authenticated API:

```
GET /api/v1/admin/drift
Authorization: Bearer <admin JWT>
```

Response:
```json
{
  "orphans_in_keycloak": ["uuid-1", "uuid-2"],
  "orphans_in_db": []
}
```

This calls Keycloak and the DB in real-time, so it reflects current state
rather than the last ticker run. Use it to investigate a drift alert.
