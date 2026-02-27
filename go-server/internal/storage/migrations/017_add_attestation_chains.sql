-- Migration 017: Attestation chains for recursive delegation verification (paper §4.8)
-- Adds parent_escrow_id linkage to rfqs and escrows, plus attestation chain storage.

ALTER TABLE rfqs ADD COLUMN parent_escrow_id INTEGER REFERENCES escrows(id);
ALTER TABLE escrows ADD COLUMN parent_escrow_id INTEGER REFERENCES escrows(id);

CREATE TABLE IF NOT EXISTS attestation_chains (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    escrow_id INTEGER NOT NULL REFERENCES escrows(id),
    milestone_index INTEGER,
    root_hash TEXT NOT NULL DEFAULT '',
    verified INTEGER NOT NULL DEFAULT 0,
    verification_summary_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS attestation_links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    chain_id INTEGER NOT NULL REFERENCES attestation_chains(id),
    link_id TEXT NOT NULL,
    parent_link_id TEXT NOT NULL DEFAULT '',
    from_address TEXT NOT NULL,
    to_address TEXT NOT NULL,
    child_escrow_id INTEGER REFERENCES escrows(id),
    task_spec_hash TEXT NOT NULL DEFAULT '',
    outcome_hash TEXT NOT NULL DEFAULT '',
    issued_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    nonce TEXT NOT NULL,
    signature TEXT NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_attestation_chains_escrow ON attestation_chains(escrow_id);
CREATE INDEX IF NOT EXISTS idx_attestation_chains_escrow_milestone ON attestation_chains(escrow_id, milestone_index);
CREATE INDEX IF NOT EXISTS idx_attestation_links_chain ON attestation_links(chain_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_attestation_links_chain_link ON attestation_links(chain_id, link_id);
CREATE INDEX IF NOT EXISTS idx_escrows_parent ON escrows(parent_escrow_id);
CREATE INDEX IF NOT EXISTS idx_rfqs_parent ON rfqs(parent_escrow_id);
