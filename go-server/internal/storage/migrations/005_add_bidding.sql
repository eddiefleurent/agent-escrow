CREATE TABLE IF NOT EXISTS rfqs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    description TEXT NOT NULL,
    spec_hash TEXT NOT NULL,
    buyer TEXT NOT NULL,
    token TEXT NOT NULL DEFAULT '',
    budget_min TEXT NOT NULL,
    budget_max TEXT NOT NULL,
    deadline INTEGER NOT NULL,
    review_period_seconds INTEGER NOT NULL,
    dispute_period_seconds INTEGER NOT NULL,
    arbitrator_timeout_seconds INTEGER NOT NULL,
    verifier TEXT NOT NULL DEFAULT '',
    arbitrator TEXT NOT NULL DEFAULT '',
    worker_stake TEXT NOT NULL DEFAULT '0',
    milestones_json TEXT NOT NULL DEFAULT '[]',
    requirements_json TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'open' CHECK(status IN ('open', 'closed', 'cancelled', 'expired')),
    expires_at INTEGER NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_rfqs_status ON rfqs(status);
CREATE INDEX IF NOT EXISTS idx_rfqs_buyer ON rfqs(buyer);

CREATE TABLE IF NOT EXISTS bids (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    rfq_id INTEGER NOT NULL REFERENCES rfqs(id),
    bidder TEXT NOT NULL,
    amount TEXT NOT NULL,
    estimated_duration INTEGER NOT NULL DEFAULT 0,
    reputation_bond TEXT NOT NULL DEFAULT '0',
    milestones_json TEXT NOT NULL DEFAULT '[]',
    message TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'accepted', 'rejected', 'withdrawn', 'expired')),
    escrow_id INTEGER REFERENCES escrows(id),
    expires_at INTEGER NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_bids_rfq_id ON bids(rfq_id);
CREATE INDEX IF NOT EXISTS idx_bids_bidder ON bids(bidder);
CREATE INDEX IF NOT EXISTS idx_bids_status ON bids(status);
