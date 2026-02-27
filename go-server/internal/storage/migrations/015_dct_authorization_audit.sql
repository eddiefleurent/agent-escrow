-- Principal authorization audit log for DCT operations (roadmap item 13b).
-- Every authorization decision (permit or deny) is recorded for security
-- auditing and incident response (paper §4.7, §4.9).

CREATE TABLE IF NOT EXISTS dct_authorization_audit (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp       TEXT NOT NULL DEFAULT (datetime('now')),
    operation       TEXT NOT NULL,
    allowed         INTEGER NOT NULL,
    caller_address  TEXT NOT NULL,
    escrow_id       INTEGER,
    token_id        TEXT,
    parent_token_id TEXT,
    reason          TEXT NOT NULL,
    request_id      TEXT,
    metadata        TEXT
);

CREATE INDEX IF NOT EXISTS idx_dct_audit_timestamp ON dct_authorization_audit(timestamp);
CREATE INDEX IF NOT EXISTS idx_dct_audit_escrow ON dct_authorization_audit(escrow_id);
CREATE INDEX IF NOT EXISTS idx_dct_audit_caller ON dct_authorization_audit(caller_address);
