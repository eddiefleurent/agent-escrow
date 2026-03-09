# AGENTS.md

Repository operating guide for human and AI contributors.

This file is the repo harness: start here for reading order, invariants, and change-coupling rules. It is not the canonical source for contract behavior, roadmap status, or paper coverage. Follow the linked source-of-truth docs for those.

## Read This First

1. `docs/ROADMAP.md` -- current execution phase and implementation order.
2. `docs/SPEC.md` -- contract state machine, settlement math, invariants, and paper traceability.
3. `docs/ARCHITECTURE.md` -- system boundaries, code map, transport model, and evolution path.
4. `docs/paper-feature-map.json` -- canonical machine-readable paper coverage and status.
5. `docs/SETUP.md` -- local setup, configuration, and deployment commands.

## System In One Screen

- **On-chain:** `TaskEscrowFactory` + `EscrowDeployer` + `TaskEscrow`, Solidity `0.8.34`, Foundry.
- **Current network:** deployed currently on Base Sepolia (`84532`) testnet.
- **Off-chain:** single Go binary combining MCP server, HTTP API, shared services, and event indexer.
- **Storage:** SQLite via `modernc.org/sqlite` and `database/sql`.
- **Interfaces:** MCP tools, HTTP API, and `escrow-cli` for shell agents.
- **Architecture rule:** settle money, roles, deadlines, and terminal decisions on-chain; keep execution, search, orchestration, and large artifacts off-chain.

## Non-Negotiables

- This is a public, open-source system intended to handle real funds on Base. Optimize for security, correctness, and auditability.
- Production target: mainnet Base (handling real funds), even while current deployments run on Base Sepolia testnet.
- Keep Solidity pinned to `0.8.34` unless explicitly requested.
- Keep contract behavior aligned with `docs/SPEC.md`.
- Prefer hard cutoffs over compatibility shims. Do not add legacy branches unless explicitly requested for a specific change.
- Do not remove or weaken tests to make CI pass.
- Prefer real implementations over mocks when tests can exercise the real path safely.
- Never hardcode private keys, mnemonics, API secrets, or faucet credentials in tracked files. Use environment variables and placeholders in `.env.example`.
- Do not use destructive git operations (`reset --hard`, force-clean, etc.) unless explicitly requested.

## Design Invariants

- The smart contracts are the source of financial truth.
- Off-chain storage is a derived read model, not an alternative ledger.
- Every product feature that is meant to be externally usable must be exposed through MCP, HTTP, and CLI. Keep interface parity.
- Transport layers stay thin. Shared logic belongs in common services, not duplicated handlers.
- Contract state changes require corresponding ABI refreshes for Go consumers.
- Changes to settlement states, payout formulas, role semantics, or lifecycle diagrams must update the relevant docs in the same change.

## Work By Surface

### Contracts

- Use custom errors instead of string reverts.
- Follow checks-effects-interactions ordering.
- Use `nonReentrant` where ETH transfers occur in state-changing flows.
- Emit events for critical state transitions and admin actions.
- Enforce explicit role checks and strict state guards.
- After contract changes, run `make go-abi` before Go builds/tests.

### Go Server

- Run `make go-lint-fix` before `make go-lint`.
- Keep `gofmt`-clean code and standard Go conventions.
- Use `database/sql` with hand-written queries. No ORM.
- Embed ABIs from `go-server/abi/` via `//go:embed`.
- MCP handlers, HTTP handlers, and CLI commands should be thin wrappers around shared logic.
- Return errors; do not panic, except for ABI parse failures in `init()`.
- Pass `context.Context` through chain and DB operations.
- Use `log/slog` for operational logging. No `log.Printf` or `fmt.Printf` for runtime logs.
- Depend on `chain.ChainClient`, not concrete chain client types, and use `chain.MockClient` in tests.

### Python Scripts

- Python utilities live under `scripts/`.
- Use `uv`, not `pip install --break-system-packages`.

## Documentation Topology

- `AGENTS.md` -- repo harness, reading order, and working rules.
- `docs/ARCHITECTURE.md` -- stable system shape: boundaries, module ownership, transport model, and evolution path.
- `docs/SPEC.md` -- settlement design intent and invariants. This is the contract-behavior source of truth.
- `docs/ROADMAP.md` -- execution order, status, and scoped future work.
- `docs/paper-feature-map.json` -- canonical machine-readable mapping of paper coverage and status.
- `docs/diagrams/*.puml` -- visual state machines, lifecycle flows, and architecture diagrams.
- `docs/intelligent-ai-delegation.md` -- full paper text, agent-readable.

## Documentation Sync Rules

- When settlement states, settlement formulas, or role semantics change: update `docs/SPEC.md` and the relevant diagrams.
- For system boundary, major component, or integration path changes: update `docs/ARCHITECTURE.md`.
- Changes to implementation sequencing, scope, or status require updates to `docs/ROADMAP.md` and `docs/paper-feature-map.json`.
- When `docs/SPEC.md` or `docs/ARCHITECTURE.md` changes, check whether `docs/diagrams/*.puml` also needs updating.
- Prefer updating existing docs over creating new ones unless explicitly requested.

## Verification

Use the existing `make` targets instead of ad hoc commands:

```bash
make build
make test
make test-unit
make test-invariant
make go-abi
make go-build
make go-cli-build
make go-test
make go-vet
make go-lint-fix
make go-lint
make fmt
make fmt-check
make sizes
make test-all
```

- For code changes, run `make test-all` before finishing.
- For documentation-only changes, rerunning test suites is not required.

## Repo Map

```text
src/                      Solidity contracts
test/                     Foundry tests
script/                   Foundry deployment scripts
scripts/                  Python utilities
demo/                     Demo scripts and run logs
go-server/
  cmd/server/main.go      Server entrypoint
  cmd/cli/main.go         CLI entrypoint
  internal/chain/         Chain bindings and client interfaces
  internal/storage/       SQLite schema and queries
  internal/escrow/        Shared escrow lifecycle orchestration
  internal/indexer/       Event indexing and reconciliation
  internal/bidding/       RFQ + bid lifecycle logic
  internal/attestation/   Completion-attestation-v1 validation
  internal/mcpserver/     MCP server and tool handlers
  internal/api/           HTTP API and middleware
  abi/                    Embedded ABI artifacts copied from Foundry output
skills/                   Agent skills, including `skills/escrow-cli/`
docs/                     Spec, architecture, roadmap, setup, diagrams
```

## Domain Notes

### Worker Stake

- `workerStake == 0` means no worker bond is required.
- If `workerStake > 0`, the worker must call `depositStake()` after funding and before `submit()`.
- Approved work returns the stake in full.
- Disputed stakes follow the same proportional split as payment via `workerAwardBps`.
- Timeout, arbitrator-timeout, or backup-activation paths forfeit the stake to the buyer.

### ERC20 Support

- `address(0)` means ETH escrow; any other token address means ERC20.
- ERC20 funding flow: approve token, wait for mined success, then call `Fund(ctx, addr, nil)`.
- ETH funding flow: call `Fund(ctx, addr, amount)` with a non-nil amount.
- Extend existing `Params` structs instead of adding long positional parameter lists.
