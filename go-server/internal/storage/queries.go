package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// dbExecer is satisfied by both *sql.DB and *sql.Tx, allowing shared query helpers
// to run inside or outside a transaction.
type dbExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Task queries

func createTaskOn(ctx context.Context, q dbExecer, title, description, specHash string) (*Task, error) {
	res, err := q.ExecContext(ctx,
		`INSERT INTO tasks (title, description, spec_hash) VALUES (?, ?, ?)`,
		title, description, specHash,
	)
	if err != nil {
		return nil, fmt.Errorf("insert task: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	t := &Task{}
	var createdAt string
	err = q.QueryRowContext(ctx,
		`SELECT id, title, description, spec_hash, created_at FROM tasks WHERE id = ?`, id,
	).Scan(&t.ID, &t.Title, &t.Description, &t.SpecHash, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	t.CreatedAt, err = parseSQLiteTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at in CreateTask: %w", err)
	}
	return t, nil
}

func (d *DB) CreateTask(ctx context.Context, title, description, specHash string) (*Task, error) {
	return createTaskOn(ctx, d.db, title, description, specHash)
}

func (d *DB) CreateTaskTx(ctx context.Context, tx *sql.Tx, title, description, specHash string) (*Task, error) {
	return createTaskOn(ctx, tx, title, description, specHash)
}

func (d *DB) GetTask(ctx context.Context, id int64) (*Task, error) {
	t := &Task{}
	var createdAt string
	err := d.db.QueryRowContext(ctx,
		`SELECT id, title, description, spec_hash, created_at FROM tasks WHERE id = ?`, id,
	).Scan(&t.ID, &t.Title, &t.Description, &t.SpecHash, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	t.CreatedAt, err = parseSQLiteTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at in GetTask: %w", err)
	}
	return t, nil
}

// Escrow queries

func createEscrowOn(ctx context.Context, q dbExecer, e *Escrow) (*Escrow, error) {
	msCount := e.MilestoneCount
	if msCount == 0 {
		msCount = 1
	}
	activeWorker := e.ActiveWorker
	if activeWorker == "" {
		activeWorker = e.Worker
	}
	res, err := q.ExecContext(ctx,
		`INSERT INTO escrows (task_id, chain_id, factory_address, escrow_address, escrow_id, buyer, worker, verifier, arbitrator, amount, worker_stake, token, status, submission_deadline, review_period_seconds, dispute_period_seconds, arbitrator_timeout_seconds, milestone_count, current_milestone, backup_worker, backup_deadline_extension, active_worker, backup_activated)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.TaskID, e.ChainID, e.FactoryAddress, e.EscrowAddress, e.EscrowID,
		e.Buyer, e.Worker, e.Verifier, e.Arbitrator, e.Amount, e.WorkerStake, e.Token, e.Status,
		e.SubmissionDeadline, e.ReviewPeriodSeconds, e.DisputePeriodSeconds, e.ArbitratorTimeoutSeconds,
		msCount, e.CurrentMilestone,
		e.BackupWorker, e.BackupDeadlineExtension, activeWorker, boolToInt(e.BackupActivated),
	)
	if err != nil {
		return nil, fmt.Errorf("insert escrow: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	row := q.QueryRowContext(ctx, `SELECT `+escrowColumns+` FROM escrows WHERE id = ?`, id)
	out, err := scanEscrow(row)
	if err != nil {
		return nil, fmt.Errorf("get escrow: %w", err)
	}
	return out, nil
}

func (d *DB) CreateEscrow(ctx context.Context, e *Escrow) (*Escrow, error) {
	return createEscrowOn(ctx, d.db, e)
}

func (d *DB) CreateEscrowTx(ctx context.Context, tx *sql.Tx, e *Escrow) (*Escrow, error) {
	return createEscrowOn(ctx, tx, e)
}

const escrowColumns = `id, task_id, chain_id, factory_address, escrow_address, escrow_id, buyer, worker, verifier, arbitrator, amount, worker_stake, token, status, submission_deadline, review_period_seconds, dispute_period_seconds, arbitrator_timeout_seconds, milestone_count, current_milestone, backup_worker, backup_deadline_extension, active_worker, backup_activated, created_at, updated_at`

func scanEscrow(scanner interface{ Scan(...any) error }) (*Escrow, error) {
	e := &Escrow{}
	var createdAt, updatedAt string
	var backupActivatedInt int
	err := scanner.Scan(&e.ID, &e.TaskID, &e.ChainID, &e.FactoryAddress, &e.EscrowAddress, &e.EscrowID,
		&e.Buyer, &e.Worker, &e.Verifier, &e.Arbitrator, &e.Amount, &e.WorkerStake, &e.Token, &e.Status,
		&e.SubmissionDeadline, &e.ReviewPeriodSeconds, &e.DisputePeriodSeconds, &e.ArbitratorTimeoutSeconds,
		&e.MilestoneCount, &e.CurrentMilestone,
		&e.BackupWorker, &e.BackupDeadlineExtension, &e.ActiveWorker, &backupActivatedInt,
		&createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	e.BackupActivated = backupActivatedInt != 0
	e.CreatedAt, err = parseSQLiteTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	e.UpdatedAt, err = parseSQLiteTime(updatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse updated_at: %w", err)
	}
	return e, nil
}

func (d *DB) GetEscrow(ctx context.Context, id int64) (*Escrow, error) {
	row := d.db.QueryRowContext(ctx, `SELECT `+escrowColumns+` FROM escrows WHERE id = ?`, id)
	e, err := scanEscrow(row)
	if err != nil {
		return nil, fmt.Errorf("get escrow: %w", err)
	}
	return e, nil
}

func (d *DB) GetEscrowByAddress(ctx context.Context, addr string) (*Escrow, error) {
	row := d.db.QueryRowContext(ctx, `SELECT `+escrowColumns+` FROM escrows WHERE escrow_address = ?`, addr)
	e, err := scanEscrow(row)
	if err != nil {
		return nil, fmt.Errorf("get escrow by address: %w", err)
	}
	return e, nil
}

func (d *DB) UpdateEscrowStatus(ctx context.Context, id int64, status string) error {
	_, err := d.db.ExecContext(ctx,
		`UPDATE escrows SET status = ?, updated_at = datetime('now') WHERE id = ?`,
		status, id,
	)
	if err != nil {
		return fmt.Errorf("UpdateEscrowStatus: %w", err)
	}
	return nil
}

// UpdateEscrowOnChainFields sets the on-chain address and ID after the creation tx is mined.
func (d *DB) UpdateEscrowOnChainFields(ctx context.Context, id int64, escrowAddress string, escrowID int64) error {
	_, err := d.db.ExecContext(ctx,
		`UPDATE escrows SET escrow_address = ?, escrow_id = ?, updated_at = datetime('now') WHERE id = ?`,
		escrowAddress, escrowID, id,
	)
	if err != nil {
		return fmt.Errorf("UpdateEscrowOnChainFields: %w", err)
	}
	return nil
}

func (d *DB) ListEscrows(ctx context.Context, role, address, status string) ([]*Escrow, error) {
	query := `SELECT ` + escrowColumns + ` FROM escrows WHERE 1=1`
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
	return d.queryEscrows(ctx, query, args...)
}

func (d *DB) ListEscrowsByChainID(ctx context.Context, chainID int64) ([]*Escrow, error) {
	return d.queryEscrows(ctx, `SELECT `+escrowColumns+` FROM escrows WHERE chain_id = ? ORDER BY id DESC`, chainID)
}

func (d *DB) queryEscrows(ctx context.Context, query string, args ...any) ([]*Escrow, error) {
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query escrows: %w", err)
	}
	defer rows.Close()

	var escrows []*Escrow
	for rows.Next() {
		e, err := scanEscrow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan escrow: %w", err)
		}
		escrows = append(escrows, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate escrows: %w", err)
	}
	return escrows, nil
}

func (d *DB) UpdateEscrowMilestoneProgress(ctx context.Context, id int64, currentMilestone int) error {
	res, err := d.db.ExecContext(ctx,
		`UPDATE escrows SET current_milestone = ?, updated_at = datetime('now') WHERE id = ?`,
		currentMilestone, id,
	)
	if err != nil {
		return fmt.Errorf("UpdateEscrowMilestoneProgress: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("UpdateEscrowMilestoneProgress rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("UpdateEscrowMilestoneProgress id=%d: %w", id, sql.ErrNoRows)
	}
	return nil
}

// Submission queries

func (d *DB) CreateSubmission(ctx context.Context, escrowID int64, submissionHash, submissionURI string) (*Submission, error) {
	res, err := d.db.ExecContext(ctx,
		`INSERT INTO submissions (escrow_id, submission_hash, submission_uri) VALUES (?, ?, ?)`,
		escrowID, submissionHash, submissionURI,
	)
	if err != nil {
		return nil, fmt.Errorf("insert submission: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	s := &Submission{}
	var submittedAt string
	err = d.db.QueryRowContext(ctx,
		`SELECT id, escrow_id, submission_hash, submission_uri, submitted_at FROM submissions WHERE id = ?`, id,
	).Scan(&s.ID, &s.EscrowID, &s.SubmissionHash, &s.SubmissionURI, &submittedAt)
	if err != nil {
		return nil, fmt.Errorf("get submission: %w", err)
	}
	s.SubmittedAt, err = parseSQLiteTime(submittedAt)
	if err != nil {
		return nil, fmt.Errorf("parse submitted_at in CreateSubmission: %w", err)
	}
	return s, nil
}

func (d *DB) GetSubmissionsByEscrow(ctx context.Context, escrowID int64) ([]*Submission, error) {
	rows, err := d.db.QueryContext(ctx,
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
		s.SubmittedAt, err = parseSQLiteTime(submittedAt)
		if err != nil {
			return nil, fmt.Errorf("parse submitted_at in GetSubmissionsByEscrow: %w", err)
		}
		subs = append(subs, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate submissions: %w", err)
	}
	return subs, nil
}

// Dispute queries

func (d *DB) CreateDispute(ctx context.Context, escrowID int64, raisedBy, reasonURI string) (*Dispute, error) {
	res, err := d.db.ExecContext(ctx,
		`INSERT INTO disputes (escrow_id, raised_by, reason_uri) VALUES (?, ?, ?)`,
		escrowID, raisedBy, reasonURI,
	)
	if err != nil {
		return nil, fmt.Errorf("insert dispute: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	return d.getDispute(ctx, id)
}

func (d *DB) UpdateDispute(ctx context.Context, id int64, resolutionURI string, workerAwardBps int) error {
	_, err := d.db.ExecContext(ctx,
		`UPDATE disputes SET resolution_uri = ?, worker_award_bps = ?, status = 'resolved', resolved_at = datetime('now') WHERE id = ?`,
		resolutionURI, workerAwardBps, id,
	)
	return err
}

func (d *DB) GetDispute(ctx context.Context, id int64) (*Dispute, error) {
	return d.getDispute(ctx, id)
}

// GetDisputeByEscrowID returns the most recent open (non-resolved) dispute for the given escrow.
func (d *DB) GetDisputeByEscrowID(ctx context.Context, escrowID int64) (*Dispute, error) {
	var disputeID int64
	err := d.db.QueryRowContext(ctx,
		`SELECT id FROM disputes WHERE escrow_id = ? AND status != 'resolved' ORDER BY id DESC LIMIT 1`, escrowID,
	).Scan(&disputeID)
	if err != nil {
		return nil, fmt.Errorf("get dispute by escrow id: %w", err)
	}
	return d.getDispute(ctx, disputeID)
}

func (d *DB) getDispute(ctx context.Context, id int64) (*Dispute, error) {
	disp := &Dispute{}
	var createdAt string
	var resolvedAt sql.NullString
	var nullBps sql.NullInt64
	err := d.db.QueryRowContext(ctx,
		`SELECT id, escrow_id, raised_by, reason_uri, resolution_uri, worker_award_bps, status, created_at, resolved_at FROM disputes WHERE id = ?`, id,
	).Scan(&disp.ID, &disp.EscrowID, &disp.RaisedBy, &disp.ReasonURI, &disp.ResolutionURI, &nullBps, &disp.Status, &createdAt, &resolvedAt)
	if err != nil {
		return nil, fmt.Errorf("get dispute: %w", err)
	}
	if nullBps.Valid {
		v := int(nullBps.Int64)
		disp.WorkerAwardBps = &v
	}
	disp.CreatedAt, err = parseSQLiteTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at in getDispute: %w", err)
	}
	if resolvedAt.Valid {
		t, err := parseSQLiteTime(resolvedAt.String)
		if err != nil {
			return nil, fmt.Errorf("parse resolved_at in getDispute: %w", err)
		}
		disp.ResolvedAt = &t
	}
	return disp, nil
}

// Reputation queries

func (d *DB) GetReputation(ctx context.Context, address, role string) (*Reputation, error) {
	r := &Reputation{}
	var updatedAt string
	err := d.db.QueryRowContext(ctx,
		`SELECT id, address, role, completed, disputed, failed, updated_at FROM reputation WHERE address = ? AND role = ?`,
		address, role,
	).Scan(&r.ID, &r.Address, &r.Role, &r.Completed, &r.Disputed, &r.Failed, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("get reputation: %w", err)
	}
	r.UpdatedAt, err = parseSQLiteTime(updatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse reputation updated_at: %w", err)
	}
	return r, nil
}

func (d *DB) GetReputationByAddress(ctx context.Context, address string) ([]*Reputation, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, address, role, completed, disputed, failed, updated_at FROM reputation WHERE address = ?`,
		address,
	)
	if err != nil {
		return nil, fmt.Errorf("get reputation by address: %w", err)
	}
	defer rows.Close()

	var reps []*Reputation
	for rows.Next() {
		r := &Reputation{}
		var updatedAt string
		if err := rows.Scan(&r.ID, &r.Address, &r.Role, &r.Completed, &r.Disputed, &r.Failed, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan reputation: %w", err)
		}
		var err error
		r.UpdatedAt, err = parseSQLiteTime(updatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse reputation updated_at: %w", err)
		}
		reps = append(reps, r)
	}
	return reps, rows.Err()
}

func (d *DB) UpsertReputation(ctx context.Context, address, role, outcome string) error {
	var col string
	switch outcome {
	case "completed":
		col = "completed"
	case "disputed":
		col = "disputed"
	case "failed":
		col = "failed"
	default:
		return fmt.Errorf("invalid outcome: %s", outcome)
	}

	query := fmt.Sprintf( //nolint:gosec // col is from a hardcoded switch, not user input
		`INSERT INTO reputation (address, role, %s) VALUES (?, ?, 1)
		 ON CONFLICT(address, role)
		 DO UPDATE SET %s = %s + 1, updated_at = datetime('now')`,
		col, col, col,
	)
	_, err := d.db.ExecContext(ctx, query, address, role)
	if err != nil {
		return fmt.Errorf("upsert reputation: %w", err)
	}
	return nil
}

func (d *DB) ListReputations(ctx context.Context, minCompleted int) ([]*Reputation, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, address, role, completed, disputed, failed, updated_at FROM reputation WHERE completed >= ? ORDER BY completed DESC`,
		minCompleted,
	)
	if err != nil {
		return nil, fmt.Errorf("list reputations: %w", err)
	}
	defer rows.Close()

	var reps []*Reputation
	for rows.Next() {
		r := &Reputation{}
		var updatedAt string
		if err := rows.Scan(&r.ID, &r.Address, &r.Role, &r.Completed, &r.Disputed, &r.Failed, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan reputation: %w", err)
		}
		var err error
		r.UpdatedAt, err = parseSQLiteTime(updatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse reputation updated_at: %w", err)
		}
		reps = append(reps, r)
	}
	return reps, rows.Err()
}

// RFQ queries

const rfqColumns = `id, title, description, spec_hash, buyer, token, budget_min, budget_max,
	deadline, review_period_seconds, dispute_period_seconds, arbitrator_timeout_seconds,
	verifier, arbitrator, worker_stake, milestones_json, requirements_json,
	status, expires_at, created_at, updated_at`

func scanRFQ(scanner interface{ Scan(...any) error }) (*RFQ, error) {
	r := &RFQ{}
	var createdAt, updatedAt string
	err := scanner.Scan(&r.ID, &r.Title, &r.Description, &r.SpecHash, &r.Buyer, &r.Token,
		&r.BudgetMin, &r.BudgetMax, &r.Deadline, &r.ReviewPeriodSeconds,
		&r.DisputePeriodSeconds, &r.ArbitratorTimeoutSeconds,
		&r.Verifier, &r.Arbitrator, &r.WorkerStake, &r.MilestonesJSON, &r.RequirementsJSON,
		&r.Status, &r.ExpiresAt, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	r.CreatedAt, err = parseSQLiteTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse rfq created_at: %w", err)
	}
	r.UpdatedAt, err = parseSQLiteTime(updatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse rfq updated_at: %w", err)
	}
	return r, nil
}

func (d *DB) CreateRFQ(ctx context.Context, r *RFQ) (*RFQ, error) {
	res, err := d.db.ExecContext(ctx,
		`INSERT INTO rfqs (title, description, spec_hash, buyer, token, budget_min, budget_max,
			deadline, review_period_seconds, dispute_period_seconds, arbitrator_timeout_seconds,
			verifier, arbitrator, worker_stake, milestones_json, requirements_json,
			status, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Title, r.Description, r.SpecHash, r.Buyer, r.Token, r.BudgetMin, r.BudgetMax,
		r.Deadline, r.ReviewPeriodSeconds, r.DisputePeriodSeconds, r.ArbitratorTimeoutSeconds,
		r.Verifier, r.Arbitrator, r.WorkerStake, r.MilestonesJSON, r.RequirementsJSON,
		r.Status, r.ExpiresAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert rfq: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	return d.GetRFQ(ctx, id)
}

func (d *DB) GetRFQ(ctx context.Context, id int64) (*RFQ, error) {
	row := d.db.QueryRowContext(ctx, `SELECT `+rfqColumns+` FROM rfqs WHERE id = ?`, id)
	r, err := scanRFQ(row)
	if err != nil {
		return nil, fmt.Errorf("get rfq: %w", err)
	}
	return r, nil
}

func (d *DB) ListRFQs(ctx context.Context, status, buyer string) ([]*RFQ, error) {
	query := `SELECT ` + rfqColumns + ` FROM rfqs WHERE 1=1`
	var args []any

	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	if buyer != "" {
		query += ` AND buyer = ?`
		args = append(args, buyer)
	}

	query += ` ORDER BY id DESC`

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list rfqs: %w", err)
	}
	defer rows.Close()

	var rfqs []*RFQ
	for rows.Next() {
		r, err := scanRFQ(rows)
		if err != nil {
			return nil, fmt.Errorf("scan rfq: %w", err)
		}
		rfqs = append(rfqs, r)
	}
	return rfqs, rows.Err()
}

func updateRFQStatusOn(ctx context.Context, q dbExecer, id int64, status string) error {
	res, err := q.ExecContext(ctx,
		`UPDATE rfqs SET status = ?, updated_at = datetime('now') WHERE id = ?`,
		status, id,
	)
	if err != nil {
		return fmt.Errorf("UpdateRFQStatus: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("UpdateRFQStatus rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("UpdateRFQStatus id=%d: %w", id, sql.ErrNoRows)
	}
	return nil
}

func (d *DB) UpdateRFQStatus(ctx context.Context, id int64, status string) error {
	return updateRFQStatusOn(ctx, d.db, id, status)
}

func (d *DB) UpdateRFQStatusTx(ctx context.Context, tx *sql.Tx, id int64, status string) error {
	return updateRFQStatusOn(ctx, tx, id, status)
}

// Bid queries

const bidColumns = `id, rfq_id, bidder, amount, estimated_duration, reputation_bond,
	milestones_json, message, status, escrow_id, expires_at, stake_mandate_id, created_at, updated_at`

func scanBid(scanner interface{ Scan(...any) error }) (*Bid, error) {
	b := &Bid{}
	var createdAt, updatedAt string
	var escrowID sql.NullInt64
	var stakeMandateID sql.NullString
	err := scanner.Scan(&b.ID, &b.RFQID, &b.Bidder, &b.Amount, &b.EstimatedDuration,
		&b.ReputationBond, &b.MilestonesJSON, &b.Message, &b.Status,
		&escrowID, &b.ExpiresAt, &stakeMandateID, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	if escrowID.Valid {
		v := escrowID.Int64
		b.EscrowID = &v
	}
	if stakeMandateID.Valid {
		b.StakeMandateID = stakeMandateID.String
	}
	b.CreatedAt, err = parseSQLiteTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse bid created_at: %w", err)
	}
	b.UpdatedAt, err = parseSQLiteTime(updatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse bid updated_at: %w", err)
	}
	return b, nil
}

func (d *DB) CreateBid(ctx context.Context, b *Bid) (*Bid, error) {
	res, err := d.db.ExecContext(ctx,
		`INSERT INTO bids (rfq_id, bidder, amount, estimated_duration, reputation_bond,
			milestones_json, message, status, expires_at, stake_mandate_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.RFQID, b.Bidder, b.Amount, b.EstimatedDuration, b.ReputationBond,
		b.MilestonesJSON, b.Message, b.Status, b.ExpiresAt, nilIfEmpty(b.StakeMandateID),
	)
	if err != nil {
		return nil, fmt.Errorf("insert bid: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	return d.GetBid(ctx, id)
}

func (d *DB) GetBid(ctx context.Context, id int64) (*Bid, error) {
	row := d.db.QueryRowContext(ctx, `SELECT `+bidColumns+` FROM bids WHERE id = ?`, id)
	b, err := scanBid(row)
	if err != nil {
		return nil, fmt.Errorf("get bid: %w", err)
	}
	return b, nil
}

func (d *DB) ListBidsByRFQ(ctx context.Context, rfqID int64) ([]*Bid, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT `+bidColumns+` FROM bids WHERE rfq_id = ? ORDER BY id DESC`, rfqID,
	)
	if err != nil {
		return nil, fmt.Errorf("list bids by rfq: %w", err)
	}
	defer rows.Close()

	var bids []*Bid
	for rows.Next() {
		b, err := scanBid(rows)
		if err != nil {
			return nil, fmt.Errorf("scan bid: %w", err)
		}
		bids = append(bids, b)
	}
	return bids, rows.Err()
}

func (d *DB) ListBidsByBidder(ctx context.Context, bidder string) ([]*Bid, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT `+bidColumns+` FROM bids WHERE bidder = ? ORDER BY id DESC`, bidder,
	)
	if err != nil {
		return nil, fmt.Errorf("list bids by bidder: %w", err)
	}
	defer rows.Close()

	var bids []*Bid
	for rows.Next() {
		b, err := scanBid(rows)
		if err != nil {
			return nil, fmt.Errorf("scan bid: %w", err)
		}
		bids = append(bids, b)
	}
	return bids, rows.Err()
}

func (d *DB) UpdateBidStatus(ctx context.Context, id int64, status string) error {
	res, err := d.db.ExecContext(ctx,
		`UPDATE bids SET status = ?, updated_at = datetime('now') WHERE id = ?`,
		status, id,
	)
	if err != nil {
		return fmt.Errorf("UpdateBidStatus: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("UpdateBidStatus rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("UpdateBidStatus id=%d: %w", id, sql.ErrNoRows)
	}
	return nil
}

func acceptBidOn(ctx context.Context, q dbExecer, bidID, escrowID int64) error {
	res, err := q.ExecContext(ctx,
		`UPDATE bids SET status = 'accepted', escrow_id = ?, updated_at = datetime('now') WHERE id = ? AND status = 'pending'`,
		escrowID, bidID,
	)
	if err != nil {
		return fmt.Errorf("AcceptBid: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("AcceptBid rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("AcceptBid bid=%d: not pending or does not exist: %w", bidID, sql.ErrNoRows)
	}
	return nil
}

func (d *DB) AcceptBid(ctx context.Context, bidID, escrowID int64) error {
	return acceptBidOn(ctx, d.db, bidID, escrowID)
}

func (d *DB) AcceptBidTx(ctx context.Context, tx *sql.Tx, bidID, escrowID int64) error {
	return acceptBidOn(ctx, tx, bidID, escrowID)
}

// RejectPendingBids sets all pending bids on an RFQ to rejected, except the given bid.
func rejectPendingBidsOn(ctx context.Context, q dbExecer, rfqID, exceptBidID int64) error {
	_, err := q.ExecContext(ctx,
		`UPDATE bids SET status = 'rejected', updated_at = datetime('now')
		 WHERE rfq_id = ? AND id != ? AND status = 'pending'`,
		rfqID, exceptBidID,
	)
	if err != nil {
		return fmt.Errorf("RejectPendingBids: %w", err)
	}
	return nil
}

func (d *DB) RejectPendingBids(ctx context.Context, rfqID, exceptBidID int64) error {
	return rejectPendingBidsOn(ctx, d.db, rfqID, exceptBidID)
}

func (d *DB) RejectPendingBidsTx(ctx context.Context, tx *sql.Tx, rfqID, exceptBidID int64) error {
	return rejectPendingBidsOn(ctx, tx, rfqID, exceptBidID)
}

// Chain log queries

func (d *DB) CreateChainLog(ctx context.Context, txHash string, logIndex int, blockNumber int64, eventName, contractAddress, rawData string) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO chain_logs (tx_hash, log_index, block_number, event_name, contract_address, raw_data) VALUES (?, ?, ?, ?, ?, ?)`,
		txHash, logIndex, blockNumber, eventName, contractAddress, rawData,
	)
	return err
}

func (d *DB) ChainLogExists(ctx context.Context, txHash string, logIndex int) (bool, error) {
	var count int
	err := d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM chain_logs WHERE tx_hash = ? AND log_index = ?`, txHash, logIndex,
	).Scan(&count)
	return count > 0, err
}

// Cursor queries

func (d *DB) GetCursor(ctx context.Context, chainID int64, cursorKey string) (int64, error) {
	var blockNumber int64
	err := d.db.QueryRowContext(ctx,
		`SELECT block_number FROM chain_cursors WHERE chain_id = ? AND cursor_key = ?`, chainID, cursorKey,
	).Scan(&blockNumber)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return blockNumber, err
}

func (d *DB) SetCursor(ctx context.Context, chainID int64, cursorKey string, blockNumber int64) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO chain_cursors (chain_id, cursor_key, block_number)
		 VALUES (?, ?, ?)
		 ON CONFLICT(chain_id, cursor_key)
		 DO UPDATE SET block_number = excluded.block_number, updated_at = datetime('now')`,
		chainID, cursorKey, blockNumber,
	)
	return err
}

// Milestone queries

func createMilestoneOn(ctx context.Context, q dbExecer, m *MilestoneRecord) (*MilestoneRecord, error) {
	res, err := q.ExecContext(ctx,
		`INSERT INTO milestones (escrow_id, milestone_index, amount, submission_deadline, status)
		 VALUES (?, ?, ?, ?, ?)`,
		m.EscrowID, m.MilestoneIndex, m.Amount, m.SubmissionDeadline, m.Status,
	)
	if err != nil {
		return nil, fmt.Errorf("insert milestone: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	row := q.QueryRowContext(ctx, `SELECT `+milestoneColumns+` FROM milestones WHERE id = ?`, id)
	out, err := scanMilestone(row)
	if err != nil {
		return nil, fmt.Errorf("get milestone: %w", err)
	}
	return out, nil
}

func (d *DB) CreateMilestone(ctx context.Context, m *MilestoneRecord) (*MilestoneRecord, error) {
	return createMilestoneOn(ctx, d.db, m)
}

func (d *DB) CreateMilestoneTx(ctx context.Context, tx *sql.Tx, m *MilestoneRecord) (*MilestoneRecord, error) {
	return createMilestoneOn(ctx, tx, m)
}

const milestoneColumns = `id, escrow_id, milestone_index, amount, submission_deadline, status,
        submission_hash, submission_uri, submitted_at, approved_at, disputed_at,
        dispute_reason_uri, created_at, updated_at`

func scanMilestone(scanner interface{ Scan(...any) error }) (*MilestoneRecord, error) {
	m := &MilestoneRecord{}
	var createdAt, updatedAt string
	var submittedAt, approvedAt, disputedAt sql.NullString
	err := scanner.Scan(&m.ID, &m.EscrowID, &m.MilestoneIndex, &m.Amount, &m.SubmissionDeadline, &m.Status,
		&m.SubmissionHash, &m.SubmissionURI, &submittedAt, &approvedAt, &disputedAt,
		&m.DisputeReasonURI, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	m.CreatedAt, err = parseSQLiteTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	m.UpdatedAt, err = parseSQLiteTime(updatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse updated_at: %w", err)
	}
	m.SubmittedAt, err = parseNullTime(submittedAt)
	if err != nil {
		return nil, fmt.Errorf("parse submitted_at: %w", err)
	}
	m.ApprovedAt, err = parseNullTime(approvedAt)
	if err != nil {
		return nil, fmt.Errorf("parse approved_at: %w", err)
	}
	m.DisputedAt, err = parseNullTime(disputedAt)
	if err != nil {
		return nil, fmt.Errorf("parse disputed_at: %w", err)
	}
	return m, nil
}

func (d *DB) GetMilestone(ctx context.Context, id int64) (*MilestoneRecord, error) {
	row := d.db.QueryRowContext(ctx, `SELECT `+milestoneColumns+` FROM milestones WHERE id = ?`, id)
	m, err := scanMilestone(row)
	if err != nil {
		return nil, fmt.Errorf("get milestone: %w", err)
	}
	return m, nil
}

func (d *DB) GetMilestonesByEscrow(ctx context.Context, escrowID int64) ([]*MilestoneRecord, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT `+milestoneColumns+` FROM milestones WHERE escrow_id = ? ORDER BY milestone_index`, escrowID,
	)
	if err != nil {
		return nil, fmt.Errorf("list milestones: %w", err)
	}
	defer rows.Close()

	var milestones []*MilestoneRecord
	for rows.Next() {
		m, err := scanMilestone(rows)
		if err != nil {
			return nil, fmt.Errorf("scan milestone: %w", err)
		}
		milestones = append(milestones, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list milestones: %w", err)
	}
	return milestones, nil
}

func (d *DB) UpdateMilestoneStatus(ctx context.Context, escrowID int64, milestoneIndex int, status string) error {
	res, err := d.db.ExecContext(ctx,
		`UPDATE milestones SET status = ?, updated_at = datetime('now') WHERE escrow_id = ? AND milestone_index = ?`,
		status, escrowID, milestoneIndex,
	)
	if err != nil {
		return fmt.Errorf("UpdateMilestoneStatus: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("UpdateMilestoneStatus rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("UpdateMilestoneStatus escrow_id=%d milestone_index=%d: %w", escrowID, milestoneIndex, sql.ErrNoRows)
	}
	return nil
}

func (d *DB) UpdateMilestoneSubmission(ctx context.Context, escrowID int64, milestoneIndex int, hash, uri string) error {
	res, err := d.db.ExecContext(ctx,
		`UPDATE milestones SET submission_hash = ?, submission_uri = ?, submitted_at = datetime('now'),
		        status = 'submitted', updated_at = datetime('now')
		 WHERE escrow_id = ? AND milestone_index = ?`,
		hash, uri, escrowID, milestoneIndex,
	)
	if err != nil {
		return fmt.Errorf("UpdateMilestoneSubmission: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("UpdateMilestoneSubmission rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("UpdateMilestoneSubmission escrow_id=%d milestone_index=%d: %w", escrowID, milestoneIndex, sql.ErrNoRows)
	}
	return nil
}

func (d *DB) UpdateEscrowBackupActivated(ctx context.Context, id int64, activeWorker string, newDeadline uint64) error {
	res, err := d.db.ExecContext(ctx,
		`UPDATE escrows SET active_worker = ?, backup_activated = 1, submission_deadline = ?, updated_at = datetime('now') WHERE id = ?`,
		activeWorker, newDeadline, id,
	)
	if err != nil {
		return fmt.Errorf("UpdateEscrowBackupActivated: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("UpdateEscrowBackupActivated rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("UpdateEscrowBackupActivated id=%d: %w", id, sql.ErrNoRows)
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// A2A task queries

const a2aTaskColumns = `id, a2a_task_id, session_id, escrow_id, delegator_agent, delegatee_agent,
	verification_policy_json, escrow_trigger, a2a_status, metadata_json, created_at, updated_at`

func scanA2ATask(scanner interface{ Scan(...any) error }) (*A2ATask, error) {
	t := &A2ATask{}
	var createdAt, updatedAt string
	var escrowID sql.NullInt64
	var escrowTriggerInt int
	err := scanner.Scan(&t.ID, &t.A2ATaskID, &t.SessionID, &escrowID, &t.DelegatorAgent, &t.DelegateeAgent,
		&t.VerificationPolicyJSON, &escrowTriggerInt, &t.A2AStatus, &t.MetadataJSON, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	if escrowID.Valid {
		v := escrowID.Int64
		t.EscrowID = &v
	}
	t.EscrowTrigger = escrowTriggerInt != 0
	t.CreatedAt, err = parseSQLiteTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse a2a_task created_at: %w", err)
	}
	t.UpdatedAt, err = parseSQLiteTime(updatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse a2a_task updated_at: %w", err)
	}
	return t, nil
}

func (d *DB) CreateA2ATask(ctx context.Context, t *A2ATask) (*A2ATask, error) {
	res, err := d.db.ExecContext(ctx,
		`INSERT INTO a2a_tasks (a2a_task_id, session_id, escrow_id, delegator_agent, delegatee_agent,
			verification_policy_json, escrow_trigger, a2a_status, metadata_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.A2ATaskID, t.SessionID, t.EscrowID, t.DelegatorAgent, t.DelegateeAgent,
		t.VerificationPolicyJSON, boolToInt(t.EscrowTrigger), t.A2AStatus, t.MetadataJSON,
	)
	if err != nil {
		return nil, fmt.Errorf("insert a2a_task: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	return d.GetA2ATask(ctx, id)
}

func (d *DB) GetA2ATask(ctx context.Context, id int64) (*A2ATask, error) {
	row := d.db.QueryRowContext(ctx, `SELECT `+a2aTaskColumns+` FROM a2a_tasks WHERE id = ?`, id)
	t, err := scanA2ATask(row)
	if err != nil {
		return nil, fmt.Errorf("get a2a_task: %w", err)
	}
	return t, nil
}

func (d *DB) GetA2ATaskByTaskID(ctx context.Context, a2aTaskID string) (*A2ATask, error) {
	row := d.db.QueryRowContext(ctx, `SELECT `+a2aTaskColumns+` FROM a2a_tasks WHERE a2a_task_id = ?`, a2aTaskID)
	t, err := scanA2ATask(row)
	if err != nil {
		return nil, fmt.Errorf("get a2a_task by task_id: %w", err)
	}
	return t, nil
}

func (d *DB) UpdateA2ATaskStatus(ctx context.Context, id int64, status string) error {
	res, err := d.db.ExecContext(ctx,
		`UPDATE a2a_tasks SET a2a_status = ?, updated_at = datetime('now') WHERE id = ?`,
		status, id,
	)
	if err != nil {
		return fmt.Errorf("UpdateA2ATaskStatus: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("UpdateA2ATaskStatus rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("UpdateA2ATaskStatus id=%d: %w", id, sql.ErrNoRows)
	}
	return nil
}

func (d *DB) UpdateA2ATaskEscrow(ctx context.Context, id int64, escrowID int64) error {
	res, err := d.db.ExecContext(ctx,
		`UPDATE a2a_tasks SET escrow_id = ?, updated_at = datetime('now') WHERE id = ?`,
		escrowID, id,
	)
	if err != nil {
		return fmt.Errorf("UpdateA2ATaskEscrow: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("UpdateA2ATaskEscrow rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("UpdateA2ATaskEscrow id=%d: %w", id, sql.ErrNoRows)
	}
	return nil
}

func (d *DB) ListA2ATasksBySession(ctx context.Context, sessionID string) ([]*A2ATask, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT `+a2aTaskColumns+` FROM a2a_tasks WHERE session_id = ? ORDER BY id DESC`, sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list a2a_tasks by session: %w", err)
	}
	defer rows.Close()

	var tasks []*A2ATask
	for rows.Next() {
		t, err := scanA2ATask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan a2a_task: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// ── AP2 Mandates ──

// CreateAP2Mandate inserts a new AP2 mandate record.
func (d *DB) CreateAP2Mandate(ctx context.Context, id, mandateType, mandateHash, signerAddress, budgetAmount, budgetCurrency string, expiresAt *string, escrowID *int64, rawPayload string) error {
	status := "bound"
	if escrowID == nil {
		status = "pending"
	}
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO ap2_mandates (id, mandate_type, mandate_hash, signer_address, budget_amount, budget_currency, expires_at, escrow_id, status, raw_payload)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, mandateType, mandateHash, signerAddress, nilIfEmpty(budgetAmount), nilIfEmpty(budgetCurrency), expiresAt, escrowID, status, rawPayload)
	if err != nil {
		return fmt.Errorf("insert ap2_mandate: %w", err)
	}
	return nil
}

// UpdateAP2MandateFunding updates a mandate's funding tx hash and status.
func (d *DB) UpdateAP2MandateFunding(ctx context.Context, mandateID, txHash string) error {
	res, err := d.db.ExecContext(ctx,
		`UPDATE ap2_mandates SET funding_tx_hash = ?, status = 'funded' WHERE id = ?`,
		txHash, mandateID)
	if err != nil {
		return fmt.Errorf("UpdateAP2MandateFunding: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("UpdateAP2MandateFunding rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("UpdateAP2MandateFunding id=%s: %w", mandateID, sql.ErrNoRows)
	}
	return nil
}

// GetAP2Mandate retrieves a mandate by ID.
func (d *DB) GetAP2Mandate(ctx context.Context, id string) (*AP2Mandate, error) {
	row := d.db.QueryRowContext(ctx,
		`SELECT id, mandate_type, mandate_hash, signer_address, budget_amount, budget_currency, expires_at, escrow_id, funding_tx_hash, status, raw_payload, created_at
		 FROM ap2_mandates WHERE id = ?`, id)

	var m AP2Mandate
	var budgetAmt, budgetCur, expiresAt, fundingTx, createdAt sql.NullString
	var escrowID sql.NullInt64

	err := row.Scan(&m.ID, &m.MandateType, &m.MandateHash, &m.SignerAddress,
		&budgetAmt, &budgetCur, &expiresAt, &escrowID, &fundingTx, &m.Status, &m.RawPayload, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("get ap2 mandate: %w", err)
	}

	m.BudgetAmount = budgetAmt.String
	m.BudgetCurrency = budgetCur.String
	m.FundingTxHash = fundingTx.String
	if expiresAt.Valid {
		m.ExpiresAt = expiresAt.String
	}
	if escrowID.Valid {
		eid := escrowID.Int64
		m.EscrowID = &eid
	}
	if createdAt.Valid {
		t, err := parseSQLiteTime(createdAt.String)
		if err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}
		m.CreatedAt = t
	}

	return &m, nil
}

// parseSQLiteTime handles the two timestamp formats that SQLite / modernc.org/sqlite
// can produce: datetime('now') returns "2006-01-02 15:04:05" while
// CURRENT_TIMESTAMP can return "2006-01-02T15:04:05Z" (ISO 8601).
func parseSQLiteTime(s string) (time.Time, error) {
	for _, layout := range []string{
		"2006-01-02 15:04:05",
		time.RFC3339,
		"2006-01-02T15:04:05Z",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised timestamp format: %q", s)
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func parseNullTime(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid || ns.String == "" {
		return nil, nil
	}
	t, err := parseSQLiteTime(ns.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
