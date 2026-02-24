ALTER TABLE rfqs ADD COLUMN bidding_mode TEXT NOT NULL DEFAULT 'sealed';
ALTER TABLE rfqs ADD COLUMN commit_deadline INTEGER NOT NULL DEFAULT 0;
ALTER TABLE rfqs ADD COLUMN reveal_deadline INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS bid_commits (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    rfq_id INTEGER NOT NULL REFERENCES rfqs(id),
    bidder TEXT NOT NULL,
    commitment TEXT NOT NULL,
    nonce TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'committed' CHECK(status IN ('committed', 'revealed', 'accepted', 'rejected', 'withdrawn', 'expired')),
    revealed_bid_id INTEGER REFERENCES bids(id),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(rfq_id, bidder, nonce),
    UNIQUE(rfq_id, bidder, commitment)
);

CREATE INDEX IF NOT EXISTS idx_bid_commits_rfq_id ON bid_commits(rfq_id);
CREATE INDEX IF NOT EXISTS idx_bid_commits_bidder ON bid_commits(bidder);
CREATE INDEX IF NOT EXISTS idx_bid_commits_status ON bid_commits(status);
CREATE INDEX IF NOT EXISTS idx_bid_commits_revealed_bid_id ON bid_commits(revealed_bid_id);
