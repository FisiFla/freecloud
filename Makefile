.PHONY: dev-up dev-down db-migrate kc-setup build-backend build-frontend clean verify

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

verify:
	@echo "==> Go vet + test..."
	cd backend && go vet ./... && go test ./...
	@echo "==> Frontend build..."
	cd frontend && npm run build
	@echo "==> All checks passed."
