.PHONY: build test test-unit test-invariant fmt fmt-check sizes snapshot clean deploy-base-sepolia go-abi go-build go-test go-vet go-lint go-lint-fix go-run go-fmt all test-all

GO_CACHE_DIR := $(CURDIR)/.cache/go-build
GOLANGCI_LINT_CACHE_DIR := $(CURDIR)/.cache/golangci-lint
GOLANGCI_LINT_VERSION ?= v2.5.0
GOLANGCI_LINT := GOCACHE=$(GO_CACHE_DIR) GOLANGCI_LINT_CACHE=$(GOLANGCI_LINT_CACHE_DIR) go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

# Solidity targets
build:
	forge build

test:
	forge test -vv

test-unit:
	forge test --match-path "test/TaskEscrow*.t.sol" -vv

test-invariant:
	forge test --match-contract TaskEscrowInvariantsTest -vv

fmt:
	forge fmt
	cd go-server && gofmt -w .

fmt-check:
	forge fmt --check
	@test -z "$$(cd go-server && gofmt -l .)" || (cd go-server && gofmt -l . && exit 1)

sizes:
	forge build --sizes

snapshot:
	forge snapshot

clean:
	forge clean

deploy-base-sepolia:
	forge script script/DeployFactory.s.sol:DeployFactory \
		--rpc-url $$BASE_SEPOLIA_RPC_URL \
		--broadcast \
		-vv

# Go targets
go-abi:
	mkdir -p go-server/abi
	cp out/TaskEscrowFactory.sol/TaskEscrowFactory.json go-server/abi/
	cp out/TaskEscrow.sol/TaskEscrow.json go-server/abi/

go-build: go-abi
	cd go-server && GOCACHE=$(GO_CACHE_DIR) go build -o bin/server ./cmd/server/

go-test: go-abi
	cd go-server && GOCACHE=$(GO_CACHE_DIR) go test ./...

go-vet: go-abi
	cd go-server && GOCACHE=$(GO_CACHE_DIR) go vet ./...

go-run: go-build
	cd go-server && ./bin/server

go-lint:
	cd go-server && $(GOLANGCI_LINT) run ./...

go-lint-fix:
	cd go-server && $(GOLANGCI_LINT) run --fix ./...

go-fmt:
	cd go-server && gofmt -w .

# Combined targets
all: build go-build

test-all: fmt-check test test-invariant go-vet go-lint go-test
