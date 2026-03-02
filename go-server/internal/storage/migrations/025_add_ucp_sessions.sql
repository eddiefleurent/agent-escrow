-- Migration 025: UCP checkout/session projection + idempotency persistence.
CREATE TABLE IF NOT EXISTS ucp_sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    checkout_id TEXT NOT NULL UNIQUE,
    session_id TEXT NOT NULL,
    escrow_id INTEGER NOT NULL REFERENCES escrows(id),
    ucp_status TEXT NOT NULL,
    idempotency_key TEXT,
    last_operation TEXT NOT NULL DEFAULT '',
    last_request_hash TEXT NOT NULL DEFAULT '',
    last_tx_hash TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_ucp_sessions_escrow_id ON ucp_sessions(escrow_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_ucp_sessions_idempotency_key
    ON ucp_sessions(idempotency_key)
    WHERE idempotency_key IS NOT NULL AND idempotency_key != '';

CREATE TABLE IF NOT EXISTS ucp_idempotency (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    idempotency_key TEXT NOT NULL UNIQUE,
    operation TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    response_json TEXT NOT NULL,
    checkout_id TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
