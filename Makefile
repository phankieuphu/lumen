SHELL := /bin/bash
MODULE := github.com/phankieuphu/lumen
GOBIN  ?= $(shell go env GOPATH)/bin

export LUMEN_TEST_POSTGRES_DSN ?= postgres://lumen:lumen@localhost:5432/lumen?sslmode=disable
export LUMEN_TEST_MYSQL_DSN    ?= lumen:lumen@tcp(localhost:3306)/lumen

.PHONY: help build run test test-int bench cover lint fmt fmt-check vuln check tools db-up db-down ui-dev ui-check

help: ## Show targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-10s %s\n",$$1,$$2}'

build: ## Build all packages and the binary
	go build -trimpath -o bin/lumen ./cmd/lumen

run: build ## Run the app
	./bin/lumen

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

UI := ui/frontend

$(UI)/node_modules: $(UI)/package-lock.json
	cd $(UI) && npm ci
	@touch $@

ui-dev: $(UI)/node_modules ## Frontend dev server on a mock backend (no Go needed)
	cd $(UI) && npm run dev:mock

ui-check: $(UI)/node_modules ## Frontend typecheck, unit tests, and production build
	cd $(UI) && npm run check

tools: ## Install dev tools
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	go install mvdan.cc/gofumpt@latest
	go install golang.org/x/tools/cmd/goimports@latest
	go install golang.org/x/vuln/cmd/govulncheck@latest

db-up: ## Start PostgreSQL and MySQL
	docker compose -f deploy/compose.yaml up -d --wait

db-down: ## Stop databases and remove volumes
	docker compose -f deploy/compose.yaml down -v
