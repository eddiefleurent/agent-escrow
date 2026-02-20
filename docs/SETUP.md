# Setup

## Solidity Version Notes

| Version | Key Feature / Change |
|---|---|
| 0.8.34 | High-severity bugfix for transient storage clearing in the IR pipeline. |

Current project target:
- Solidity compiler: `0.8.34` (`foundry.toml`)
- Contract/test pragmas: `^0.8.34`

## Prerequisites
- Linux/macOS shell
- `curl`, `git`
- Go 1.26+ (for off-chain server)

## Install Foundry
```bash
curl -L https://foundry.paradigm.xyz | bash
foundryup
```

If `forge` is not found after install:
```bash
source ~/.bashrc
```

## Build and Test Contracts
```bash
make build
make test
make test-invariant
```

## Go Server Setup

### Build
```bash
make go-abi      # copy ABI artifacts from Foundry output
make go-build    # compile Go binary to go-server/bin/server
```

### Test
```bash
make go-test
```

### Run
Set required environment variables:
```bash
export RPC_URL=https://sepolia.base.org
export FACTORY_ADDRESS=0x...
export PRIVATE_KEY=0x...
```

Optional:
```bash
export CHAIN_ID=84532         # default: 84532 (Base Sepolia)
export PORT=8080               # default: 8080
export DATABASE_URL=app.db     # default: delegation.db
export MCP_TRANSPORT=stdio     # enable MCP server on stdio
```

Start the server:
```bash
make go-run
```

### Build Everything
```bash
make all         # forge build + go build
make test-all    # all Solidity + Go tests
```

## Deploy Factory (Base Sepolia)
```bash
export PRIVATE_KEY=0x...
export TREASURY=0x...
export OWNER=0x...
export PROTOCOL_FEE_BPS=100
export BASE_SEPOLIA_RPC_URL=https://sepolia.base.org
make deploy-base-sepolia
```

## Quick Checks
```bash
forge --version
forge config
go version
curl http://localhost:8080/api/v1/health
```
