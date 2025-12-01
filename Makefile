.PHONY: build test test-race test-cover lint audit run clean docker help

# Build variables
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE    := $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
BINARY  := rafale

# Linker flags for version injection
LDFLAGS := -ldflags "-s -w \
	-X github.com/0xredeth/Rafale/internal/version.Version=$(VERSION) \
	-X github.com/0xredeth/Rafale/internal/version.Commit=$(COMMIT) \
	-X github.com/0xredeth/Rafale/internal/version.Date=$(DATE)"

## build: Build production binary with version info
build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/rafale

## test: Run tests
test:
	go test -v ./...

## test-race: Run tests with race detector
test-race:
	go test -v -race ./...

## test-cover: Run tests with coverage report
test-cover:
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

## lint: Run golangci-lint
lint:
	golangci-lint run

## audit: Run security and quality checks
audit:
	go mod verify
	go vet ./...
	golangci-lint run
	govulncheck ./...

## run: Run the indexer
run: build
	./$(BINARY) start

## clean: Remove build artifacts
clean:
	rm -f $(BINARY) coverage.out coverage.html

## docker: Build Docker image
docker:
	docker build -t rafale:$(VERSION) -t rafale:latest .

## docker-up: Start with docker-compose
docker-up:
	docker-compose up -d

## docker-down: Stop docker-compose
docker-down:
	docker-compose down

## help: Show this help
help:
	@echo "Rafale - Linea zkEVM Event Indexer"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'

.DEFAULT_GOAL := help
