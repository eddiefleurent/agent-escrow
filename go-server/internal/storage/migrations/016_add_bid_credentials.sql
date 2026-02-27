-- Migration 016: Add credential fields for verifiable bid credentials (V3 item 14).
-- RFQ credential requirements and bid attestations with verification state.

-- RFQ-side: JSON array of credential requirement selectors.
ALTER TABLE rfqs ADD COLUMN required_credentials_json TEXT NOT NULL DEFAULT '[]';

-- Bid-side: JSON array of attestation payloads presented at reveal.
ALTER TABLE bids ADD COLUMN credentials_json TEXT NOT NULL DEFAULT '[]';

-- Bid-side: verification result summary (pass/fail + reasons).
ALTER TABLE bids ADD COLUMN credential_verified INTEGER NOT NULL DEFAULT 0;
ALTER TABLE bids ADD COLUMN credential_match_summary TEXT NOT NULL DEFAULT '{}';
