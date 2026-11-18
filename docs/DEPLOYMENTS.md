# Deployments

## Base Sepolia (Chain ID 84532)

> **Record gap (2026-11-17).** No deployment of the V3 contracts (verifier quorum, service tiers, ZK slot, re-delegation surcharge) is recorded in this file. Multi-verifier quorum (V3 item 19) was merged on 2026-02-28 (commit `cca6c93`), after the factories below were deployed, so none of them includes it; no deployment of the other V3 contract features is recorded either. Record the V3 factory address, deploy tx hash, block, and date here after the next Base Sepolia deployment, and re-verify the contract source on the explorer as `CLAUDE.md` requires.

### V2 -- ERC20/USDC Support (Last recorded; pre-V3)

| Item | Value |
|---|---|
| Factory Address | `0x798830e2d3C25cF9296fe06a46D808CFB550e880` |
| Deploy Tx Hash | `0x3890605ab6533b0e6367ec94228a52a9de991680e7537d121392b074e9d6336c` |
| Deploy Block | `37932768` |
| Deployer | `0x458397fDDB048239Ab033054d3F70919a95cF4d3` |
| Owner | `0x458397fDDB048239Ab033054d3F70919a95cF4d3` |
| Treasury | `0x458397fDDB048239Ab033054d3F70919a95cF4d3` |
| Protocol Fee | 100 bps (1%) |
| Date | 2026-02-20 |
| Explorer | [sepolia.basescan.org](https://sepolia.basescan.org/address/0x798830e2d3C25cF9296fe06a46D808CFB550e880) |

### Referenced but unrecorded factory

| Item | Value |
|---|---|
| Factory Address | `0x7006930a9d309ca476b5538800da16525ecb191d` |
| Where it appears | `demo/DEMO_RUN.md` (demos dated 2026-02-24, described there as deployed after the `EscrowDeployer` refactor) and the default address in `go-server/cmd/check_selector/main.go` |
| Deploy tx hash, block, deployer, fee | Not recorded in this repository |

This address is quoted from the demo log and the code, not independently verified here. It predates the V3 quorum and tier contracts, so it should not be treated as a V3 deployment.

### V1 -- Settlement Kernel (Retired)

| Item | Value |
|---|---|
| Factory Address | `0xf10a696e7dfC8B923ddeA2E01B07D0B01a75cf34` |
| Deploy Tx Hash | `0x3c2c097585317e8871eb74f4c89aa6ca8979d6cf8a89dae8087cb8dbd2f2f7e2` |
| Deploy Block | `37910871` |
| Protocol Fee | 100 bps (1%) |
| Date | 2026-02-20 |
| Explorer | [sepolia.basescan.org](https://sepolia.basescan.org/address/0xf10a696e7dfC8B923ddeA2E01B07D0B01a75cf34) |

> Deployer credentials are in a local `.env` file (not committed).
> See [DEPLOY.md](DEPLOY.md) for the full deployment guide.
