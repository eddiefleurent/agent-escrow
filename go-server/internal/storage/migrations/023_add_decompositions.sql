-- Migration 023: contract-first decomposition tooling (paper §4.1)
CREATE TABLE IF NOT EXISTS decompositions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    buyer TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL,
    spec_hash TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK(status IN ('draft', 'valid', 'finalized')) DEFAULT 'draft',
    validation_errors_json TEXT NOT NULL DEFAULT '[]',
    rfq_ids_json TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_decompositions_buyer ON decompositions(lower(buyer), status);

CREATE TABLE IF NOT EXISTS decomposition_nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    decomposition_id INTEGER NOT NULL REFERENCES decompositions(id),
    parent_node_id INTEGER REFERENCES decomposition_nodes(id),
    title TEXT NOT NULL,
    description TEXT NOT NULL,
    verification_type TEXT NOT NULL DEFAULT ''
        CHECK(verification_type IN ('', 'optimistic', 'quorum', 'zk_proof', 'unit_test')),
    verification_details_json TEXT NOT NULL DEFAULT '{}',
    depth INTEGER NOT NULL DEFAULT 0,
    requires_further_decomposition INTEGER NOT NULL DEFAULT 0,
    rfq_id INTEGER REFERENCES rfqs(id),
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_decomposition_nodes_decomp ON decomposition_nodes(decomposition_id);
CREATE INDEX IF NOT EXISTS idx_decomposition_nodes_parent ON decomposition_nodes(parent_node_id);
