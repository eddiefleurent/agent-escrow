# AGENTS.md

Operating guide for AI coding agents working in this repository.

## Project Context

- Active work: `docs/ROADMAP.md` -- the phase marked "(Current)" contains the next incomplete items.
- Architecture and design: `docs/ARCHITECTURE.md`
- Contract specification (state machine, interfaces, invariants): `docs/SPEC_V1.md`
- Implementation status: `docs/ROADMAP.md`
- Setup and deploy commands: `docs/SETUP.md`
- Paper being implemented: ["Intelligent AI Delegation"](https://arxiv.org/abs/2602.11865) (Tomašev et al., Google DeepMind, 2026)

## Constraints

- Keep Solidity pinned to `0.8.34` unless explicitly requested.
- Keep V1 behavior aligned with `docs/SPEC_V1.md` (state machine and role semantics).
- Do not introduce destructive git operations (`reset --hard`, force clean, etc.) unless explicitly requested.
- Do not remove or weaken tests to make CI pass.

## Solidity Code Standards

- Use custom errors over string reverts.
- Keep checks-effects-interactions ordering.
- Use `nonReentrant` where ETH transfers occur in state-changing flows.
- Emit events for all critical state transitions and administrative actions.
- Prefer explicit role checks and strict state guards.

## Go Code Standards

- Use `gofmt` and standard Go conventions.
- No ORM -- use `database/sql` with hand-written queries.
- ABI files are embedded via `//go:embed` from `go-server/abi/`.
- Run `make go-abi` after any contract changes before building Go.
- Keep MCP tool handlers and HTTP handlers as thin wrappers around shared logic.
- Error handling: return errors, don't panic (except in `init()` for ABI parsing).
- Use `context.Context` for all chain and DB operations.
- Logging: use `log/slog` (stdlib structured logger). Never use `log.Printf` or `fmt.Printf` for operational logging. Use JSON handler with leveled output (`slog.Info`, `slog.Warn`, `slog.Error`). Include relevant context as key-value pairs.
- Chain operations: depend on the `chain.ChainClient` interface, not `*chain.Client` directly. Use `chain.MockClient` in tests.

## Test Requirements

Run before finishing changes:

```bash
make build
make test
make test-invariant
make go-test
```

Or all at once:

```bash
make test-all
```

If tests fail:
1. Fix the root cause.
2. Rerun the full suite.
3. Report what changed and why.

## Deployment Checklist

Before testnet/mainnet deployment:
1. Confirm env vars: `PRIVATE_KEY`, `TREASURY`, `OWNER`, `PROTOCOL_FEE_BPS`, `BASE_SEPOLIA_RPC_URL`.
2. Verify fee bounds and treasury/owner addresses.
3. Deploy with: `make deploy-base-sepolia`
4. Record deployed addresses and tx hashes in docs.
5. Verify contract source on explorer.

## Current Architecture

- On-chain: `TaskEscrowFactory` + `TaskEscrow` (Solidity, Foundry)
- Off-chain: Single Go binary with MCP server + HTTP API + event indexer
- Storage: SQLite (pure Go, no CGO)
- Agent interface: MCP tools are the primary integration surface
