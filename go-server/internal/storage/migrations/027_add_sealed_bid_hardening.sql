ALTER TABLE rfqs ADD COLUMN sealed_bid_status TEXT NOT NULL DEFAULT '';
ALTER TABLE rfqs ADD COLUMN sealed_bid_selection_rule TEXT NOT NULL DEFAULT '';
ALTER TABLE rfqs ADD COLUMN best_bid_id INTEGER REFERENCES bids(id);
ALTER TABLE rfqs ADD COLUMN sealed_bid_finalized_at INTEGER NOT NULL DEFAULT 0;

UPDATE rfqs
SET sealed_bid_selection_rule = 'lowest_amount_then_duration_then_commit_time_then_bid_id'
WHERE bidding_mode = 'sealed'
  AND sealed_bid_selection_rule = '';

CREATE TABLE IF NOT EXISTS sealed_bidder_discipline (
    bidder TEXT PRIMARY KEY,
    non_reveal_count INTEGER NOT NULL DEFAULT 0,
    cooldown_until INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

ALTER TABLE bid_commits RENAME TO bid_commits_old;

CREATE TABLE bid_commits (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    rfq_id INTEGER NOT NULL REFERENCES rfqs(id),
    bidder TEXT NOT NULL,
    commitment TEXT NOT NULL,
    nonce TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'committed' CHECK(status IN ('committed', 'revealed', 'accepted', 'rejected', 'withdrawn', 'expired', 'superseded')),
    revealed_bid_id INTEGER REFERENCES bids(id),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(rfq_id, bidder, nonce),
    UNIQUE(rfq_id, bidder, commitment)
);

INSERT INTO bid_commits (id, rfq_id, bidder, commitment, nonce, status, revealed_bid_id, created_at, updated_at)
SELECT id, rfq_id, bidder, commitment, nonce, status, revealed_bid_id, created_at, updated_at
FROM bid_commits_old;

DROP TABLE bid_commits_old;

CREATE INDEX IF NOT EXISTS idx_bid_commits_rfq_id ON bid_commits(rfq_id);
CREATE INDEX IF NOT EXISTS idx_bid_commits_bidder ON bid_commits(bidder);
CREATE INDEX IF NOT EXISTS idx_bid_commits_status ON bid_commits(status);
CREATE INDEX IF NOT EXISTS idx_bid_commits_revealed_bid_id ON bid_commits(revealed_bid_id);
CREATE INDEX IF NOT EXISTS idx_bid_commits_rfq_status ON bid_commits(rfq_id, status);
CREATE INDEX IF NOT EXISTS idx_bid_commits_rfq_bidder_created_at ON bid_commits(rfq_id, bidder, created_at);
CREATE UNIQUE INDEX IF NOT EXISTS uidx_bid_commits_revealed_bid_id
    ON bid_commits(revealed_bid_id)
    WHERE revealed_bid_id IS NOT NULL;
