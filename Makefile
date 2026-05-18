.PHONY: dev build run test vet tidy fmt docker docker-run db-up db-down db-shell clean

# ─── Go ──────────────────────────────────────────────────────────

dev:
	go run ./cmd/server

build:
	mkdir -p bin
	go build -ldflags="-s -w" -o bin/winton-tv ./cmd/server

run: build
	./bin/winton-tv

test:
	go test -race -v ./...

vet:
	go vet ./...

tidy:
	go mod tidy

fmt:
	gofmt -s -w .

# ─── Docker (full stack: app + postgres) ────────────────────────

docker:
	docker build -t winton-tv:dev .

docker-up:
	docker compose up --build

docker-down:
	docker compose down

# ─── DB (postgres only — for `make dev` against host-Go) ────────

db-up:
	docker compose up -d postgres

db-down:
	docker compose stop postgres

db-shell:
	docker compose exec postgres psql -U winton -d winton

db-reset:
	docker compose down -v postgres
	docker compose up -d postgres

clean:
	rm -rf bin/
