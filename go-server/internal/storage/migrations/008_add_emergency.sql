CREATE TABLE IF NOT EXISTS frozen_addresses (
    address TEXT PRIMARY KEY,
    frozen_at TEXT NOT NULL DEFAULT (datetime('now')),
    reason TEXT NOT NULL DEFAULT '',
    frozen_by TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS emergency_actions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    action TEXT NOT NULL,
    target TEXT NOT NULL DEFAULT '',
    escrow_id TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    tx_hash TEXT NOT NULL DEFAULT ''
);

ALTER TABLE escrows ADD COLUMN frozen BOOLEAN NOT NULL DEFAULT 0;
