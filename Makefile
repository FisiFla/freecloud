.PHONY: dev-up dev-down db-migrate kc-setup build-backend build-frontend clean verify verify-db verify-all test-db

dev-up:
	docker compose -f docker/docker-compose.yml up -d
	@echo "Waiting for services..."
	@sleep 5
	@echo "Running migrations..."
	cd backend && go run cmd/server/migrate.go

dev-down:
	docker compose -f docker/docker-compose.yml down

db-migrate:
	cd backend && go run cmd/server/migrate.go

kc-setup:
	@chmod +x backend/cmd/scripts/setup_realm.sh
	@bash backend/cmd/scripts/setup_realm.sh

test-integration:
	cd backend && go test ./internal/handlers/ -v -run Integration

build-backend:
	cd backend && go build -o ../bin/freecloud-server ./cmd/server

build-frontend:
	cd frontend && npm install && npm run build

clean:
	rm -rf bin/
	docker compose -f docker/docker-compose.yml down -v

# Fast no-live gate: vet, unit tests, frontend type-check + build.
verify:
	@echo "==> Go vet + test..."
	cd backend && go vet ./... && go test ./...
	@echo "==> Frontend type-check + build..."
	cd frontend && npm install --no-audit --no-fund --include=dev && npm run verify
	@echo "==> All checks passed."

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
			go test -tags=integration -race ./internal/db/ -v; \
		ret=$$?; \
		docker stop freecloud-test-pg >/dev/null 2>&1 || true; \
		exit $$ret; \
	else \
		echo "Using TEST_DATABASE_URL from environment."; \
		cd backend && TEST_DATABASE_URL="$$TEST_DATABASE_URL" \
			go test -tags=integration -race ./internal/db/ -v; \
	fi

# DB gate: fast verify + the DB integration tests.
verify-db: verify test-db
	@echo "==> DB-backed checks passed."

# Everything: fast verify + DB integration tests + race across all packages.
verify-all: verify-db
	@echo "==> Go race tests (all packages)..."
	cd backend && go test -race ./...
	@echo "==> All checks (fast + DB + race) passed."
