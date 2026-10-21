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

`docker/observability/grafana-provisioning/dashboards/freecloud.json` — the
single, provisioned dashboard. It is mounted into Grafana through
`docker/observability/grafana-provisioning/` and picked up by the file
provider (`grafana-provisioning/dashboards/provider.yml`), so it is the
dashboard that actually runs. There is no second copy elsewhere in the repo.

Panels:

- **Request rate** — per-route req/s over the last 2 minutes
- **5xx error rate** — percentage of 5xx responses (thresholds: green/yellow/red)
- **P50 / P95 / P99 latency** — histogram quantiles over 2-minute windows
- **Backend / Keycloak / Fleet / Readyz health** — stat panels with color coding
- **Reconciliation drift gauges** — orphan counts in both directions
- **Last reconciliation run** — time since the last successful job run
- **Leader election** — timeseries of `freecloud_leader_election_is_leader` by
  `job` / `instance`

The health panels use `increase(http_requests_total{...}[5m])`, not the raw
counter: a counter never returns to zero, so comparing it to zero would keep
the panel green forever. With `increase()` the panel shows the number of
successful calls in the last 5 minutes and turns red when that drops to 0.

## Alert rules

`docker/observability/alerts.yml` defines the following alerts:

| Alert | Severity | Condition |
|---|---|---|
| `BackendDown` | critical | No scrape from the backend for > 1 min |
| `ReadyzDown` | critical | Blackbox probe of `/readyz` failing for > 2 min |
| `HighErrorRate` | warning | 5xx rate > 5% for 5 min |
| `CriticalErrorRate` | critical | 5xx rate > 25% for 2 min |
| `HighP99Latency` | warning | P99 latency > 2s for 5 min on any route |
| `ReconciliationDriftKeycloak` | warning | Keycloak orphans > 0 for > 30 min |
| `ReconciliationDriftDB` | warning | DB orphans > 0 for > 30 min |
| `ReconciliationJobStale` | warning | Last run timestamp > 1 hour ago (or the metric is missing) |
| `LeaderlessBackgroundJob` | warning | `sum by (job) (freecloud_leader_election_is_leader) == 0` for 15m (or the metric is missing) |
| `ReviewScheduleRunnerStalled` | warning | No `leader_ran` tick in 15 min for 30 min (or the counter is missing) |
| `ReviewScheduleRunnerSkipping` | warning | Only `skipped_not_leader` ticks for > 1 h |

Rules that depend on a metric the backend may not have emitted yet are paired
with `absent(...)`, so "the job never ran" alerts instead of silently
producing an empty vector.

## Running the observability stack

The observability services are in a separate Compose override so they are
opt-in (not bundled in the production stack by default).

```bash
# Overlay on top of the production stack. The secrets have to be in the
# shell environment: the production compose resolves the database DSN at
# parse time and fails fast without it.
set -a && . .secrets/secrets.env && set +a
docker compose \
  -f docker/docker-compose.prod.yml \
  -f docker/docker-compose.observability.yml \
  --env-file .env.prod \
  up -d

# Grafana is on port 3100 (override with GF_PORT in .env.prod)
```

The overlay adds three services:

- **prometheus** — scrapes `backend:8080/metrics` and the `/readyz` probe job
- **blackbox-exporter** — actively probes `/readyz` (the `http_2xx` module in
  `docker/observability/blackbox-exporter.yml`); it is what produces the
  `probe_success` series the `ReadyzDown` alert evaluates
- **grafana** — dashboard and alert rules

All three join the same default Compose network as the backend, so Prometheus
can reach `backend:8080` without exposing `/metrics` publicly.

Grafana datasource (Prometheus at `http://prometheus:9090`) and the FreeCloud
dashboard are provisioned automatically from
`docker/observability/grafana-provisioning/`.

## Environment variables (add to .env.prod)

| Variable | Default | Description |
|---|---|---|
| `GF_ADMIN_PASSWORD` | *(none — required)* | Grafana admin password. The compose file fails to start Grafana when it is unset, so there is no usable default; set a strong one. |
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
