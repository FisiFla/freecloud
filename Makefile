.PHONY: dev-up dev-down db-migrate build-backend build-frontend clean

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

build-backend:
	cd backend && go build -o ../bin/freecloud-server ./cmd/server

build-frontend:
	cd frontend && npm install && npm run build

clean:
	rm -rf bin/
	docker compose -f docker/docker-compose.yml down -v
