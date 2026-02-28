-- Add multi-verifier quorum fields (V3 item 19).
ALTER TABLE escrows ADD COLUMN verifier_panel_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE escrows ADD COLUMN quorum_threshold INTEGER NOT NULL DEFAULT 1;
ALTER TABLE escrows ADD COLUMN quorum_verifier_count INTEGER NOT NULL DEFAULT 1;
ALTER TABLE escrows ADD COLUMN verifier_stake_per_verifier TEXT NOT NULL DEFAULT '0';
