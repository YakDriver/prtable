GO ?= go

default: help

build: ## [CI] Build / go build
	@echo "make: Building..."
	@$(GO) build -o prtable .

ci: build fmt-check fix-check tidy-check vet test ## Run every check CI runs

fix: ## Apply go fix
	@echo "make: Applying go fix..."
	@$(GO) fix ./...

fix-check: ## [CI] Go Fix Check / go fix
	@echo "make: Checking go fix..."
	@$(GO) fix -diff ./... || { echo "make: To fix, run: make fix"; exit 1; }

fmt: ## Apply gofmt
	@echo "make: Applying gofmt..."
	@gofmt -w .

fmt-check: ## [CI] Format Check / gofmt
	@echo "make: Checking gofmt..."
	@files=$$(gofmt -l .) || exit $$?; \
	if [ -n "$$files" ]; then \
		echo "make: Files need gofmt:"; echo "$$files"; \
		echo "make: To fix, run: make fmt"; exit 1; \
	fi

help: ## Display this help
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-12s\033[0m %s\n", $$1, $$2}'

test: ## [CI] Test / go test
	@echo "make: Running tests..."
	@$(GO) test -count=1 ./...

tidy: ## Apply go mod tidy
	@echo "make: Applying go mod tidy..."
	@$(GO) mod tidy

tidy-check: ## [CI] Tidy Check / go mod tidy
	@echo "make: Checking go mod tidy..."
	@$(GO) mod tidy -diff || { echo "make: To fix, run: make tidy"; exit 1; }

vet: ## [CI] Vet / go vet
	@echo "make: Running go vet..."
	@$(GO) vet ./...

.PHONY: build ci default fix fix-check fmt fmt-check help test tidy tidy-check vet
