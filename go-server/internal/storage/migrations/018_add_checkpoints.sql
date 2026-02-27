-- Checkpoint artifacts for mid-task agent swaps (paper §6.1: checkpoint artifacts + partial compensation clauses).
-- Standardized state snapshots committed by the active worker, enabling a replacement worker to resume.
CREATE TABLE IF NOT EXISTS checkpoints (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    escrow_id INTEGER NOT NULL REFERENCES escrows(id),
    milestone_index INTEGER,
    state_snapshot_uri TEXT NOT NULL,
    snapshot_hash TEXT NOT NULL DEFAULT '',
    schema_version TEXT NOT NULL DEFAULT 'checkpoint-v1',
    committed_by TEXT NOT NULL,
    completion_pct INTEGER,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_checkpoints_escrow_id ON checkpoints(escrow_id);
CREATE INDEX IF NOT EXISTS idx_checkpoints_escrow_milestone ON checkpoints(escrow_id, milestone_index);
