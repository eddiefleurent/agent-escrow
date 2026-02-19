# Contributing

## Setup

1. Install [Foundry](https://book.getfoundry.sh/getting-started/installation) and Go 1.26+
2. Clone the repo
3. Build and test:
   ```bash
   make all       # build Solidity + Go
   make test-all  # run all tests
   ```

See [`docs/SETUP.md`](docs/SETUP.md) for detailed environment setup.

## Code Style

**Solidity**: Compiler `0.8.34`. Custom errors (not string reverts), `nonReentrant` on ETH transfers, checks-effects-interactions ordering, events for all state transitions.

**Go**: `gofmt` formatting (`make go-fmt`). `database/sql` directly (no ORM). Return errors, don't panic. `context.Context` for chain and DB operations.

## Testing

All changes must pass the full suite:

```bash
make test-all
```

This runs Foundry unit tests, invariant tests, and Go tests.

## Pull Requests

1. Create a feature branch
2. Make changes
3. Run `make test-all` -- all tests must pass
4. Run `make fmt && make go-fmt` for formatting
5. Submit PR with a clear description of what changed and why

## Architecture Notes

- Contract changes require updating tests and `docs/SPEC_V1.md`
- Contract ABI changes require rebuilding Go: `make go-abi && make go-build`
- New MCP tools should have matching HTTP API endpoints
- See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for system design
