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

### Admin Access and CORS

- HTTP emergency routes and DCT emergency override are disabled by default. To enable them, set `EMERGENCY_ENABLED=true` and provision `ADMIN_TOKEN` from your secret store. Generate a random token with `openssl rand -hex 32`. Startup rejects tokens shorter than 32 characters when HTTP emergency routes are enabled.
- All `/api/v1/emergency/*` routes, `POST /api/v1/dcts/emergency-override`, and `GET /api/v1/dcts/audit` require `Authorization: Bearer <ADMIN_TOKEN>`. DCT audit can be enabled independently by provisioning the token alone. Set `OWNER_ADDRESS` to the factory owner for DCT emergency override. On-chain emergency transactions require the server's signing key to belong to the factory owner.
- Set `ESCROW_ADMIN_TOKEN` in the CLI environment to the same value as the server's `ADMIN_TOKEN`. Use HTTPS for remote admin requests.
- To expose MCP emergency tools and DCT audit, set `MCP_TRANSPORT=stdio` and `MCP_EMERGENCY_ENABLED=true`. This opt-in is independent of HTTP emergency access because the stdio session is local to the operator and trusts the client attached to that process.
- Leave `CORS_ORIGINS` unset to omit `Access-Control-Allow-Origin` from HTTP responses. With no allowlist, browser WebSocket upgrades also require a matching origin host. To allow cross-origin access, configure a comma-separated list of allowed origins or explicitly set `*`.

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
