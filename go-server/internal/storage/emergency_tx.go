package storage

import (
	"context"
	"fmt"
)

func (d *DB) RecordFreezeEscrowAndRevokeDCT(ctx context.Context, escrowID int64, escrowAddress, txHash string) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE escrows SET frozen = 1, updated_at = datetime('now') WHERE id = ?`, escrowID); err != nil {
		return fmt.Errorf("update escrow frozen: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE dct_tokens
		 SET revoked_at = datetime('now'), revocation_reason = ?, revoked_by = ?, updated_at = datetime('now')
		 WHERE escrow_id = ? AND revoked_at IS NULL`, "escrow_frozen", "emergency", escrowID); err != nil {
		return fmt.Errorf("revoke dct tokens by escrow: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO emergency_actions (action, target, escrow_id, reason, tx_hash) VALUES (?, ?, ?, ?, ?)`,
		"freeze_escrow", escrowAddress, "", "", txHash); err != nil {
		return fmt.Errorf("create emergency action: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func (d *DB) RecordEmergencyResolveAndRevokeDCT(ctx context.Context, escrowID int64, escrowAddress string, workerAwardBps uint16, txHash string) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE escrows SET status = ?, updated_at = datetime('now') WHERE id = ?`, "resolved", escrowID); err != nil {
		return fmt.Errorf("update escrow status: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE dct_tokens
		 SET revoked_at = datetime('now'), revocation_reason = ?, revoked_by = ?, updated_at = datetime('now')
		 WHERE escrow_id = ? AND revoked_at IS NULL`, "emergency_resolve", "emergency", escrowID); err != nil {
		return fmt.Errorf("revoke dct tokens by escrow: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO emergency_actions (action, target, escrow_id, reason, tx_hash) VALUES (?, ?, ?, ?, ?)`,
		"emergency_resolve", escrowAddress, "", fmt.Sprintf("workerAwardBps=%d", workerAwardBps), txHash); err != nil {
		return fmt.Errorf("create emergency action: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
