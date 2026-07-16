.PHONY: dev-up dev-down db-migrate kc-build build-backend build-frontend clean verify verify-secrets verify-db verify-all test-db prod-build prod-up prod-down localhost-up localhost-down prod-secrets-init verify-provisioning-live

dev-up:
	docker compose -f docker/docker-compose.yml up -d
	@echo "Waiting for services..."
	@sleep 5
	@echo "Running migrations..."
	cd backend && go run ./cmd/server migrate

dev-down:
	docker compose -f docker/docker-compose.yml down

db-migrate:
	cd backend && go run ./cmd/server migrate

# A2 (FCEX3-6): Build the custom Keycloak image with the posture-check SPI.
# Requires Docker. Runs the multi-stage docker/Dockerfile.keycloak build.
# Only needed when keycloak-authenticator/ changes — not on every Go/TS change.
kc-build:
	docker compose -f docker/docker-compose.yml build keycloak

test-integration:
	cd backend && go test ./internal/handlers/ -v -run Integration

# A4 — OPTIONAL live verification of outbound provisioning connectors against
# a real GitHub org / Slack workspace. Skips each target entirely when its
# credentials are absent (safe to run unattended). See
# backend/cmd/verify-provisioning/main.go for the required env vars
# (GITHUB_SCIM_TOKEN + GITHUB_SCIM_ORG + GITHUB_SCIM_TEST_USERNAME;
# SLACK_SCIM_TOKEN + SLACK_SCIM_TEST_EMAIL — Slack stays parked, needs a paid
# plan with SCIM enabled).
verify-provisioning-live:
	cd backend && go run ./cmd/verify-provisioning

build-backend:
	cd backend && go build -o ../bin/freecloud-server ./cmd/server

build-frontend:
	cd frontend && npm install && npm run build

# Production stack (docker/docker-compose.prod.yml). Requires a populated
# .env.prod (copy from .env.prod.example). The backend serves on the internal
# network; Caddy terminates TLS for the public hostnames.
prod-build:
	docker compose -f docker/docker-compose.prod.yml --env-file .env.prod build

# C3: prod-up generates secrets (if not yet present) then sources them into the
# shell so compose can interpolate POSTGRES_PASSWORD / KC_ADMIN_PASSWORD into
# URL strings in docker-compose.prod.yml.
prod-up: prod-secrets-init
	@set -a && . .secrets/secrets.env && set +a && \
		docker compose -f docker/docker-compose.prod.yml --env-file .env.prod up -d --build

prod-down:
	docker compose -f docker/docker-compose.prod.yml --env-file .env.prod down

# C1/C2 — All-in-one localhost stack (no TLS, no required env).
# Generates secrets on first boot, then starts all services.
# Open http://localhost:3000 when up.
localhost-up:
	@mkdir -p .secrets
	docker compose -f docker/docker-compose.localhost.yml up --build -d

localhost-down:
	docker compose -f docker/docker-compose.localhost.yml down

# C1/C3 — Generate production secrets (first-boot only, idempotent).
# Run before `make prod-up` if you want to generate secrets ahead of time.
prod-secrets-init:
	@mkdir -p .secrets
	docker run --rm \
		-v "$$(pwd)/.secrets:/run/freecloud" \
		-v "$$(pwd)/docker/init-secrets/generate.sh:/generate.sh:ro" \
		busybox:1.36 /bin/sh /generate.sh

clean:
	rm -rf bin/
	docker compose -f docker/docker-compose.yml down -v

# Fast no-live gate: vet, unit tests, frontend type-check + build.
verify: verify-secrets
	@echo "==> Go vet + test..."
	cd backend && go vet ./... && go test ./...
	@echo "==> Frontend type-check + build..."
	cd frontend && npm install --no-audit --no-fund --include=dev && npm run verify
	@echo "==> All checks passed."

verify-secrets:
	@echo "==> Production secret generator..."
	@tmp="$$(mktemp -d)"; \
		trap 'rm -rf "$$tmp"' EXIT; \
		SECRETS_FILE="$$tmp/secrets.env" sh docker/init-secrets/generate.sh >/dev/null; \
		for key in POSTGRES_PASSWORD KC_ADMIN_PASSWORD AUTH_SECRET SCIM_BEARER_TOKEN ACCESS_EVAL_TOKEN FLEET_WEBHOOK_SECRET PROVISIONING_MASTER_KEY; do \
			if ! grep -q "^$$key=" "$$tmp/secrets.env"; then \
				echo "missing $$key in generated secrets.env"; \
				exit 1; \
			fi; \
		done

# DB-backed integration tests against TEST_DATABASE_URL (or a spun-up Postgres).
# These are NOT part of the fast `verify` gate.
# NOTE: every shell variable reference in a Make recipe must use $$ (Make
# consumes a single $ as its own variable syntax). Omitting the second $
# silently rewrites the variable name and causes the test suite to skip.
test-db:
	@if [ -z "$$TEST_DATABASE_URL" ]; then \
		echo "TEST_DATABASE_URL not set. Checking for Docker..."; \
		if ! command -v docker >/dev/null 2>&1; then \
			echo ""; \
			echo "ERROR: Docker is not installed and TEST_DATABASE_URL is not set."; \
			echo "       Either install Docker, or run against an existing Postgres:"; \
			echo "         TEST_DATABASE_URL=postgres://user:pass@host:5432/dbname?sslmode=disable make test-db"; \
			echo ""; \
			exit 1; \
		fi; \
		echo "Starting an ephemeral Postgres 16 via docker..."; \
		if ! docker run --rm -d --name freecloud-test-pg \
			-e POSTGRES_USER=freecloud \
			-e POSTGRES_PASSWORD=freecloud \
			-e POSTGRES_DB=freecloud_test \
			-p 55432:5432 \
			postgres:16-alpine >/dev/null; then \
			echo "ERROR: failed to start the Postgres container. Is the Docker daemon running?"; \
			exit 1; \
		fi; \
		if ! command -v pg_isready >/dev/null 2>&1; then \
			echo "pg_isready not found; falling back to a fixed 5s wait for Postgres..."; \
			sleep 5; \
		else \
			echo "Waiting for Postgres to accept connections..."; \
			for i in 1 2 3 4 5 6 7 8 9 10; do \
				pg_isready -h localhost -p 55432 -U freecloud >/dev/null 2>&1 && break; \
				sleep 1; \
			done; \
		fi; \
		trap 'docker stop freecloud-test-pg >/dev/null 2>&1 || true' EXIT; \
		cd backend && TEST_DATABASE_URL="postgres://freecloud:freecloud@localhost:55432/freecloud_test?sslmode=disable" \
			go test -tags=integration -race -p 1 ./internal/db/ ./internal/handlers/ -v; \
		ret=$$?; \
		docker stop freecloud-test-pg >/dev/null 2>&1 || true; \
		exit $$ret; \
	else \
		echo "Using TEST_DATABASE_URL from environment."; \
		cd backend && TEST_DATABASE_URL="$$TEST_DATABASE_URL" \
			go test -tags=integration -race -p 1 ./internal/db/ ./internal/handlers/ -v; \
	fi

# DB gate: fast verify + the DB integration tests.
verify-db: verify test-db
	@echo "==> DB-backed checks passed."

# Everything: fast verify + DB integration tests + race across all packages.
verify-all: verify-db
	@echo "==> Go race tests (all packages)..."
	cd backend && go test -race ./...
	@echo "==> All checks (fast + DB + race) passed."
