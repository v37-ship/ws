.PHONY: all run build test vet fmt clean ci docker-up docker-down docker-logs docker-build docker-deploy smoke

all: build

run:
	go run ./cmd/server

build:
	go build -o bin/server ./cmd/server

test:
	go test -race -count=1 ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

clean:
	rm -rf bin/ server

ci:
	./scripts/ci-local.sh

docker-build:
	docker build -t spa-booking:local .

docker-up:
	docker compose up --build -d

docker-deploy:
	./scripts/deploy-local.sh

smoke:
	curl -fsS http://localhost:8088/healthz && curl -fsS http://localhost:8088/readyz

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f app
