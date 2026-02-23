# Demo Scripts

Live demo scripts exercising the full escrow lifecycle on Base Sepolia. Each script creates real on-chain transactions against the deployed factory.

## Scripts

| Script | Description |
|---|---|
| `eth_demos.sh` | Seven V2 scenarios (C–I) using ETH: worker stake, milestones, disputes, backup agents, bidding, reputation, emergency response |
| `usdc_demos.sh` | Same seven scenarios using USDC (ERC20), proving full token parity |
| `ap2_demo.py` | AP2 mandate bridge demo — gasless EIP-3009 `receiveWithAuthorization` funding via the AP2 mandate-to-escrow bridge |

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

3. **Tooling installed**: `cast` (Foundry), `curl`, `jq`, `python3`

4. **For USDC demos**: participants need USDC balances on Base Sepolia. Use the faucet:
   ```bash
   uv run scripts/faucet.py --token usdc --claims 10
   ```

5. **For AP2 demo**: additional Python dependencies:
   ```bash
   uv pip install eth-account requests python-dotenv
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

Results are saved to `/tmp/v2_demo_results.json` (ETH), `/tmp/v2_usdc_demo_results.json` (USDC), and `/tmp/ap2_demo_results.json` (AP2).

## Results Documentation

Full transaction logs, settlement math, and sequence diagrams for all demos: [`DEMO_RUN.md`](DEMO_RUN.md)
