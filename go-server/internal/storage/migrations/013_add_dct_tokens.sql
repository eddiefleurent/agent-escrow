CREATE TABLE IF NOT EXISTS dct_tokens (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    token_id TEXT NOT NULL UNIQUE,
    token_hash TEXT NOT NULL UNIQUE,
    parent_token_id TEXT,
    escrow_id INTEGER NOT NULL REFERENCES escrows(id),
    subject TEXT NOT NULL,
    issuer TEXT NOT NULL,
    operations_json TEXT NOT NULL,
    resources_json TEXT NOT NULL,
    expires_at INTEGER NOT NULL,
    revoked_at TEXT,
    revocation_reason TEXT NOT NULL DEFAULT '',
    revoked_by TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_dct_tokens_escrow_id ON dct_tokens(escrow_id);
CREATE INDEX IF NOT EXISTS idx_dct_tokens_subject ON dct_tokens(subject);
CREATE INDEX IF NOT EXISTS idx_dct_tokens_parent_token_id ON dct_tokens(parent_token_id);
