-- Migration 022: append-only reputation events for damped scoring (V3 market stability)
CREATE TABLE IF NOT EXISTS reputation_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    address TEXT NOT NULL,
    role TEXT NOT NULL CHECK(role IN ('worker', 'buyer')),
    outcome TEXT NOT NULL CHECK(outcome IN ('completed', 'disputed', 'failed')),
    tx_hash TEXT NOT NULL,
    log_index INTEGER NOT NULL,
    block_number INTEGER NOT NULL DEFAULT 0,
    occurred_at TEXT NOT NULL DEFAULT (datetime('now')),
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_reputation_events_tx_log_addr_role
    ON reputation_events(tx_hash, log_index, lower(address), role);

CREATE INDEX IF NOT EXISTS idx_reputation_events_addr_role_time
    ON reputation_events(lower(address), role, occurred_at, id);
