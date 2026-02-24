# NEXT_ROADMAP_ITEM_DESIGN: Sealed Bidding (Roadmap Item #12)

## Scope and boundary
Sealed bidding is implemented fully off-chain in the Go marketplace service. On-chain Solidity escrow contracts remain settlement-only.

Repo-verifiable architectural boundary:
- `docs/ROADMAP.md` item 12 defines commit-reveal sealed bidding.
- `docs/ARCHITECTURE.md` defines off-chain RFQ/bid lifecycle with on-chain formalization at acceptance.
- `docs/SPEC.md` keeps contract scope focused on settlement state machine/invariants.

## Final protocol decisions

### 1) Canonical serialization (v1 commitment preimage)
Commitment algorithm:
- `keccak256(utf8(payload))`
- `payload = join('|', fields)`
- Domain separator: `agent-escrow:sealed-bid:v1`

Exact field order:
1. `domain` (`agent-escrow:sealed-bid:v1`)
2. `rfq_id` (base-10 unsigned integer)
3. `bidder` (lowercase `0x` + 40 hex chars)
4. `amount` (base-10 unsigned integer, canonical no leading zeros except `0`)
5. `estimated_duration` (base-10 unsigned integer, allows `0`)
6. `reputation_bond` (base-10 unsigned integer, allows `0`)
7. `milestones_hash` (`keccak256(utf8(canonical_milestones_json))`, hex `0x...`)
8. `message_hash` (`keccak256(utf8(message))`, hex `0x...`)
9. `expires_at` (Unix seconds, base-10 unsigned integer)
10. `stake_mandate_hash` (`keccak256(utf8(trim(stake_mandate_id)))`, hex `0x...`)
11. `nonce` (opaque UTF-8 string, non-empty)
12. `salt` (opaque UTF-8 string, non-empty)

Canonical milestones JSON:
- Input parses as `[]` of `{amount, submission_deadline}` objects.
- Re-encoded with stable compact JSON before hashing.
- Empty/missing value canonicalizes to `[]`.

Encoding rules:
- UTF-8 byte encoding for all text before hashing.
- Amount/time fields are decimal strings (no scientific notation).
- Commit hash is lowercase `0x` + 64 hex chars.

### 2) Deadline semantics and boundaries
For sealed RFQs, these fields are mandatory at create time:
- `commit_deadline`
- `reveal_deadline`
- `expires_at`
- `deadline`

Ordering invariant:
- `now < commit_deadline <= reveal_deadline <= expires_at <= deadline`

Phase boundaries (exact):
- Commit phase open iff `now < commit_deadline`
- Reveal phase open iff `commit_deadline <= now <= reveal_deadline`
- Accept allowed iff `reveal_deadline < now < deadline`

### 3) Commit/reveal policy (duplicates/replacements)
Commit policy:
- Duplicate `(rfq_id, bidder, nonce)` is rejected.
- Duplicate `(rfq_id, bidder, commitment)` is rejected.
- Replacement is supported only by submitting a new commit with a new nonce and different commitment, within cap/rate rules.

Reveal policy:
- Exactly one successful reveal per commit (`status` transitions `committed -> revealed`).
- Reveal payload must recompute to exact commitment under canonical serialization.

### 4) Accept timing rule
Accept is allowed only after reveal phase ends (`now > reveal_deadline`).

Rationale:
- Prevents buyer from accepting early based on partial reveals.
- Avoids reveal sniping dynamics.
- Preserves symmetric information set through reveal window closure.

### 5) Abuse-control defaults
Default controls implemented in service:
- Active commit cap: max **3** active commits (`committed` or `revealed`) per bidder per RFQ.
- Commit rate limit: max **10** commit requests per bidder per RFQ per **60 seconds**.
- Non-reveal behavior: when reveal phase is over, unresolved `committed` rows transition to `expired`.

Non-reveal expiry triggers:
- On reveal attempts after reveal window closes.
- On RFQ reads/list/accept flows when reveal window has passed.

### 6) Visibility/redaction policy
`GET /api/v1/rfqs/{id}` returns commit metadata without `commitment` or `nonce`.
Returned commit fields:
- `id`, `rfq_id`, `bidder`, `status`, `revealed_bid_id`, `created_at`, `updated_at`

## Data model and indexes
Schema uses existing `rfqs`, `bids`, and `bid_commits` tables.

Added indexes:
- `bids(rfq_id, status, expires_at)`
- `bid_commits(rfq_id, status)`
- `bid_commits(rfq_id, bidder, created_at)`

## Lifecycle summary
1. Buyer creates sealed RFQ with all four deadlines.
2. Bidder submits commitment during commit phase.
3. Bidder reveals during reveal phase; server verifies canonical recomputation.
4. Successful reveal materializes a pending `bids` row linked to commit.
5. After reveal close, buyer accepts exactly one pending revealed bid.
6. Winner transitions to `accepted`; other pending bids and unresolved commits transition to rejected/expired according to lifecycle rules.

## Test coverage requirements
Implemented coverage includes:
- CreateRFQ sealed deadline requirements
- Commit duplicate nonce/commitment handling
- Commit cap/rate controls
- Canonical reveal verification and normalization behavior
- Reveal after deadline transitions unresolved commits to `expired`
- Accept boundary enforcement and commit-link validation
- Storage query tests for commit lookup/count/expiry
- API response redaction checks for commit fields

## Why this design is reproducible in-repo
All behavior and rationale above is traceable to repository artifacts:
- Roadmap intent: `docs/ROADMAP.md` item 12
- Architecture boundary: `docs/ARCHITECTURE.md` RFQ/Bid and off-chain/on-chain split
- Settlement contract scope: `docs/SPEC.md`
- Concrete implementation: Go service (`go-server/internal/bidding`), storage (`go-server/internal/storage`), API (`go-server/internal/api`), MCP (`go-server/internal/mcpserver`), CLI (`go-server/internal/cli`)
