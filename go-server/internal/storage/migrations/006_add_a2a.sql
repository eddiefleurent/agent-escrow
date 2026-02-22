CREATE TABLE IF NOT EXISTS a2a_tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    a2a_task_id TEXT NOT NULL UNIQUE,
    session_id TEXT NOT NULL,
    escrow_id INTEGER REFERENCES escrows(id),
    delegator_agent TEXT NOT NULL,
    delegatee_agent TEXT NOT NULL DEFAULT '',
    verification_policy_json TEXT NOT NULL DEFAULT '{}',
    escrow_trigger BOOLEAN NOT NULL DEFAULT 0,
    a2a_status TEXT NOT NULL DEFAULT 'submitted',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_a2a_tasks_session ON a2a_tasks(session_id);
CREATE INDEX IF NOT EXISTS idx_a2a_tasks_escrow ON a2a_tasks(escrow_id);
CREATE INDEX IF NOT EXISTS idx_a2a_tasks_status ON a2a_tasks(a2a_status);
