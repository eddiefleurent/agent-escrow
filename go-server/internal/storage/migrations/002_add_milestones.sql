ALTER TABLE escrows ADD COLUMN milestone_count INTEGER NOT NULL DEFAULT 1;
ALTER TABLE escrows ADD COLUMN current_milestone INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS milestones (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    escrow_id INTEGER NOT NULL REFERENCES escrows(id),
    milestone_index INTEGER NOT NULL,
    amount TEXT NOT NULL,
    submission_deadline INTEGER NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    submission_hash TEXT NOT NULL DEFAULT '',
    submission_uri TEXT NOT NULL DEFAULT '',
    submitted_at TEXT,
    approved_at TEXT,
    disputed_at TEXT,
    dispute_reason_uri TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_milestones_escrow_index ON milestones(escrow_id, milestone_index);
