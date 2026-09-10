.PHONY: all run build test clean docker-up docker-down docker-logs migrate

all: build

run:
	go run ./cmd/server

build:
	go build -o bin/server ./cmd/server

test:
	go test -v ./...

clean:
	rm -rf bin/ server

docker-up:
	docker compose up --build -d

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f app
