package storage

import (
	"context"
	"database/sql"
	"fmt"
)

const dctColumns = `id, token_id, token_hash, parent_token_id, escrow_id, subject, issuer,
	operations_json, resources_json, profile, caveats_json, depth, expires_at, revoked_at, revocation_reason, revoked_by, created_at, updated_at`

func scanDCTToken(scanner interface{ Scan(...any) error }) (*DCTToken, error) {
	t := &DCTToken{}
	var parentTokenID sql.NullString
	var revokedAt sql.NullString
	var createdAt, updatedAt string
	if err := scanner.Scan(
		&t.ID, &t.TokenID, &t.TokenHash, &parentTokenID, &t.EscrowID, &t.Subject, &t.Issuer,
		&t.OperationsJSON, &t.ResourcesJSON, &t.Profile, &t.CaveatsJSON, &t.Depth, &t.ExpiresAt, &revokedAt, &t.RevocationReason, &t.RevokedBy,
		&createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}
	if parentTokenID.Valid {
		t.ParentTokenID = parentTokenID.String
	}
	if revokedAt.Valid {
		ts, err := parseSQLiteTime(revokedAt.String)
		if err != nil {
			return nil, fmt.Errorf("parse revoked_at: %w", err)
		}
		t.RevokedAt = &ts
	}
	var err error
	t.CreatedAt, err = parseSQLiteTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	t.UpdatedAt, err = parseSQLiteTime(updatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse updated_at: %w", err)
	}
	return t, nil
}

func (d *DB) CreateDCTToken(ctx context.Context, t *DCTToken) (*DCTToken, error) {
	res, err := d.db.ExecContext(ctx,
		`INSERT INTO dct_tokens (token_id, token_hash, parent_token_id, escrow_id, subject, issuer, operations_json, resources_json, profile, caveats_json, depth, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.TokenID, t.TokenHash, nilIfEmpty(t.ParentTokenID), t.EscrowID, t.Subject, t.Issuer,
		t.OperationsJSON, t.ResourcesJSON, t.Profile, t.CaveatsJSON, t.Depth, t.ExpiresAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert dct token: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	return d.GetDCTTokenByID(ctx, id)
}

func (d *DB) GetDCTTokenByID(ctx context.Context, id int64) (*DCTToken, error) {
	row := d.db.QueryRowContext(ctx, `SELECT `+dctColumns+` FROM dct_tokens WHERE id = ?`, id)
	t, err := scanDCTToken(row)
	if err != nil {
		return nil, fmt.Errorf("get dct token by id: %w", err)
	}
	return t, nil
}

func (d *DB) GetDCTTokenByTokenID(ctx context.Context, tokenID string) (*DCTToken, error) {
	row := d.db.QueryRowContext(ctx, `SELECT `+dctColumns+` FROM dct_tokens WHERE token_id = ?`, tokenID)
	t, err := scanDCTToken(row)
	if err != nil {
		return nil, fmt.Errorf("get dct token by token_id: %w", err)
	}
	return t, nil
}

func (d *DB) GetDCTTokenByTokenHash(ctx context.Context, tokenHash string) (*DCTToken, error) {
	row := d.db.QueryRowContext(ctx, `SELECT `+dctColumns+` FROM dct_tokens WHERE token_hash = ?`, tokenHash)
	t, err := scanDCTToken(row)
	if err != nil {
		return nil, fmt.Errorf("get dct token by token_hash: %w", err)
	}
	return t, nil
}

func (d *DB) RevokeDCTToken(ctx context.Context, tokenID, reason, revokedBy string) error {
	res, err := d.db.ExecContext(ctx,
		`UPDATE dct_tokens
		 SET revoked_at = datetime('now'), revocation_reason = ?, revoked_by = ?, updated_at = datetime('now')
		 WHERE token_id = ? AND revoked_at IS NULL`, reason, revokedBy, tokenID)
	if err != nil {
		return fmt.Errorf("revoke dct token: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("revoke dct token rows affected: %w", err)
	}
	if n > 0 {
		return nil
	}

	var revokedAt sql.NullString
	if err := d.db.QueryRowContext(ctx, `SELECT revoked_at FROM dct_tokens WHERE token_id = ?`, tokenID).Scan(&revokedAt); err != nil {
		if err == sql.ErrNoRows {
			return sql.ErrNoRows
		}
		return fmt.Errorf("lookup dct token after revoke: %w", err)
	}
	if revokedAt.Valid {
		return nil
	}
	return sql.ErrNoRows
}

func (d *DB) RevokeDCTTokensByEscrow(ctx context.Context, escrowID int64, reason, revokedBy string) (int64, error) {
	res, err := d.db.ExecContext(ctx,
		`UPDATE dct_tokens
		 SET revoked_at = datetime('now'), revocation_reason = ?, revoked_by = ?, updated_at = datetime('now')
		 WHERE escrow_id = ? AND revoked_at IS NULL`, reason, revokedBy, escrowID)
	if err != nil {
		return 0, fmt.Errorf("revoke dct tokens by escrow: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("revoke dct tokens by escrow rows affected: %w", err)
	}
	return n, nil
}

func (d *DB) ListDCTTokensByEscrow(ctx context.Context, escrowID int64) ([]*DCTToken, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT `+dctColumns+` FROM dct_tokens WHERE escrow_id = ? ORDER BY id DESC`, escrowID)
	if err != nil {
		return nil, fmt.Errorf("list dct tokens by escrow: %w", err)
	}
	defer rows.Close()

	var out []*DCTToken
	for rows.Next() {
		t, err := scanDCTToken(rows)
		if err != nil {
			return nil, fmt.Errorf("scan dct token: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
