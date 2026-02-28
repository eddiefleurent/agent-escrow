# AGENTS.md

Operating guide for AI coding agents working in this repository.

## Project Context

This project implements the ["Intelligent AI Delegation"](https://arxiv.org/abs/2602.11865) paper (Tomašev, Franklin, Osindero -- Google DeepMind, 2026) as a working escrow-based delegation marketplace on Base (Ethereum L2).

- Active work: `docs/ROADMAP.md` -- the phase marked "(Current)" contains the next incomplete items.
- Contract design intent (state machine, settlement math, invariants, paper traceability): `docs/SPEC.md`
- Architecture and design (high-level context): `docs/ARCHITECTURE.md`
- Visual diagrams (state machine, lifecycle, architecture): `docs/diagrams/*.puml`
- Implementation status: `docs/ROADMAP.md`
- Setup and deploy commands: `docs/SETUP.md`
- Source paper (full text, agent-readable): `docs/intelligent-ai-delegation.md`

## Current Architecture

- **On-chain**: `TaskEscrowFactory` + `TaskEscrow` (Solidity 0.8.34, Foundry)
- **Off-chain**: Single Go binary -- MCP server + HTTP API + event indexer
- **Storage**: SQLite via `modernc.org/sqlite` (pure Go, no CGO)
- **Agent interfaces**: Skills + `escrow-cli` for shell agents, MCP tools (33 tools) for MCP-native agents, plus HTTP API
- **Target chain**: Base Sepolia (chain ID 84532)

## Public Project -- Production Blockchain

This is a **public, open-source project** that will be deployed live on Base (Ethereum L2) handling real funds. It is **not** an internal tool. Treat all code with production-grade rigor: security, correctness, and auditability matter. Do not cut corners on error handling, input validation, or access control.

## Constraints

- Keep Solidity pinned to `0.8.34` unless explicitly requested.
- Keep contract behavior aligned with `docs/SPEC.md` (state machine and role semantics).
- Do not introduce destructive git operations (`reset --hard`, force clean, etc.) unless explicitly requested.
- Do not remove or weaken tests to make CI pass.
- Use real implementations over mocks whenever possible in tests.
- Prioritize good design over simply making tests pass.
- Default to a hard-cutoff approach for behavior and interfaces: avoid backward-compatibility shims, migration branches, or legacy paths unless explicitly requested for a specific change.
- Never hardcode private keys, mnemonics, or secrets in tracked files (including testnet/demo scripts). Always load secrets from `.env`/environment variables and keep only placeholders in `.env.example`.

## Build and Test

```bash
make build          # forge build
make test           # forge test -vv
make test-unit      # forge test for TaskEscrow*.t.sol only (faster)
make test-invariant # invariant tests
make go-abi         # copy ABI artifacts from Foundry output to go-server/abi/
make go-build       # compile Go binary (runs go-abi first)
make go-cli-build   # compile escrow-cli binary (runs go-abi first)
make go-cli-install # install escrow-cli to ~/.local/bin
make go-test        # go test ./...
make go-vet         # go vet ./...
make go-lint        # golangci-lint run ./... (static analysis)
make go-lint-fix    # golangci-lint run --fix ./... (auto-fix what it can)
make go-run         # build and run the server locally
make fmt            # forge fmt + gofmt -w (both Solidity and Go)
make fmt-check      # lint formatting without writing (used in CI)
make sizes          # show contract sizes (check proximity to 24KB limit)
make test-all       # fmt-check + all Solidity + Go vet + Go lint + Go tests
```

Always run `make test-all` before finishing changes. If tests fail, fix the root cause, rerun, and report what changed.

Exception: if a change is strictly documentation-only (for example `*.md` files, diagrams, or other non-code docs), rerunning test suites is not required.

## Solidity Standards

- Custom errors over string reverts.
- Checks-effects-interactions ordering.
- `nonReentrant` where ETH transfers occur in state-changing flows.
- Emit events for all critical state transitions and administrative actions.
- Explicit role checks and strict state guards.

## Go Standards

- **Linting**: `golangci-lint` v2 with config in `go-server/.golangci.yml`. Always run `make go-lint-fix` first (auto-fixes formatting, imports, and simple issues), then `make go-lint` to check for remaining issues. Lint is included in `make test-all`.
- `gofmt` and standard Go conventions.
- No ORM -- `database/sql` with hand-written queries.
- ABI files embedded via `//go:embed` from `go-server/abi/`.
- Run `make go-abi` after any contract changes before building Go.
- MCP tool handlers, HTTP handlers, and CLI commands are thin wrappers around shared logic.
- **Interface parity**: Every feature must be exposed through all three interfaces (MCP tools, HTTP API, and CLI commands). When adding or modifying a feature, update all three. Do not ship a feature that is only accessible via one or two interfaces.
- Return errors, don't panic (except in `init()` for ABI parsing).
- `context.Context` for all chain and DB operations.
- Logging: use `log/slog` (stdlib structured logger). Never use `log.Printf` or `fmt.Printf` for operational logging. Use JSON handler with leveled output (`slog.Info`, `slog.Warn`, `slog.Error`). Include relevant context as key-value pairs.
- Chain operations: depend on `chain.ChainClient` interface, not `*chain.Client` directly. Use `chain.MockClient` in tests.

## Python Scripts

Python scripts live in `scripts/` and use `uv` for environment management. Do not use `pip install --break-system-packages`.

```bash
uv venv .venv                              # create venv (one-time)
uv pip install cdp-sdk python-dotenv       # install deps (one-time)
uv run scripts/faucet.py                   # request testnet ETH from CDP faucet
uv run scripts/faucet.py --token usdc      # request testnet USDC
uv run scripts/faucet.py --claims 10       # batch claims (rate-limited ~10/burst)
```

CDP faucet credentials (`CDP_API_KEY_ID`, `CDP_API_KEY_SECRET`, `CDP_WALLET_SECRET`) must be in `.env`. See `.env.example`.

## Key Directories

```text
src/                      Solidity contracts
test/                     Foundry tests
script/                   Deploy scripts (Foundry/Solidity)
scripts/                  Utility scripts (Python)
demo/                     Demo scripts and results (ETH, USDC, AP2)
go-server/
  cmd/server/main.go      Entrypoint
  cmd/cli/main.go         CLI entrypoint (`escrow-cli`)
  internal/
    chain/                 go-ethereum client, ABI bindings
    storage/               SQLite schema, queries, models
    indexer/                Event polling -> DB reconciliation
    attestation/           Completion-attestation-v1 profile, chain validation (paper §4.8)
    bidding/               Shared bidding protocol logic (RFQ + Bid lifecycle)
    mcpserver/             MCP server + 33 tool handlers
    api/                   HTTP JSON API + middleware
  abi/                     Embedded ABI artifacts (copied by make go-abi)
skills/                   Agent skills (including `skills/escrow-cli/`)
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

## Documentation Maintenance

Three docs are kept in sync with the code:

- **`docs/SPEC.md`** -- contract design intent: state machine, settlement math, invariants, and paper traceability. Does not duplicate Solidity interfaces, events, or off-chain details (those live in the code and ARCHITECTURE.md). Update only when the state machine, settlement formulas, or invariants change.
- **`docs/diagrams/*.puml`** -- PlantUML visual diagrams. Update when contract state transitions, lifecycle flows, or system architecture change. Multiple `@startuml` blocks can live in one file; prefer extending existing files over creating new ones. When editing, match the existing style, formatting conventions, and level of detail of the surrounding diagram. After any `.puml` change, regenerate the corresponding PNGs with `plantuml docs/diagrams/*.puml`.
- **`docs/ARCHITECTURE.md`** -- high-level system design and paper grounding. Update when major structural changes occur (new components, new integration paths). Code-level documentation is handled by DeepWiki; ARCHITECTURE.md covers the "why" and "how things connect."

When updating `docs/SPEC.md` or `docs/ARCHITECTURE.md`, always check whether `docs/diagrams/*.puml` also needs updating. State transitions, settlement flows, and role semantics described in the spec or architecture doc are often visualized in the diagrams -- keep them in sync.

Do not create new documentation files unless explicitly requested. Prefer updating existing docs.

## Worker Stake

- `workerStake` is an optional anti-Sybil bond set at escrow creation (paper §4.8). `0` means no stake required.
- When `workerStake > 0`, the worker must call `depositStake()` after funding and before `submit()`.
- ETH stake: `depositStake{value: workerStake}()`. ERC20 stake: worker approves the token first, then calls `depositStake()` (contract pulls via `transferFrom`).
- If approved, the stake is returned to the worker in full. Disputed stakes follow the same proportional split as payment (`workerAwardBps`). On timeout or arbitrator timeout, the stake is forfeited to the buyer.

## ERC20 Token Support

- `token == address(0)` (or `""` / `"0x0000000000000000000000000000000000000000"` in Go) means ETH-denominated escrow; any other address is ERC20.
- ERC20 funding flow: `ApproveERC20` → `WaitMined` (check `receipt.Status == 1`) → `Fund(ctx, addr, nil)`.
- ETH funding flow: `Fund(ctx, addr, amount)` with non-nil amount.
- Comprehensive ERC20 tests live in `test/TaskEscrowERC20.t.sol`.
- Contracts use `Params` structs (e.g., `CreateEscrowParams`) to reduce constructor argument count -- extend these structs rather than adding bare parameters.
