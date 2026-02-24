-- Preserve compatibility for RFQs created before sealed bidding columns existed.
-- If these rows were auto-filled by the prior defaults (sealed/0/0), normalize them
-- back to open-style semantics using each RFQ's existing deadline.
UPDATE rfqs
SET
    bidding_mode = 'open',
    commit_deadline = deadline,
    reveal_deadline = deadline
WHERE bidding_mode = 'sealed'
  AND commit_deadline = 0
  AND reveal_deadline = 0;

-- Ensure one-to-one linkage between a revealed bid and a bid_commit.
-- For any pre-existing duplicates, keep the newest linkage and detach older ones.
WITH ranked AS (
    SELECT
        id,
        ROW_NUMBER() OVER (PARTITION BY revealed_bid_id ORDER BY id DESC) AS rn
    FROM bid_commits
    WHERE revealed_bid_id IS NOT NULL
)
UPDATE bid_commits
SET
    revealed_bid_id = NULL,
    status = CASE WHEN status = 'revealed' THEN 'committed' ELSE status END,
    updated_at = datetime('now')
WHERE id IN (SELECT id FROM ranked WHERE rn > 1);

CREATE UNIQUE INDEX IF NOT EXISTS uidx_bid_commits_revealed_bid_id
    ON bid_commits(revealed_bid_id)
    WHERE revealed_bid_id IS NOT NULL;
