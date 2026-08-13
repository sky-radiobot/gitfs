.PHONY: all build test unit-tests integration-tests vet sync-submodules clean help

# Make sure submodules (simple-go) are checked out before anything else,
# so a fresh clone without --recurse-submodules still builds.
$(shell git submodule update --init > /dev/null 2>&1)

all: build

build: sync-submodules ## Build all packages
	go build ./...

sync-submodules: ## Init/update git submodules and push back any local changes made to them
	./scripts/sync-submodules

unit-tests: ## Run Go unit tests
	go test ./...

integration-tests: ## Run bash integration tests (builds bin/gitfs first)
	$(MAKE) -C tests/integration

test: unit-tests integration-tests ## Run all tests

vet: ## Run go vet
	go vet ./...

clean: ## Remove build artifacts
	rm -rf bin

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-16s %s\n", $$1, $$2}'
