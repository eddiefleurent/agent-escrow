package authz

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
)

// AuditEntry represents a single authorization decision to be logged.
type AuditEntry struct {
	Operation     Operation
	Allowed       bool
	CallerAddress string
	EscrowID      int64
	TokenID       string
	ParentTokenID string
	Reason        DenyReason
	RequestID     string
	Metadata      map[string]any
}

// AuditStore persists authorization audit entries.
type AuditStore interface {
	LogAuthzDecision(ctx context.Context, e AuditEntry) error
	ListAuthzAudit(ctx context.Context, escrowID int64, limit, offset int) ([]AuditRecord, error)
}

// AuditRecord is a row from the dct_authorization_audit table.
type AuditRecord struct {
	ID            int64  `json:"id"`
	Timestamp     string `json:"timestamp"`
	Operation     string `json:"operation"`
	Allowed       bool   `json:"allowed"`
	CallerAddress string `json:"caller_address"`
	EscrowID      int64  `json:"escrow_id,omitempty"`
	TokenID       string `json:"token_id,omitempty"`
	ParentTokenID string `json:"parent_token_id,omitempty"`
	Reason        string `json:"reason"`
	RequestID     string `json:"request_id,omitempty"`
	Metadata      string `json:"metadata,omitempty"`
}

// SQLiteAuditStore implements AuditStore using a *sql.DB.
type SQLiteAuditStore struct {
	DB *sql.DB
}

func (s *SQLiteAuditStore) LogAuthzDecision(ctx context.Context, e AuditEntry) error {
	var metadataJSON string
	if len(e.Metadata) > 0 {
		b, err := json.Marshal(e.Metadata)
		if err != nil {
			slog.Warn("authz audit: failed to marshal metadata", "error", err)
			metadataJSON = "{}"
		} else {
			metadataJSON = string(b)
		}
	}

	allowedInt := 0
	if e.Allowed {
		allowedInt = 1
	}

	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO dct_authorization_audit
		 (operation, allowed, caller_address, escrow_id, token_id, parent_token_id, reason, request_id, metadata)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(e.Operation), allowedInt, e.CallerAddress,
		e.EscrowID, e.TokenID, e.ParentTokenID,
		string(e.Reason), e.RequestID, metadataJSON,
	)
	if err != nil {
		return fmt.Errorf("authz audit log: %w", err)
	}
	return nil
}

func (s *SQLiteAuditStore) ListAuthzAudit(ctx context.Context, escrowID int64, limit, offset int) ([]AuditRecord, error) {
	if limit <= 0 {
		limit = 50
	}

	var rows *sql.Rows
	var err error
	if escrowID > 0 {
		rows, err = s.DB.QueryContext(ctx,
			`SELECT id, timestamp, operation, allowed, caller_address, escrow_id, token_id, parent_token_id, reason, request_id, metadata
			 FROM dct_authorization_audit WHERE escrow_id = ? ORDER BY id DESC LIMIT ? OFFSET ?`,
			escrowID, limit, offset)
	} else {
		rows, err = s.DB.QueryContext(ctx,
			`SELECT id, timestamp, operation, allowed, caller_address, escrow_id, token_id, parent_token_id, reason, request_id, metadata
			 FROM dct_authorization_audit ORDER BY id DESC LIMIT ? OFFSET ?`,
			limit, offset)
	}
	if err != nil {
		return nil, fmt.Errorf("list authz audit: %w", err)
	}
	defer rows.Close()

	var records []AuditRecord
	for rows.Next() {
		var r AuditRecord
		var allowed int
		var escrowIDNull sql.NullInt64
		var tokenID, parentTokenID, requestID, metadata sql.NullString
		if err := rows.Scan(&r.ID, &r.Timestamp, &r.Operation, &allowed,
			&r.CallerAddress, &escrowIDNull, &tokenID, &parentTokenID,
			&r.Reason, &requestID, &metadata); err != nil {
			return nil, fmt.Errorf("scan authz audit: %w", err)
		}
		r.Allowed = allowed == 1
		if escrowIDNull.Valid {
			r.EscrowID = escrowIDNull.Int64
		}
		if tokenID.Valid {
			r.TokenID = tokenID.String
		}
		if parentTokenID.Valid {
			r.ParentTokenID = parentTokenID.String
		}
		if requestID.Valid {
			r.RequestID = requestID.String
		}
		if metadata.Valid {
			r.Metadata = metadata.String
		}
		records = append(records, r)
	}
	return records, rows.Err()
}
