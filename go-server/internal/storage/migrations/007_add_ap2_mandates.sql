CREATE TABLE IF NOT EXISTS ap2_mandates (
    id TEXT PRIMARY KEY,
    mandate_type TEXT NOT NULL,
    mandate_hash TEXT NOT NULL UNIQUE,
    signer_address TEXT NOT NULL,
    budget_amount TEXT,
    budget_currency TEXT,
    expires_at TEXT,
    escrow_id INTEGER REFERENCES escrows(id),
    funding_tx_hash TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    raw_payload TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_ap2_mandates_escrow ON ap2_mandates(escrow_id);
CREATE INDEX IF NOT EXISTS idx_ap2_mandates_signer ON ap2_mandates(signer_address);
CREATE INDEX IF NOT EXISTS idx_ap2_mandates_hash ON ap2_mandates(mandate_hash);

ALTER TABLE bids ADD COLUMN stake_mandate_id TEXT REFERENCES ap2_mandates(id);
