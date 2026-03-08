# Demo Scripts

Live demo scripts exercising the full escrow lifecycle on Base Sepolia. Each script creates real on-chain transactions against the deployed factory.

## Scripts

| Script | Description |
|---|---|
| `eth_demos.sh` | Seven V2 scenarios (C–I) using ETH: worker stake, milestones, disputes, backup agents, bidding, reputation, emergency response |
| `usdc_demos.sh` | Same seven scenarios using USDC (ERC20), proving full token parity |
| `ap2_demo.py` | AP2 mandate bridge demo — gasless EIP-3009 `receiveWithAuthorization` funding via the AP2 mandate-to-escrow bridge |
| `preflight_parity.sh` | Non-transactional parity readiness checks (env, tools, addresses, balances, API health) |
| `check_no_secrets.sh` | Guardrail scan for explicit private-key leaks in demo/docs markdown/json |
| `parity_results.template.json` | Canonical results schema for HTTP/CLI/MCP/UCP parity capture |

## Supporting Assets

| Path | Description |
|---|---|
| `agents/` | Agent-specific Codex prompt files and orchestration helpers for experimental multi-agent demos |
| `demo-roles.md` | Role-separated operator runbook for buyer/worker/verifier/arbitrator sessions |
| `runtime/` | Ignored local runtime state produced by agent-demo scripts |

The publicly documented, current live demos are the ETH, USDC, and AP2 flows above. Role-separated agent demos remain staging material until SH5 client-side signing lands.

## Prerequisites

1. **Go server running** on port 8080 with a fresh database:
   ```bash
   lsof -ti :8080 | xargs kill -9 2>/dev/null
   rm -f go-server/delegation.db*
   set -a && source .env && set +a
   # Run server in a separate terminal (recommended), or background it:
   (cd go-server && ./bin/server)
   # Alternative: (cd go-server && ./bin/server > /tmp/agent-escrow-server.log 2>&1 &)
   ```
   Keep your demo terminal at the repo root, then wait ~30s for the indexer to sync before running demos.

2. **Environment variables** sourced from `.env` (see `.env.example`):
   - `PRIVATE_KEY` — buyer/owner key (server signs with this)
   - `WORKER_KEY`, `VERIFIER_KEY`, `ARBITRATOR_KEY`, `BACKUP_KEY` — required for `cast send` in demos
   - `BACKUP_WORKER_KEY` is still accepted as a legacy alias for `BACKUP_KEY`
   - `FACTORY_ADDRESS` — deployed factory on Base Sepolia

3. **Tooling installed**: `cast` (Foundry), `curl`, `jq`, `python3`, `uv`

4. **For USDC demos**: participants need USDC balances on Base Sepolia. Use the faucet:
   ```bash
   uv run scripts/faucet.py --token usdc --claims 10
   ```

5. **For AP2 demo**: additional Python dependencies:
   ```bash
   uv pip install eth-account requests python-dotenv
   ```

6. **Recommended before any public demo run**:
   ```bash
   # ETH readiness
   set -a && source .env && set +a
   bash demo/preflight_parity.sh

   # Include USDC readiness checks for USDC/AP2/UCP runs
   bash demo/preflight_parity.sh --require-usdc

   # Secret guardrail scan before committing docs/log output
   bash demo/check_no_secrets.sh
   ```

## Running

Each demo script should be run from the **repo root** with `.env` sourced:

```bash
# ETH demos (all 7 scenarios)
set -a && source .env && set +a
bash demo/eth_demos.sh

# USDC demos (all 7 scenarios)
# Restart server fresh first (new DB)
bash demo/usdc_demos.sh

# AP2 mandate bridge demo
# Restart server fresh first (new DB)
uv run demo/ap2_demo.py
```

Results are saved to `/tmp/v2_demo_results.json` (ETH), `/tmp/v2_usdc_demo_results.json` (USDC), and a secure temp file for AP2 (or `AP2_RESULTS_FILE`/`DEMO_OUTPUT_PATH`/`OUTPUT_PATH` if set).

For parity runs, copy the canonical schema first:

```bash
cp demo/parity_results.template.json /tmp/parity_results.json
```

The legacy two-agent Codex orchestrator writes local coordination files under `demo/runtime/agent-state/`.

## Results Documentation

Full transaction logs, settlement math, and sequence diagrams for all demos: [`DEMO_RUN.md`](DEMO_RUN.md)
