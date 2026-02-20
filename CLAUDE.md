# CLAUDE.md

Operating guide for Claude Code in this repository.

## Project Context

This project implements the ["Intelligent AI Delegation"](https://arxiv.org/abs/2602.11865) paper (Tomašev, Franklin, Osindero -- Google DeepMind, 2026) as a working escrow-based delegation marketplace on Base (Ethereum L2).

- Active work: `docs/ROADMAP.md` -- the phase marked "(Current)" contains the next incomplete items.
- Architecture and design: `docs/ARCHITECTURE.md`
- Contract specification (state machine, interfaces, invariants): `docs/SPEC_V1.md`
- Implementation status: `docs/ROADMAP.md`
- Setup and deploy commands: `docs/SETUP.md`

## Current Architecture

- **On-chain**: `TaskEscrowFactory` + `TaskEscrow` (Solidity 0.8.34, Foundry)
- **Off-chain**: Single Go binary -- MCP server + HTTP API + event indexer
- **Storage**: SQLite via `modernc.org/sqlite` (pure Go, no CGO)
- **Agent interface**: MCP tools are the primary integration surface
- **Target chain**: Base Sepolia (chain ID 84532)

## Constraints

- Keep Solidity pinned to `0.8.34` unless explicitly requested.
- Keep V1 behavior aligned with `docs/SPEC_V1.md` (state machine and role semantics).
- Do not introduce destructive git operations (`reset --hard`, force clean, etc.) unless explicitly requested.
- Do not remove or weaken tests to make CI pass.
- Use real implementations over mocks whenever possible in tests.
- Prioritize good design over simply making tests pass.

## Build and Test

```bash
make build          # forge build
make test           # forge test -vv
make test-unit      # forge test for TaskEscrow*.t.sol only (faster)
make test-invariant # invariant tests
make go-abi         # copy ABI artifacts from Foundry output to go-server/abi/
make go-build       # compile Go binary (runs go-abi first)
make go-test        # go test ./...
make go-vet         # go vet ./...
make go-run         # build and run the server locally
make fmt            # forge fmt + gofmt -w (both Solidity and Go)
make fmt-check      # lint formatting without writing (used in CI)
make sizes          # show contract sizes (check proximity to 24KB limit)
make test-all       # fmt-check + all Solidity + Go vet + Go tests
```

Always run `make test-all` before finishing changes. If tests fail, fix the root cause, rerun, and report what changed.

## Solidity Standards

- Custom errors over string reverts.
- Checks-effects-interactions ordering.
- `nonReentrant` where ETH transfers occur in state-changing flows.
- Emit events for all critical state transitions and administrative actions.
- Explicit role checks and strict state guards.

## Go Standards

- `gofmt` and standard Go conventions.
- No ORM -- `database/sql` with hand-written queries.
- ABI files embedded via `//go:embed` from `go-server/abi/`.
- Run `make go-abi` after any contract changes before building Go.
- MCP tool handlers and HTTP handlers are thin wrappers around shared logic.
- Return errors, don't panic (except in `init()` for ABI parsing).
- `context.Context` for all chain and DB operations.
- Logging: use `log/slog` (stdlib structured logger). Never use `log.Printf` or `fmt.Printf` for operational logging. Use JSON handler with leveled output (`slog.Info`, `slog.Warn`, `slog.Error`). Include relevant context as key-value pairs.
- Chain operations: depend on `chain.ChainClient` interface, not `*chain.Client` directly. Use `chain.MockClient` in tests.

## Key Directories

```text
src/                      Solidity contracts
test/                     Foundry tests
script/                   Deploy scripts
go-server/
  cmd/server/main.go      Entrypoint
  internal/
    chain/                 go-ethereum client, ABI bindings
    storage/               SQLite schema, queries, models
    indexer/                Event polling -> DB reconciliation
    mcpserver/             MCP server + 8 tool handlers
    api/                   HTTP JSON API + middleware
  abi/                     Embedded ABI artifacts (copied by make go-abi)
docs/                     Architecture, spec, roadmap, setup
```

## Deployment

```bash
export PRIVATE_KEY=0x...
export TREASURY=0x...
export OWNER=0x...
export PROTOCOL_FEE_BPS=100
export BASE_SEPOLIA_RPC_URL=https://sepolia.base.org
make deploy-base-sepolia
```

Verify contract source on block explorer after deployment. Record deployed addresses and tx hashes in docs.

## ERC20 Token Support

- `token == address(0)` (or `""` / `"0x0000000000000000000000000000000000000000"` in Go) means ETH-denominated escrow; any other address is ERC20.
- ERC20 funding flow: `ApproveERC20` → `WaitMined` (check `receipt.Status == 1`) → `Fund(ctx, addr, nil)`.
- ETH funding flow: `Fund(ctx, addr, amount)` with non-nil amount.
- Comprehensive ERC20 tests live in `test/TaskEscrowERC20.t.sol`.
- Contracts use `Params` structs (e.g., `CreateEscrowParams`) to reduce constructor argument count -- extend these structs rather than adding bare parameters.
