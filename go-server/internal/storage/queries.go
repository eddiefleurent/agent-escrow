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
	Exec(string, ...any) (sql.Result, error)
	QueryRow(string, ...any) *sql.Row
}

// Task queries

func createTaskOn(q dbExecer, title, description, specHash string) (*Task, error) {
	res, err := q.Exec(
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
	err = q.QueryRow(
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

func (d *DB) CreateTask(title, description, specHash string) (*Task, error) {
	return createTaskOn(d.db, title, description, specHash)
}

func (d *DB) CreateTaskTx(tx *sql.Tx, title, description, specHash string) (*Task, error) {
	return createTaskOn(tx, title, description, specHash)
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
	t.CreatedAt, err = parseSQLiteTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at in GetTask: %w", err)
	}
	return t, nil
}

// Escrow queries

func createEscrowOn(q dbExecer, e *Escrow) (*Escrow, error) {
	msCount := e.MilestoneCount
	if msCount == 0 {
		msCount = 1
	}
	activeWorker := e.ActiveWorker
	if activeWorker == "" {
		activeWorker = e.Worker
	}
	res, err := q.Exec(
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
	row := q.QueryRow(`SELECT `+escrowColumns+` FROM escrows WHERE id = ?`, id)
	out, err := scanEscrow(row)
	if err != nil {
		return nil, fmt.Errorf("get escrow: %w", err)
	}
	return out, nil
}

func (d *DB) CreateEscrow(e *Escrow) (*Escrow, error) {
	return createEscrowOn(d.db, e)
}

func (d *DB) CreateEscrowTx(tx *sql.Tx, e *Escrow) (*Escrow, error) {
	return createEscrowOn(tx, e)
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

func (d *DB) GetEscrow(id int64) (*Escrow, error) {
	row := d.db.QueryRow(`SELECT `+escrowColumns+` FROM escrows WHERE id = ?`, id)
	e, err := scanEscrow(row)
	if err != nil {
		return nil, fmt.Errorf("get escrow: %w", err)
	}
	return e, nil
}

func (d *DB) GetEscrowByAddress(addr string) (*Escrow, error) {
	row := d.db.QueryRow(`SELECT `+escrowColumns+` FROM escrows WHERE escrow_address = ?`, addr)
	e, err := scanEscrow(row)
	if err != nil {
		return nil, fmt.Errorf("get escrow by address: %w", err)
	}
	return e, nil
}

func (d *DB) UpdateEscrowStatus(id int64, status string) error {
	_, err := d.db.Exec(
		`UPDATE escrows SET status = ?, updated_at = datetime('now') WHERE id = ?`,
		status, id,
	)
	if err != nil {
		return fmt.Errorf("UpdateEscrowStatus: %w", err)
	}
	return nil
}

// UpdateEscrowOnChainFields sets the on-chain address and ID after the creation tx is mined.
func (d *DB) UpdateEscrowOnChainFields(id int64, escrowAddress string, escrowID int64) error {
	_, err := d.db.Exec(
		`UPDATE escrows SET escrow_address = ?, escrow_id = ?, updated_at = datetime('now') WHERE id = ?`,
		escrowAddress, escrowID, id,
	)
	if err != nil {
		return fmt.Errorf("UpdateEscrowOnChainFields: %w", err)
	}
	return nil
}

func (d *DB) ListEscrows(role, address, status string) ([]*Escrow, error) {
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
	return d.queryEscrows(query, args...)
}

func (d *DB) ListEscrowsByChainID(chainID int64) ([]*Escrow, error) {
	return d.queryEscrows(`SELECT `+escrowColumns+` FROM escrows WHERE chain_id = ? ORDER BY id DESC`, chainID)
}

func (d *DB) queryEscrows(query string, args ...any) ([]*Escrow, error) {
	rows, err := d.db.Query(query, args...)
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

func (d *DB) UpdateEscrowMilestoneProgress(id int64, currentMilestone int) error {
	res, err := d.db.Exec(
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

func (d *DB) CreateSubmission(escrowID int64, submissionHash, submissionURI string) (*Submission, error) {
	res, err := d.db.Exec(
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
	err = d.db.QueryRow(
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

func (d *DB) CreateDispute(escrowID int64, raisedBy, reasonURI string) (*Dispute, error) {
	res, err := d.db.Exec(
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
	return d.getDispute(id)
}

func (d *DB) UpdateDispute(id int64, resolutionURI string, workerAwardBps int) error {
	_, err := d.db.Exec(
		`UPDATE disputes SET resolution_uri = ?, worker_award_bps = ?, status = 'resolved', resolved_at = datetime('now') WHERE id = ?`,
		resolutionURI, workerAwardBps, id,
	)
	return err
}

func (d *DB) GetDispute(id int64) (*Dispute, error) {
	return d.getDispute(id)
}

// GetDisputeByEscrowID returns the most recent open (non-resolved) dispute for the given escrow.
func (d *DB) GetDisputeByEscrowID(escrowID int64) (*Dispute, error) {
	var disputeID int64
	err := d.db.QueryRow(
		`SELECT id FROM disputes WHERE escrow_id = ? AND status != 'resolved' ORDER BY id DESC LIMIT 1`, escrowID,
	).Scan(&disputeID)
	if err != nil {
		return nil, fmt.Errorf("get dispute by escrow id: %w", err)
	}
	return d.getDispute(disputeID)
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

func (d *DB) GetReputation(address, role string) (*Reputation, error) {
	r := &Reputation{}
	var updatedAt string
	err := d.db.QueryRow(
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

func (d *DB) GetReputationByAddress(address string) ([]*Reputation, error) {
	rows, err := d.db.Query(
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

func (d *DB) UpsertReputation(address, role, outcome string) error {
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

	query := fmt.Sprintf(
		`INSERT INTO reputation (address, role, %s) VALUES (?, ?, 1)
		 ON CONFLICT(address, role)
		 DO UPDATE SET %s = %s + 1, updated_at = datetime('now')`,
		col, col, col,
	)
	_, err := d.db.Exec(query, address, role)
	if err != nil {
		return fmt.Errorf("upsert reputation: %w", err)
	}
	return nil
}

func (d *DB) ListReputations(minCompleted int) ([]*Reputation, error) {
	rows, err := d.db.Query(
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

func (d *DB) CreateRFQ(r *RFQ) (*RFQ, error) {
	res, err := d.db.Exec(
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
	return d.GetRFQ(id)
}

func (d *DB) GetRFQ(id int64) (*RFQ, error) {
	row := d.db.QueryRow(`SELECT `+rfqColumns+` FROM rfqs WHERE id = ?`, id)
	r, err := scanRFQ(row)
	if err != nil {
		return nil, fmt.Errorf("get rfq: %w", err)
	}
	return r, nil
}

func (d *DB) ListRFQs(status, buyer string) ([]*RFQ, error) {
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

	rows, err := d.db.Query(query, args...)
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

func updateRFQStatusOn(q dbExecer, id int64, status string) error {
	res, err := q.Exec(
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

func (d *DB) UpdateRFQStatus(id int64, status string) error {
	return updateRFQStatusOn(d.db, id, status)
}

func (d *DB) UpdateRFQStatusTx(tx *sql.Tx, id int64, status string) error {
	return updateRFQStatusOn(tx, id, status)
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

func (d *DB) CreateBid(b *Bid) (*Bid, error) {
	res, err := d.db.Exec(
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
	return d.GetBid(id)
}

func (d *DB) GetBid(id int64) (*Bid, error) {
	row := d.db.QueryRow(`SELECT `+bidColumns+` FROM bids WHERE id = ?`, id)
	b, err := scanBid(row)
	if err != nil {
		return nil, fmt.Errorf("get bid: %w", err)
	}
	return b, nil
}

func (d *DB) ListBidsByRFQ(rfqID int64) ([]*Bid, error) {
	rows, err := d.db.Query(
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

func (d *DB) ListBidsByBidder(bidder string) ([]*Bid, error) {
	rows, err := d.db.Query(
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

func (d *DB) UpdateBidStatus(id int64, status string) error {
	res, err := d.db.Exec(
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

func acceptBidOn(q dbExecer, bidID, escrowID int64) error {
	res, err := q.Exec(
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

func (d *DB) AcceptBid(bidID, escrowID int64) error {
	return acceptBidOn(d.db, bidID, escrowID)
}

func (d *DB) AcceptBidTx(tx *sql.Tx, bidID, escrowID int64) error {
	return acceptBidOn(tx, bidID, escrowID)
}

// RejectPendingBids sets all pending bids on an RFQ to rejected, except the given bid.
func rejectPendingBidsOn(q dbExecer, rfqID, exceptBidID int64) error {
	_, err := q.Exec(
		`UPDATE bids SET status = 'rejected', updated_at = datetime('now')
		 WHERE rfq_id = ? AND id != ? AND status = 'pending'`,
		rfqID, exceptBidID,
	)
	if err != nil {
		return fmt.Errorf("RejectPendingBids: %w", err)
	}
	return nil
}

func (d *DB) RejectPendingBids(rfqID, exceptBidID int64) error {
	return rejectPendingBidsOn(d.db, rfqID, exceptBidID)
}

func (d *DB) RejectPendingBidsTx(tx *sql.Tx, rfqID, exceptBidID int64) error {
	return rejectPendingBidsOn(tx, rfqID, exceptBidID)
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

// Milestone queries

func createMilestoneOn(q dbExecer, m *MilestoneRecord) (*MilestoneRecord, error) {
	res, err := q.Exec(
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
	row := q.QueryRow(`SELECT `+milestoneColumns+` FROM milestones WHERE id = ?`, id)
	out, err := scanMilestone(row)
	if err != nil {
		return nil, fmt.Errorf("get milestone: %w", err)
	}
	return out, nil
}

func (d *DB) CreateMilestone(m *MilestoneRecord) (*MilestoneRecord, error) {
	return createMilestoneOn(d.db, m)
}

func (d *DB) CreateMilestoneTx(tx *sql.Tx, m *MilestoneRecord) (*MilestoneRecord, error) {
	return createMilestoneOn(tx, m)
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

func (d *DB) GetMilestone(id int64) (*MilestoneRecord, error) {
	row := d.db.QueryRow(`SELECT `+milestoneColumns+` FROM milestones WHERE id = ?`, id)
	m, err := scanMilestone(row)
	if err != nil {
		return nil, fmt.Errorf("get milestone: %w", err)
	}
	return m, nil
}

func (d *DB) GetMilestonesByEscrow(escrowID int64) ([]*MilestoneRecord, error) {
	rows, err := d.db.Query(
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

func (d *DB) UpdateMilestoneStatus(escrowID int64, milestoneIndex int, status string) error {
	res, err := d.db.Exec(
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

func (d *DB) UpdateMilestoneSubmission(escrowID int64, milestoneIndex int, hash, uri string) error {
	res, err := d.db.Exec(
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

func (d *DB) UpdateEscrowBackupActivated(id int64, activeWorker string, newDeadline uint64) error {
	res, err := d.db.Exec(
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

func (d *DB) CreateA2ATask(t *A2ATask) (*A2ATask, error) {
	res, err := d.db.Exec(
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
	return d.GetA2ATask(id)
}

func (d *DB) GetA2ATask(id int64) (*A2ATask, error) {
	row := d.db.QueryRow(`SELECT `+a2aTaskColumns+` FROM a2a_tasks WHERE id = ?`, id)
	t, err := scanA2ATask(row)
	if err != nil {
		return nil, fmt.Errorf("get a2a_task: %w", err)
	}
	return t, nil
}

func (d *DB) GetA2ATaskByTaskID(a2aTaskID string) (*A2ATask, error) {
	row := d.db.QueryRow(`SELECT `+a2aTaskColumns+` FROM a2a_tasks WHERE a2a_task_id = ?`, a2aTaskID)
	t, err := scanA2ATask(row)
	if err != nil {
		return nil, fmt.Errorf("get a2a_task by task_id: %w", err)
	}
	return t, nil
}

func (d *DB) UpdateA2ATaskStatus(id int64, status string) error {
	res, err := d.db.Exec(
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

func (d *DB) UpdateA2ATaskEscrow(id int64, escrowID int64) error {
	res, err := d.db.Exec(
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

func (d *DB) ListA2ATasksBySession(sessionID string) ([]*A2ATask, error) {
	rows, err := d.db.Query(
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
		id, mandateType, mandateHash, signerAddress, budgetAmount, budgetCurrency, expiresAt, escrowID, status, rawPayload)
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
