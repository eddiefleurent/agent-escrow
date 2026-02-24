# Open Tasks

Operational/distribution backlog snapshot as of 2026-02-24.

---

## Release + Install

- [ ] **GitHub Actions release workflow** -- build `escrow-cli` binaries for
  `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64` on tagged
  releases and upload as release assets.

- [ ] **Install script** (`install.sh`) -- one-liner that detects platform,
  downloads the right binary from GitHub Releases, and installs to PATH
  (for example `~/.local/bin`).

- [ ] **Stable install URL** -- decide and document a canonical URL strategy
  for installer bootstrap (latest release redirect vs pinned version URL).

## Agent Discovery + Distribution

- [ ] **Server URL distribution policy** -- decide canonical discovery path for
  participant agents (`ESCROW_SERVER_URL` injection, DEPLOYMENTS reference,
  Bazaar listing, or well-known endpoint) and document it.

- [ ] **Bazaar listing execution** -- publish the escrow service in Bazaar
  (roadmap integration exists; operational listing still pending).

- [ ] **PATH note in skill docs** -- confirm `~/.local/bin` is available in
  typical agent runtime environments, and keep setup guidance explicit where
  it is not.

## Completed (for context)

- [x] **Participant skill** (`skills/escrow-cli/`) implemented.
- [x] **Admin skill** (`skills/escrow-admin/`) implemented with reference docs.
- [x] **Live CLI flow exercised on Base Sepolia** via Codex agent run and
  recorded in `demo/DEMO_RUN.md` (2026-02-24).
