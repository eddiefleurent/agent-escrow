-- ZK verification slot support:
-- - Escrow-level verifier wiring (zk_verifier + circuit_id)
-- - Submission/milestone proof commitments (proof_hash)
ALTER TABLE escrows ADD COLUMN zk_verifier TEXT NOT NULL
    DEFAULT '0x0000000000000000000000000000000000000000'
    CHECK (
        length(zk_verifier) = 42
        AND substr(zk_verifier, 1, 2) = '0x'
        AND substr(zk_verifier, 3) NOT GLOB '*[^0-9A-Fa-f]*'
    );
ALTER TABLE escrows ADD COLUMN circuit_id TEXT NOT NULL
    DEFAULT '0x0000000000000000000000000000000000000000000000000000000000000000'
    CHECK (
        length(circuit_id) = 66
        AND substr(circuit_id, 1, 2) = '0x'
        AND substr(circuit_id, 3) NOT GLOB '*[^0-9A-Fa-f]*'
    );

ALTER TABLE submissions ADD COLUMN proof_hash TEXT NOT NULL
    DEFAULT '0x0000000000000000000000000000000000000000000000000000000000000000'
    CHECK (
        length(proof_hash) = 66
        AND substr(proof_hash, 1, 2) = '0x'
        AND substr(proof_hash, 3) NOT GLOB '*[^0-9A-Fa-f]*'
    );
ALTER TABLE milestones ADD COLUMN proof_hash TEXT NOT NULL
    DEFAULT '0x0000000000000000000000000000000000000000000000000000000000000000'
    CHECK (
        length(proof_hash) = 66
        AND substr(proof_hash, 1, 2) = '0x'
        AND substr(proof_hash, 3) NOT GLOB '*[^0-9A-Fa-f]*'
    );
