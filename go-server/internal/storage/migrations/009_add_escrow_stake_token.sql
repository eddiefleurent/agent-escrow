-- Add worker_stake and token columns to escrows table.
-- These were added to the CREATE TABLE definition in 001 but existing
-- databases need the ALTER TABLE path.
ALTER TABLE escrows ADD COLUMN worker_stake TEXT NOT NULL DEFAULT '0';
ALTER TABLE escrows ADD COLUMN token TEXT NOT NULL DEFAULT '0x0000000000000000000000000000000000000000';
