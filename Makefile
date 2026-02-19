.PHONY: build test test-unit test-invariant fmt clean deploy-base-sepolia go-abi go-build go-test go-run go-fmt all test-all

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
	cd go-server && go build -o bin/server ./cmd/server/

go-test: go-abi
	cd go-server && go test ./...

go-run: go-build
	cd go-server && ./bin/server

go-fmt:
	cd go-server && gofmt -w .

# Combined targets
all: build go-build

test-all: test test-invariant go-test
