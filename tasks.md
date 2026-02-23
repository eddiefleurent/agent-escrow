# Open Tasks

Follow-up items from skill and CLI development. Not tied to the V2/V3 roadmap --
these are operational/distribution concerns.

---

## CLI Distribution

- [ ] **GitHub Actions release workflow** -- build `escrow-cli` binaries for
  `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64` on every tagged
  release. Upload as release assets.

- [ ] **Install script** (`install.sh`) -- one-liner that detects platform, downloads
  the right binary from GitHub releases, places it on PATH (e.g. `~/.local/bin`).
  Update `skills/escrow-cli/SKILL.md` setup section once this exists.

- [ ] **Decide on install.sh URL** -- needs a stable URL pattern (GitHub releases
  latest redirect). Update the skill's setup section once the URL is known.

---

## Skills

- [x] **Participant skill** (`skills/escrow-cli/`) -- role-oriented (buyer / worker /
  verifier) with full payload examples. Done.

- [x] **Admin skill** (`skills/escrow-admin/`) -- emergency protocol, AP2, health.
  Skeleton created.

- [x] **Admin skill reference** -- `skills/escrow-admin/references/REFERENCE.md`
  with full emergency + AP2 field schemas including exact types (escrow_id is int,
  not string; mandate_envelope structure; all three mandate payload variants).

- [ ] **Verify skills against live server** -- run the participant skill against Base
  Sepolia to confirm all command examples are accurate. Fix any drift.

---

## Agent Discovery

- [ ] **Server URL distribution** -- when a participant agent has the skill installed,
  how do they know the server URL? Options: operator injects `ESCROW_SERVER_URL` into
  the agent's environment, or we publish the live URL somewhere stable (DEPLOYMENTS.md,
  well-known endpoint, Bazaar listing). Decide and document.

- [ ] **Bazaar listing** (V2 roadmap item) -- register the escrow server as a
  discoverable service so agents can find it without being told the URL explicitly.

---

## Minor

- [ ] **REFERENCE.md for admin skill** -- `skills/escrow-admin/references/REFERENCE.md`
  is missing. Create it (can be a trimmed copy of the participant reference covering
  only emergency + AP2 commands).

- [ ] **Check `escrow-cli` install target** -- `make go-cli-install` puts the binary
  in `~/.local/bin`. Confirm this is on PATH in typical agent environments, or add a
  note to the skill's setup section.
