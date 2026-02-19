CREATE TABLE IF NOT EXISTS tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    spec_hash TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS escrows (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id INTEGER NOT NULL REFERENCES tasks(id),
    chain_id INTEGER NOT NULL,
    factory_address TEXT NOT NULL,
    escrow_address TEXT NOT NULL,
    escrow_id INTEGER NOT NULL,
    buyer TEXT NOT NULL,
    worker TEXT NOT NULL,
    verifier TEXT NOT NULL,
    arbitrator TEXT NOT NULL,
    amount TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'created',
    submission_deadline TEXT NOT NULL,
    review_period_seconds INTEGER NOT NULL,
    dispute_period_seconds INTEGER NOT NULL,
    arbitrator_timeout_seconds INTEGER NOT NULL DEFAULT 604800,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_escrows_address ON escrows(escrow_address);

CREATE TABLE IF NOT EXISTS submissions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    escrow_id INTEGER NOT NULL REFERENCES escrows(id),
    submission_hash TEXT NOT NULL,
    submission_uri TEXT NOT NULL DEFAULT '',
    submitted_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS disputes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    escrow_id INTEGER NOT NULL REFERENCES escrows(id),
    raised_by TEXT NOT NULL,
    reason_uri TEXT NOT NULL DEFAULT '',
    resolution_uri TEXT NOT NULL DEFAULT '',
    worker_award_bps INTEGER,
    status TEXT NOT NULL DEFAULT 'open',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    resolved_at TEXT
);

CREATE TABLE IF NOT EXISTS chain_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tx_hash TEXT NOT NULL,
    log_index INTEGER NOT NULL,
    block_number INTEGER NOT NULL,
    event_name TEXT NOT NULL,
    contract_address TEXT NOT NULL,
    raw_data TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_chain_logs_unique ON chain_logs(tx_hash, log_index);

CREATE TABLE IF NOT EXISTS chain_cursors (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    chain_id INTEGER NOT NULL,
    cursor_key TEXT NOT NULL,
    block_number INTEGER NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_chain_cursors_key ON chain_cursors(chain_id, cursor_key);
