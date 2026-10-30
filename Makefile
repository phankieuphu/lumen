SHELL := /bin/bash
MODULE := github.com/patrick-rontech/kiemdb
GOBIN  ?= $(shell go env GOPATH)/bin

export KIEMDB_TEST_POSTGRES_DSN ?= postgres://kiemdb:kiemdb@localhost:5432/kiemdb?sslmode=disable
export KIEMDB_TEST_MYSQL_DSN    ?= kiemdb:kiemdb@tcp(localhost:3306)/kiemdb

.PHONY: help build run test test-int bench cover lint fmt fmt-check vuln check tools db-up db-down

help: ## Show targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-10s %s\n",$$1,$$2}'

build: ## Build all packages and the binary
	go build -trimpath -o bin/kiemdb ./cmd/kiemdb

run: build ## Run the app
	./bin/kiemdb

test: ## Unit tests with the race detector
	go test -race -count=1 ./...

test-int: ## Integration tests (needs `make db-up`)
	go test -race -count=1 -tags=integration ./...

bench: ## Benchmarks
	go test -run=^$$ -bench=. -benchmem ./...

cover: ## Coverage report
	go test -race -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1

lint: ## golangci-lint
	$(GOBIN)/golangci-lint run ./...

fmt: ## Format code
	$(GOBIN)/gofumpt -l -w .
	$(GOBIN)/goimports -local $(MODULE) -w .

fmt-check: ## Fail if code is not formatted
	@out=$$($(GOBIN)/gofumpt -l .); if [ -n "$$out" ]; then echo "needs gofumpt:"; echo "$$out"; exit 1; fi

vuln: ## Scan dependencies for known vulnerabilities
	$(GOBIN)/govulncheck ./...

check: fmt-check lint test vuln ## Everything CI runs except integration

tools: ## Install dev tools
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	go install mvdan.cc/gofumpt@latest
	go install golang.org/x/tools/cmd/goimports@latest
	go install golang.org/x/vuln/cmd/govulncheck@latest

db-up: ## Start PostgreSQL and MySQL
	docker compose -f deploy/compose.yaml up -d --wait

db-down: ## Stop databases and remove volumes
	docker compose -f deploy/compose.yaml down -v
