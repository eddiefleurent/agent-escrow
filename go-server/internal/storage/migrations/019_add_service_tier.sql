-- Service tier support (paper §5.3: tiered service levels)
-- 0 = low_assurance (optimistic), 1 = high_assurance (verifier required)
ALTER TABLE escrows ADD COLUMN service_tier INTEGER NOT NULL DEFAULT 0;
ALTER TABLE rfqs ADD COLUMN service_tier INTEGER NOT NULL DEFAULT 0;
