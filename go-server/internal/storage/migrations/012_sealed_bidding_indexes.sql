CREATE INDEX IF NOT EXISTS idx_bids_rfq_status_expires_at ON bids(rfq_id, status, expires_at);
CREATE INDEX IF NOT EXISTS idx_bid_commits_rfq_status ON bid_commits(rfq_id, status);
CREATE INDEX IF NOT EXISTS idx_bid_commits_rfq_bidder_created_at ON bid_commits(rfq_id, bidder, created_at);
