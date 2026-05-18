.PHONY: dev build run test vet tidy fmt docker docker-run clean

# Local dev — runs the server with `go run`, hot reload not included
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

docker:
	docker build -t winton-tv:dev .

docker-run: docker
	docker run --rm -p 8080:8080 winton-tv:dev

clean:
	rm -rf bin/
