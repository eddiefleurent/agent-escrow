package storage

import (
	"database/sql"
	"fmt"
	"time"
)

// Task queries

func (d *DB) CreateTask(title, description, specHash string) (*Task, error) {
	res, err := d.db.Exec(
		`INSERT INTO tasks (title, description, spec_hash) VALUES (?, ?, ?)`,
		title, description, specHash,
	)
	if err != nil {
		return nil, fmt.Errorf("insert task: %w", err)
	}
	id, _ := res.LastInsertId()
	return d.GetTask(id)
}

func (d *DB) GetTask(id int64) (*Task, error) {
	t := &Task{}
	var createdAt string
	err := d.db.QueryRow(
		`SELECT id, title, description, spec_hash, created_at FROM tasks WHERE id = ?`, id,
	).Scan(&t.ID, &t.Title, &t.Description, &t.SpecHash, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	t.CreatedAt, err = time.Parse("2006-01-02 15:04:05", createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at in GetTask: %w", err)
	}
	return t, nil
}

// Escrow queries

func (d *DB) CreateEscrow(e *Escrow) (*Escrow, error) {
	res, err := d.db.Exec(
		`INSERT INTO escrows (task_id, chain_id, factory_address, escrow_address, escrow_id, buyer, worker, verifier, arbitrator, amount, status, submission_deadline, review_period_seconds, dispute_period_seconds, arbitrator_timeout_seconds)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.TaskID, e.ChainID, e.FactoryAddress, e.EscrowAddress, e.EscrowID,
		e.Buyer, e.Worker, e.Verifier, e.Arbitrator, e.Amount, e.Status,
		e.SubmissionDeadline, e.ReviewPeriodSeconds, e.DisputePeriodSeconds, e.ArbitratorTimeoutSeconds,
	)
	if err != nil {
		return nil, fmt.Errorf("insert escrow: %w", err)
	}
	id, _ := res.LastInsertId()
	return d.GetEscrow(id)
}

func (d *DB) GetEscrow(id int64) (*Escrow, error) {
	e := &Escrow{}
	var createdAt, updatedAt string
	err := d.db.QueryRow(
		`SELECT id, task_id, chain_id, factory_address, escrow_address, escrow_id, buyer, worker, verifier, arbitrator, amount, status, submission_deadline, review_period_seconds, dispute_period_seconds, arbitrator_timeout_seconds, created_at, updated_at
		 FROM escrows WHERE id = ?`, id,
	).Scan(&e.ID, &e.TaskID, &e.ChainID, &e.FactoryAddress, &e.EscrowAddress, &e.EscrowID,
		&e.Buyer, &e.Worker, &e.Verifier, &e.Arbitrator, &e.Amount, &e.Status,
		&e.SubmissionDeadline, &e.ReviewPeriodSeconds, &e.DisputePeriodSeconds, &e.ArbitratorTimeoutSeconds,
		&createdAt, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("get escrow: %w", err)
	}
	e.CreatedAt, err = time.Parse("2006-01-02 15:04:05", createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at in GetEscrow: %w", err)
	}
	e.UpdatedAt, err = time.Parse("2006-01-02 15:04:05", updatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse updated_at in GetEscrow: %w", err)
	}
	return e, nil
}

func (d *DB) GetEscrowByAddress(addr string) (*Escrow, error) {
	e := &Escrow{}
	var createdAt, updatedAt string
	err := d.db.QueryRow(
		`SELECT id, task_id, chain_id, factory_address, escrow_address, escrow_id, buyer, worker, verifier, arbitrator, amount, status, submission_deadline, review_period_seconds, dispute_period_seconds, arbitrator_timeout_seconds, created_at, updated_at
		 FROM escrows WHERE escrow_address = ?`, addr,
	).Scan(&e.ID, &e.TaskID, &e.ChainID, &e.FactoryAddress, &e.EscrowAddress, &e.EscrowID,
		&e.Buyer, &e.Worker, &e.Verifier, &e.Arbitrator, &e.Amount, &e.Status,
		&e.SubmissionDeadline, &e.ReviewPeriodSeconds, &e.DisputePeriodSeconds, &e.ArbitratorTimeoutSeconds,
		&createdAt, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("get escrow by address: %w", err)
	}
	e.CreatedAt, err = time.Parse("2006-01-02 15:04:05", createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at in GetEscrowByAddress: %w", err)
	}
	e.UpdatedAt, err = time.Parse("2006-01-02 15:04:05", updatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse updated_at in GetEscrowByAddress: %w", err)
	}
	return e, nil
}

func (d *DB) UpdateEscrowStatus(id int64, status string) error {
	_, err := d.db.Exec(
		`UPDATE escrows SET status = ?, updated_at = datetime('now') WHERE id = ?`,
		status, id,
	)
	return err
}

// UpdateEscrowOnChainFields sets the on-chain address and ID after the creation tx is mined.
func (d *DB) UpdateEscrowOnChainFields(id int64, escrowAddress string, escrowID int64) error {
	_, err := d.db.Exec(
		`UPDATE escrows SET escrow_address = ?, escrow_id = ?, updated_at = datetime('now') WHERE id = ?`,
		escrowAddress, escrowID, id,
	)
	return err
}

func (d *DB) ListEscrows(role, address, status string) ([]*Escrow, error) {
	query := `SELECT id, task_id, chain_id, factory_address, escrow_address, escrow_id, buyer, worker, verifier, arbitrator, amount, status, submission_deadline, review_period_seconds, dispute_period_seconds, arbitrator_timeout_seconds, created_at, updated_at FROM escrows WHERE 1=1`
	var args []any

	if role != "" && address != "" {
		switch role {
		case "buyer":
			query += ` AND buyer = ?`
		case "worker":
			query += ` AND worker = ?`
		case "verifier":
			query += ` AND verifier = ?`
		case "arbitrator":
			query += ` AND arbitrator = ?`
		default:
			return nil, fmt.Errorf("invalid role: %s", role)
		}
		args = append(args, address)
	}

	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}

	query += ` ORDER BY id DESC`

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list escrows: %w", err)
	}
	defer rows.Close()

	var escrows []*Escrow
	for rows.Next() {
		e := &Escrow{}
		var createdAt, updatedAt string
		if err := rows.Scan(&e.ID, &e.TaskID, &e.ChainID, &e.FactoryAddress, &e.EscrowAddress, &e.EscrowID,
			&e.Buyer, &e.Worker, &e.Verifier, &e.Arbitrator, &e.Amount, &e.Status,
			&e.SubmissionDeadline, &e.ReviewPeriodSeconds, &e.DisputePeriodSeconds, &e.ArbitratorTimeoutSeconds,
			&createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan escrow: %w", err)
		}
		e.CreatedAt, err = time.Parse("2006-01-02 15:04:05", createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse created_at in ListEscrows: %w", err)
		}
		e.UpdatedAt, err = time.Parse("2006-01-02 15:04:05", updatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse updated_at in ListEscrows: %w", err)
		}
		escrows = append(escrows, e)
	}
	return escrows, nil
}

// Submission queries

func (d *DB) CreateSubmission(escrowID int64, submissionHash, submissionURI string) (*Submission, error) {
	res, err := d.db.Exec(
		`INSERT INTO submissions (escrow_id, submission_hash, submission_uri) VALUES (?, ?, ?)`,
		escrowID, submissionHash, submissionURI,
	)
	if err != nil {
		return nil, fmt.Errorf("insert submission: %w", err)
	}
	id, _ := res.LastInsertId()
	s := &Submission{}
	var submittedAt string
	err = d.db.QueryRow(
		`SELECT id, escrow_id, submission_hash, submission_uri, submitted_at FROM submissions WHERE id = ?`, id,
	).Scan(&s.ID, &s.EscrowID, &s.SubmissionHash, &s.SubmissionURI, &submittedAt)
	if err != nil {
		return nil, fmt.Errorf("get submission: %w", err)
	}
	s.SubmittedAt, err = time.Parse("2006-01-02 15:04:05", submittedAt)
	if err != nil {
		return nil, fmt.Errorf("parse submitted_at in CreateSubmission: %w", err)
	}
	return s, nil
}

func (d *DB) GetSubmissionsByEscrow(escrowID int64) ([]*Submission, error) {
	rows, err := d.db.Query(
		`SELECT id, escrow_id, submission_hash, submission_uri, submitted_at FROM submissions WHERE escrow_id = ? ORDER BY id`, escrowID,
	)
	if err != nil {
		return nil, fmt.Errorf("list submissions: %w", err)
	}
	defer rows.Close()

	var subs []*Submission
	for rows.Next() {
		s := &Submission{}
		var submittedAt string
		if err := rows.Scan(&s.ID, &s.EscrowID, &s.SubmissionHash, &s.SubmissionURI, &submittedAt); err != nil {
			return nil, fmt.Errorf("scan submission: %w", err)
		}
		s.SubmittedAt, err = time.Parse("2006-01-02 15:04:05", submittedAt)
		if err != nil {
			return nil, fmt.Errorf("parse submitted_at in GetSubmissionsByEscrow: %w", err)
		}
		subs = append(subs, s)
	}
	return subs, nil
}

// Dispute queries

func (d *DB) CreateDispute(escrowID int64, raisedBy, reasonURI string) (*Dispute, error) {
	res, err := d.db.Exec(
		`INSERT INTO disputes (escrow_id, raised_by, reason_uri) VALUES (?, ?, ?)`,
		escrowID, raisedBy, reasonURI,
	)
	if err != nil {
		return nil, fmt.Errorf("insert dispute: %w", err)
	}
	id, _ := res.LastInsertId()
	return d.getDispute(id)
}

func (d *DB) UpdateDispute(id int64, resolutionURI string, workerAwardBps int) error {
	_, err := d.db.Exec(
		`UPDATE disputes SET resolution_uri = ?, worker_award_bps = ?, status = 'resolved', resolved_at = datetime('now') WHERE id = ?`,
		resolutionURI, workerAwardBps, id,
	)
	return err
}

func (d *DB) getDispute(id int64) (*Dispute, error) {
	disp := &Dispute{}
	var createdAt string
	var resolvedAt sql.NullString
	var nullBps sql.NullInt64
	err := d.db.QueryRow(
		`SELECT id, escrow_id, raised_by, reason_uri, resolution_uri, worker_award_bps, status, created_at, resolved_at FROM disputes WHERE id = ?`, id,
	).Scan(&disp.ID, &disp.EscrowID, &disp.RaisedBy, &disp.ReasonURI, &disp.ResolutionURI, &nullBps, &disp.Status, &createdAt, &resolvedAt)
	if err != nil {
		return nil, fmt.Errorf("get dispute: %w", err)
	}
	if nullBps.Valid {
		v := int(nullBps.Int64)
		disp.WorkerAwardBps = &v
	}
	disp.CreatedAt, err = time.Parse("2006-01-02 15:04:05", createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at in getDispute: %w", err)
	}
	if resolvedAt.Valid {
		t, err := time.Parse("2006-01-02 15:04:05", resolvedAt.String)
		if err != nil {
			return nil, fmt.Errorf("parse resolved_at in getDispute: %w", err)
		}
		disp.ResolvedAt = &t
	}
	return disp, nil
}

// Chain log queries

func (d *DB) CreateChainLog(txHash string, logIndex int, blockNumber int64, eventName, contractAddress, rawData string) error {
	_, err := d.db.Exec(
		`INSERT OR IGNORE INTO chain_logs (tx_hash, log_index, block_number, event_name, contract_address, raw_data) VALUES (?, ?, ?, ?, ?, ?)`,
		txHash, logIndex, blockNumber, eventName, contractAddress, rawData,
	)
	return err
}

func (d *DB) ChainLogExists(txHash string, logIndex int) (bool, error) {
	var count int
	err := d.db.QueryRow(
		`SELECT COUNT(*) FROM chain_logs WHERE tx_hash = ? AND log_index = ?`, txHash, logIndex,
	).Scan(&count)
	return count > 0, err
}

// Cursor queries

func (d *DB) GetCursor(chainID int64, cursorKey string) (int64, error) {
	var blockNumber int64
	err := d.db.QueryRow(
		`SELECT block_number FROM chain_cursors WHERE chain_id = ? AND cursor_key = ?`, chainID, cursorKey,
	).Scan(&blockNumber)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return blockNumber, err
}

func (d *DB) SetCursor(chainID int64, cursorKey string, blockNumber int64) error {
	_, err := d.db.Exec(
		`INSERT INTO chain_cursors (chain_id, cursor_key, block_number)
		 VALUES (?, ?, ?)
		 ON CONFLICT(chain_id, cursor_key)
		 DO UPDATE SET block_number = excluded.block_number, updated_at = datetime('now')`,
		chainID, cursorKey, blockNumber,
	)
	return err
}
