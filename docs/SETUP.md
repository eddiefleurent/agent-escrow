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
# export MAX_BODY_BYTES=1048576       # default: 1 MiB
# export RATE_LIMIT_RPS=10            # 0 disables; default 10
# export RATE_LIMIT_BURST=20          # 0 disables; default 20
# export TRUSTED_PROXIES=10.0.0.0/8   # CIDRs allowed to send X-Forwarded-For
```

Start the server:
```bash
make go-run
```

### Admin Access and CORS

- HTTP emergency routes and DCT emergency override are disabled by default. To enable them, set `EMERGENCY_ENABLED=true` and provision `ADMIN_TOKEN` from your secret store. Generate a random token with `openssl rand -hex 32`. Startup rejects tokens shorter than 32 characters when HTTP emergency routes are enabled.
- All `/api/v1/emergency/*` routes, `POST /api/v1/dcts/emergency-override`, and `GET /api/v1/dcts/audit` require `Authorization: Bearer <ADMIN_TOKEN>`. DCT audit can be enabled independently by provisioning the token alone. Set `OWNER_ADDRESS` to the factory owner for DCT emergency override. On-chain emergency transactions require the server's signing key to belong to the factory owner.
- Set `ESCROW_ADMIN_TOKEN` in the CLI environment to the same value as the server's `ADMIN_TOKEN`. Use HTTPS for remote admin requests.
- To expose MCP emergency tools and DCT audit, set `MCP_TRANSPORT=stdio` and `MCP_EMERGENCY_ENABLED=true`. This opt-in is independent of HTTP emergency access because the stdio session is local to the operator and trusts the client attached to that process. Enabling it grants that MCP client full owner rights (any `owner`/`caller` argument it sends is treated as authenticated), so only enable it for an operator-controlled agent.
- Leave `CORS_ORIGINS` unset to omit `Access-Control-Allow-Origin` from HTTP responses. With no allowlist, browser WebSocket upgrades also require a matching origin host. To allow cross-origin access, configure a comma-separated list of allowed origins or explicitly set `*`.

### HTTP Request Limits

- `MAX_BODY_BYTES` caps every request body (default `1048576`, 1 MiB). A larger body, including a chunked body with no `Content-Length`, returns `413` and a JSON error. The edge reads the body through `http.MaxBytesReader` before the handler decodes it.
- `RATE_LIMIT_RPS` and `RATE_LIMIT_BURST` set a per-client-IP token bucket for `POST`, `PUT`, `PATCH`, and `DELETE` (defaults `10` and `20`). Either value set to `0` disables the limiter. `GET` is not limited. SSE (`/api/v1/events` and `/api/v1/escrows/{id}/events`) and the WebSocket endpoint (`/api/v1/events/ws`) are exempt. A rejected request returns `429` with `Retry-After`.
- The client IP is taken from `RemoteAddr`. `X-Forwarded-For` is used only when that peer belongs to a CIDR in `TRUSTED_PROXIES` (comma-separated, for example `10.0.0.0/8,172.16.0.0/12`). The leftmost forwarded address is the client, so a trusted proxy should overwrite the header. Unset, the header is ignored and cannot be used to pick a fresh bucket.
- Idle rate-limit buckets are dropped after 10 minutes, and the table keeps at most 10000 addresses.
- Every response echoes `X-Request-ID`. An inbound id is kept when it is at most 128 characters and contains only `A-Z`, `a-z`, `0-9`, `-`, `_`, and `.`. Anything else is replaced with a UUID. The id is stored on the request context, written on the HTTP access log as `request_id`, and copied into JSON error bodies.

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
