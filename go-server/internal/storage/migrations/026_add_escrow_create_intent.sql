ALTER TABLE escrows ADD COLUMN create_intent_id TEXT NOT NULL DEFAULT '';
ALTER TABLE escrows ADD COLUMN create_tx_hash TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_escrows_create_intent
    ON escrows(create_intent_id)
    WHERE create_intent_id != '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_escrows_create_tx_hash
    ON escrows(create_tx_hash)
    WHERE create_tx_hash != '';
