.PHONY: build test lint run docker-build

BINARY := alto
CMD := ./cmd/alto

build:
	go build -o $(BINARY) $(CMD)

test:
	go test ./...

lint:
	golangci-lint run

run:
	go run $(CMD)

docker-build:
	docker build -t alto:latest .
