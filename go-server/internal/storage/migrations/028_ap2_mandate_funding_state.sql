-- AP2 funding state and authorization-nonce dedup.
--
-- New rows use status pending, then funded (receipt status 1) or failed
-- (chain error before broadcast, or a reverted receipt). A broadcast whose
-- receipt wait times out stays pending and keeps its tx hash.
--
-- Legacy rows were written before EIP-712 digest ids. Their id is the first
-- 16 hex chars of sha256(envelope JSON), and raw_payload is only the mandate
-- body (the authorization object was not stored). This migration backfills
-- chain_id, token, authorizer, and authorization_nonce when that payload
-- contains authorization.nonce or, failing that, nonce, and the row is linked
-- to an escrow. The payload nonce is not guaranteed to be the EIP-3009 nonce
-- on those rows. It is the only copy this database kept.
--
-- Intent and cart rows from that era usually have neither field. Their new
-- columns stay NULL. They are testnet-only and are left out of the unique
-- index, so they are not part of nonce dedup. A nonce that is not 0x-prefixed
-- 32-byte hex is stored lowercased as it appeared and may not collide with a
-- canonical nonce written by the current server.

ALTER TABLE ap2_mandates ADD COLUMN chain_id INTEGER;
ALTER TABLE ap2_mandates ADD COLUMN token TEXT;
ALTER TABLE ap2_mandates ADD COLUMN authorizer TEXT;
ALTER TABLE ap2_mandates ADD COLUMN authorization_nonce TEXT;

UPDATE ap2_mandates
SET
    chain_id = (SELECT e.chain_id FROM escrows e WHERE e.id = ap2_mandates.escrow_id),
    token = lower((SELECT e.token FROM escrows e WHERE e.id = ap2_mandates.escrow_id)),
    authorizer = lower(signer_address),
    authorization_nonce = lower(COALESCE(
        json_extract(raw_payload, '$.authorization.nonce'),
        json_extract(raw_payload, '$.nonce')
    ))
WHERE authorization_nonce IS NULL
  AND escrow_id IS NOT NULL
  AND (
      json_extract(raw_payload, '$.authorization.nonce') IS NOT NULL
      OR json_extract(raw_payload, '$.nonce') IS NOT NULL
  );

-- 'bound' was the pre-receipt status. A row with no tx hash never broadcast
-- successfully. One with a hash was marked funded only after this migration
-- if the old code had already stored the hash under status funded. Rows still
-- stuck at 'bound' with a hash are treated as an unconfirmed broadcast.
UPDATE ap2_mandates
SET status = 'pending'
WHERE status = 'bound'
  AND funding_tx_hash IS NOT NULL
  AND funding_tx_hash != '';

UPDATE ap2_mandates
SET status = 'failed'
WHERE status = 'bound'
  AND (funding_tx_hash IS NULL OR funding_tx_hash = '');

CREATE UNIQUE INDEX IF NOT EXISTS idx_ap2_mandates_auth_nonce
ON ap2_mandates(chain_id, token, authorizer, authorization_nonce)
WHERE chain_id IS NOT NULL
  AND token IS NOT NULL AND token != ''
  AND authorizer IS NOT NULL AND authorizer != ''
  AND authorization_nonce IS NOT NULL AND authorization_nonce != '';
