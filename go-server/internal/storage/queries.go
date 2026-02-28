package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
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
	zkVerifier := e.ZKVerifier
	if zkVerifier == "" {
		zkVerifier = "0x0000000000000000000000000000000000000000"
	}
	circuitID := e.CircuitID
	if circuitID == "" {
		circuitID = "0x0000000000000000000000000000000000000000000000000000000000000000"
	}
	res, err := q.ExecContext(ctx,
		`INSERT INTO escrows (task_id, chain_id, factory_address, escrow_address, escrow_id, buyer, worker, verifier, verifier_panel_json, quorum_threshold, quorum_verifier_count, verifier_stake_per_verifier, arbitrator, amount, worker_stake, token, status, submission_deadline, review_period_seconds, dispute_period_seconds, arbitrator_timeout_seconds, milestone_count, current_milestone, backup_worker, backup_deadline_extension, active_worker, backup_activated, frozen, service_tier, zk_verifier, circuit_id, parent_escrow_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.TaskID, e.ChainID, e.FactoryAddress, e.EscrowAddress, e.EscrowID,
		e.Buyer, e.Worker, e.Verifier, e.VerifierPanelJSON, e.QuorumThreshold, e.QuorumVerifierCount, e.VerifierStakePerVerifier,
		e.Arbitrator, e.Amount, e.WorkerStake, e.Token, e.Status,
		e.SubmissionDeadline, e.ReviewPeriodSeconds, e.DisputePeriodSeconds, e.ArbitratorTimeoutSeconds,
		msCount, e.CurrentMilestone,
		e.BackupWorker, e.BackupDeadlineExtension, activeWorker, boolToInt(e.BackupActivated), boolToInt(e.Frozen),
		e.ServiceTier, zkVerifier, circuitID, e.ParentEscrowID,
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

const escrowColumns = `id, task_id, chain_id, factory_address, escrow_address, escrow_id, buyer, worker, verifier, verifier_panel_json, quorum_threshold, quorum_verifier_count, verifier_stake_per_verifier, arbitrator, amount, worker_stake, token, status, submission_deadline, review_period_seconds, dispute_period_seconds, arbitrator_timeout_seconds, milestone_count, current_milestone, backup_worker, backup_deadline_extension, active_worker, backup_activated, frozen, service_tier, zk_verifier, circuit_id, parent_escrow_id, created_at, updated_at`

func scanEscrow(scanner interface{ Scan(...any) error }) (*Escrow, error) {
	e := &Escrow{}
	var createdAt, updatedAt string
	var backupActivatedInt, frozenInt int
	var parentEscrowID sql.NullInt64
	err := scanner.Scan(&e.ID, &e.TaskID, &e.ChainID, &e.FactoryAddress, &e.EscrowAddress, &e.EscrowID,
		&e.Buyer, &e.Worker, &e.Verifier, &e.VerifierPanelJSON, &e.QuorumThreshold, &e.QuorumVerifierCount, &e.VerifierStakePerVerifier,
		&e.Arbitrator, &e.Amount, &e.WorkerStake, &e.Token, &e.Status,
		&e.SubmissionDeadline, &e.ReviewPeriodSeconds, &e.DisputePeriodSeconds, &e.ArbitratorTimeoutSeconds,
		&e.MilestoneCount, &e.CurrentMilestone,
		&e.BackupWorker, &e.BackupDeadlineExtension, &e.ActiveWorker, &backupActivatedInt, &frozenInt,
		&e.ServiceTier, &e.ZKVerifier, &e.CircuitID, &parentEscrowID,
		&createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	e.BackupActivated = backupActivatedInt != 0
	e.Frozen = frozenInt != 0
	if parentEscrowID.Valid {
		v := parentEscrowID.Int64
		e.ParentEscrowID = &v
	}
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

func (d *DB) GetEscrowByOnChainID(ctx context.Context, chainID, escrowID int64) (*Escrow, error) {
	row := d.db.QueryRowContext(ctx,
		`SELECT `+escrowColumns+` FROM escrows WHERE chain_id = ? AND escrow_id = ?`,
		chainID, escrowID)
	e, err := scanEscrow(row)
	if err != nil {
		return nil, fmt.Errorf("get escrow by on-chain ID: %w", err)
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
			args = append(args, address)
		case "worker":
			query += ` AND worker = ?`
			args = append(args, address)
		case "verifier":
			normalizedAddr := strings.ToLower(address)
			query += ` AND (verifier = ? OR verifier_panel_json LIKE ?)`
			args = append(args, normalizedAddr, "%\""+normalizedAddr+"\"%")
		case "arbitrator":
			query += ` AND arbitrator = ?`
			args = append(args, address)
		default:
			return nil, fmt.Errorf("invalid role: %s", role)
		}
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

func (d *DB) CreateSubmission(ctx context.Context, escrowID int64, submissionHash, submissionURI, proofHash string) (*Submission, error) {
	if proofHash == "" {
		proofHash = "0x0000000000000000000000000000000000000000000000000000000000000000"
	}
	res, err := d.db.ExecContext(ctx,
		`INSERT INTO submissions (escrow_id, submission_hash, submission_uri, proof_hash) VALUES (?, ?, ?, ?)`,
		escrowID, submissionHash, submissionURI, proofHash,
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
		`SELECT id, escrow_id, submission_hash, submission_uri, proof_hash, submitted_at FROM submissions WHERE id = ?`, id,
	).Scan(&s.ID, &s.EscrowID, &s.SubmissionHash, &s.SubmissionURI, &s.ProofHash, &submittedAt)
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
		`SELECT id, escrow_id, submission_hash, submission_uri, proof_hash, submitted_at FROM submissions WHERE escrow_id = ? ORDER BY id`, escrowID,
	)
	if err != nil {
		return nil, fmt.Errorf("list submissions: %w", err)
	}
	defer rows.Close()

	var subs []*Submission
	for rows.Next() {
		s := &Submission{}
		var submittedAt string
		if err := rows.Scan(&s.ID, &s.EscrowID, &s.SubmissionHash, &s.SubmissionURI, &s.ProofHash, &submittedAt); err != nil {
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
	verifier, arbitrator, worker_stake, milestones_json, requirements_json, required_credentials_json,
	bidding_mode, commit_deadline, reveal_deadline, service_tier, parent_escrow_id,
	status, expires_at, created_at, updated_at`

func scanRFQ(scanner interface{ Scan(...any) error }) (*RFQ, error) {
	r := &RFQ{}
	var createdAt, updatedAt string
	var parentEscrowID sql.NullInt64
	err := scanner.Scan(&r.ID, &r.Title, &r.Description, &r.SpecHash, &r.Buyer, &r.Token,
		&r.BudgetMin, &r.BudgetMax, &r.Deadline, &r.ReviewPeriodSeconds,
		&r.DisputePeriodSeconds, &r.ArbitratorTimeoutSeconds,
		&r.Verifier, &r.Arbitrator, &r.WorkerStake, &r.MilestonesJSON, &r.RequirementsJSON,
		&r.RequiredCredentialsJSON,
		&r.BiddingMode, &r.CommitDeadline, &r.RevealDeadline,
		&r.ServiceTier, &parentEscrowID,
		&r.Status, &r.ExpiresAt, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	if parentEscrowID.Valid {
		v := parentEscrowID.Int64
		r.ParentEscrowID = &v
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
			verifier, arbitrator, worker_stake, milestones_json, requirements_json, required_credentials_json,
			bidding_mode, commit_deadline, reveal_deadline, service_tier, parent_escrow_id, status, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Title, r.Description, r.SpecHash, r.Buyer, r.Token, r.BudgetMin, r.BudgetMax,
		r.Deadline, r.ReviewPeriodSeconds, r.DisputePeriodSeconds, r.ArbitratorTimeoutSeconds,
		r.Verifier, r.Arbitrator, r.WorkerStake, r.MilestonesJSON, r.RequirementsJSON,
		r.RequiredCredentialsJSON,
		r.BiddingMode, r.CommitDeadline, r.RevealDeadline, r.ServiceTier, r.ParentEscrowID, r.Status, r.ExpiresAt,
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
	milestones_json, message, status, escrow_id, expires_at, stake_mandate_id,
	credentials_json, credential_verified, credential_match_summary,
	created_at, updated_at`

func scanBid(scanner interface{ Scan(...any) error }) (*Bid, error) {
	b := &Bid{}
	var createdAt, updatedAt string
	var escrowID sql.NullInt64
	var stakeMandateID sql.NullString
	var credentialVerifiedInt int
	err := scanner.Scan(&b.ID, &b.RFQID, &b.Bidder, &b.Amount, &b.EstimatedDuration,
		&b.ReputationBond, &b.MilestonesJSON, &b.Message, &b.Status,
		&escrowID, &b.ExpiresAt, &stakeMandateID,
		&b.CredentialsJSON, &credentialVerifiedInt, &b.CredentialMatchSummary,
		&createdAt, &updatedAt)
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
	b.CredentialVerified = credentialVerifiedInt != 0
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

func createBidOn(ctx context.Context, q dbExecer, b *Bid) (*Bid, error) {
	credJSON := b.CredentialsJSON
	if credJSON == "" {
		credJSON = "[]"
	}
	matchSummary := b.CredentialMatchSummary
	if matchSummary == "" {
		matchSummary = "{}"
	}
	res, err := q.ExecContext(ctx,
		`INSERT INTO bids (rfq_id, bidder, amount, estimated_duration, reputation_bond,
			milestones_json, message, status, expires_at, stake_mandate_id,
			credentials_json, credential_verified, credential_match_summary)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.RFQID, b.Bidder, b.Amount, b.EstimatedDuration, b.ReputationBond,
		b.MilestonesJSON, b.Message, b.Status, b.ExpiresAt, nilIfEmpty(b.StakeMandateID),
		credJSON, boolToInt(b.CredentialVerified), matchSummary,
	)
	if err != nil {
		return nil, fmt.Errorf("insert bid: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	row := q.QueryRowContext(ctx, `SELECT `+bidColumns+` FROM bids WHERE id = ?`, id)
	out, err := scanBid(row)
	if err != nil {
		return nil, fmt.Errorf("get bid: %w", err)
	}
	return out, nil
}

func (d *DB) CreateBid(ctx context.Context, b *Bid) (*Bid, error) {
	return createBidOn(ctx, d.db, b)
}

func (d *DB) CreateBidTx(ctx context.Context, tx *sql.Tx, b *Bid) (*Bid, error) {
	return createBidOn(ctx, tx, b)
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

func updateBidCredentialVerificationOn(ctx context.Context, q dbExecer, bidID int64, verified bool, matchSummary string) error {
	// Normalize empty input to a valid JSON object and validate before writing.
	if matchSummary == "" {
		matchSummary = "{}"
	}
	if !json.Valid([]byte(matchSummary)) {
		return fmt.Errorf("UpdateBidCredentialVerification bid=%d: matchSummary is not valid JSON", bidID)
	}
	res, err := q.ExecContext(ctx,
		`UPDATE bids SET credential_verified = ?, credential_match_summary = ?, updated_at = datetime('now') WHERE id = ?`,
		boolToInt(verified), matchSummary, bidID,
	)
	if err != nil {
		return fmt.Errorf("UpdateBidCredentialVerification: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("UpdateBidCredentialVerification rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("UpdateBidCredentialVerification bid=%d: %w", bidID, sql.ErrNoRows)
	}
	return nil
}

func (d *DB) UpdateBidCredentialVerification(ctx context.Context, bidID int64, verified bool, matchSummary string) error {
	return updateBidCredentialVerificationOn(ctx, d.db, bidID, verified, matchSummary)
}

func (d *DB) UpdateBidCredentialVerificationTx(ctx context.Context, tx *sql.Tx, bidID int64, verified bool, matchSummary string) error {
	return updateBidCredentialVerificationOn(ctx, tx, bidID, verified, matchSummary)
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

const bidCommitColumns = `id, rfq_id, bidder, commitment, nonce, status, revealed_bid_id, created_at, updated_at`

var (
	ErrDuplicateBidCommitNonce      = errors.New("duplicate bid commit nonce")
	ErrDuplicateBidCommitCommitment = errors.New("duplicate bid commit commitment")
)

func scanBidCommit(scanner interface{ Scan(...any) error }) (*BidCommit, error) {
	c := &BidCommit{}
	var createdAt, updatedAt string
	var revealedBidID sql.NullInt64
	err := scanner.Scan(
		&c.ID, &c.RFQID, &c.Bidder, &c.Commitment, &c.Nonce, &c.Status, &revealedBidID, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	if revealedBidID.Valid {
		v := revealedBidID.Int64
		c.RevealedBidID = &v
	}
	c.CreatedAt, err = parseSQLiteTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse bid_commit created_at: %w", err)
	}
	c.UpdatedAt, err = parseSQLiteTime(updatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse bid_commit updated_at: %w", err)
	}
	return c, nil
}

func createBidCommitOn(ctx context.Context, q dbExecer, c *BidCommit) (*BidCommit, error) {
	var revealed any
	if c.RevealedBidID != nil {
		revealed = *c.RevealedBidID
	}
	res, err := q.ExecContext(ctx,
		`INSERT INTO bid_commits (rfq_id, bidder, commitment, nonce, status, revealed_bid_id) VALUES (?, ?, ?, ?, ?, ?)`,
		c.RFQID, c.Bidder, c.Commitment, c.Nonce, c.Status, revealed,
	)
	if err != nil {
		if isSQLiteUniqueConstraint(err, "bid_commits.rfq_id", "bid_commits.bidder", "bid_commits.nonce") {
			return nil, fmt.Errorf("insert bid_commit: %w", ErrDuplicateBidCommitNonce)
		}
		if isSQLiteUniqueConstraint(err, "bid_commits.rfq_id", "bid_commits.bidder", "bid_commits.commitment") {
			return nil, fmt.Errorf("insert bid_commit: %w", ErrDuplicateBidCommitCommitment)
		}
		return nil, fmt.Errorf("insert bid_commit: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	row := q.QueryRowContext(ctx, `SELECT `+bidCommitColumns+` FROM bid_commits WHERE id = ?`, id)
	out, err := scanBidCommit(row)
	if err != nil {
		return nil, fmt.Errorf("get bid_commit: %w", err)
	}
	return out, nil
}

func isSQLiteUniqueConstraint(err error, cols ...string) bool {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
		lowerErr := strings.ToLower(err.Error())
		for _, col := range cols {
			if !strings.Contains(lowerErr, col) {
				return false
			}
		}
		return true
	}

	// Keep text matching as a practical fallback for wrapped/non-sqlite driver errors.
	lowerErr := strings.ToLower(err.Error())
	if !strings.Contains(lowerErr, "unique constraint failed") {
		return false
	}
	for _, col := range cols {
		if !strings.Contains(lowerErr, col) {
			return false
		}
	}
	return true
}

func (d *DB) CreateBidCommit(ctx context.Context, c *BidCommit) (*BidCommit, error) {
	return createBidCommitOn(ctx, d.db, c)
}

func (d *DB) CreateBidCommitTx(ctx context.Context, tx *sql.Tx, c *BidCommit) (*BidCommit, error) {
	return createBidCommitOn(ctx, tx, c)
}

func (d *DB) GetBidCommitByRFQBidderNonce(ctx context.Context, rfqID int64, bidder, nonce string) (*BidCommit, error) {
	row := d.db.QueryRowContext(
		ctx,
		`SELECT `+bidCommitColumns+` FROM bid_commits WHERE rfq_id = ? AND bidder = ? AND nonce = ?`,
		rfqID, bidder, nonce,
	)
	c, err := scanBidCommit(row)
	if err != nil {
		return nil, fmt.Errorf("get bid_commit by nonce: %w", err)
	}
	return c, nil
}

func (d *DB) GetBidCommitByRFQBidderCommitment(ctx context.Context, rfqID int64, bidder, commitment string) (*BidCommit, error) {
	row := d.db.QueryRowContext(
		ctx,
		`SELECT `+bidCommitColumns+` FROM bid_commits WHERE rfq_id = ? AND bidder = ? AND commitment = ?`,
		rfqID, bidder, commitment,
	)
	c, err := scanBidCommit(row)
	if err != nil {
		return nil, fmt.Errorf("get bid_commit by commitment: %w", err)
	}
	return c, nil
}

func (d *DB) GetBidCommitByRevealedBidID(ctx context.Context, bidID int64) (*BidCommit, error) {
	row := d.db.QueryRowContext(
		ctx,
		`SELECT `+bidCommitColumns+` FROM bid_commits WHERE revealed_bid_id = ?`,
		bidID,
	)
	c, err := scanBidCommit(row)
	if err != nil {
		return nil, fmt.Errorf("get bid_commit by revealed bid id: %w", err)
	}
	return c, nil
}

func (d *DB) ListBidCommitsByRFQ(ctx context.Context, rfqID int64) ([]*BidCommit, error) {
	rows, err := d.db.QueryContext(
		ctx,
		`SELECT `+bidCommitColumns+` FROM bid_commits WHERE rfq_id = ? ORDER BY id DESC`,
		rfqID,
	)
	if err != nil {
		return nil, fmt.Errorf("list bid_commits by rfq: %w", err)
	}
	defer rows.Close()

	var out []*BidCommit
	for rows.Next() {
		c, err := scanBidCommit(rows)
		if err != nil {
			return nil, fmt.Errorf("scan bid_commit: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func updateBidCommitRevealOn(ctx context.Context, q dbExecer, id, revealedBidID int64) error {
	res, err := q.ExecContext(
		ctx,
		`UPDATE bid_commits
         SET status = 'revealed', revealed_bid_id = ?, updated_at = datetime('now')
         WHERE id = ? AND status = 'committed'`,
		revealedBidID, id,
	)
	if err != nil {
		return fmt.Errorf("UpdateBidCommitReveal: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("UpdateBidCommitReveal rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("UpdateBidCommitReveal id=%d: %w", id, sql.ErrNoRows)
	}
	return nil
}

func (d *DB) UpdateBidCommitReveal(ctx context.Context, id, revealedBidID int64) error {
	return updateBidCommitRevealOn(ctx, d.db, id, revealedBidID)
}

func (d *DB) UpdateBidCommitRevealTx(ctx context.Context, tx *sql.Tx, id, revealedBidID int64) error {
	return updateBidCommitRevealOn(ctx, tx, id, revealedBidID)
}

func updateBidCommitStatusByRevealedBidOn(ctx context.Context, q dbExecer, bidID int64, status string) error {
	_, err := q.ExecContext(
		ctx,
		`UPDATE bid_commits SET status = ?, updated_at = datetime('now') WHERE revealed_bid_id = ?`,
		status, bidID,
	)
	if err != nil {
		return fmt.Errorf("UpdateBidCommitStatusByRevealedBid: %w", err)
	}
	return nil
}

func (d *DB) UpdateBidCommitStatusByRevealedBidTx(ctx context.Context, tx *sql.Tx, bidID int64, status string) error {
	return updateBidCommitStatusByRevealedBidOn(ctx, tx, bidID, status)
}

func rejectUnacceptedBidCommitsOn(ctx context.Context, q dbExecer, rfqID, acceptedBidID int64) error {
	_, err := q.ExecContext(
		ctx,
		`UPDATE bid_commits
         SET status = 'rejected', updated_at = datetime('now')
         WHERE rfq_id = ?
           AND status IN ('committed', 'revealed')
           AND (revealed_bid_id IS NULL OR revealed_bid_id != ?)`,
		rfqID, acceptedBidID,
	)
	if err != nil {
		return fmt.Errorf("RejectUnacceptedBidCommits: %w", err)
	}
	return nil
}

func (d *DB) RejectUnacceptedBidCommits(ctx context.Context, rfqID, acceptedBidID int64) error {
	return rejectUnacceptedBidCommitsOn(ctx, d.db, rfqID, acceptedBidID)
}

func (d *DB) RejectUnacceptedBidCommitsTx(ctx context.Context, tx *sql.Tx, rfqID, acceptedBidID int64) error {
	return rejectUnacceptedBidCommitsOn(ctx, tx, rfqID, acceptedBidID)
}

func (d *DB) CountActiveBidCommitsByRFQBidder(ctx context.Context, rfqID int64, bidder string) (int, error) {
	if rfqID <= 0 {
		return 0, errors.New("count active bid_commits: rfqID must be > 0")
	}
	if bidder == "" {
		return 0, errors.New("count active bid_commits: bidder must be non-empty")
	}

	var count int
	err := d.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM bid_commits
         WHERE rfq_id = ? AND bidder = ? AND status IN ('committed', 'revealed')`,
		rfqID, bidder,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count active bid_commits: %w", err)
	}
	return count, nil
}

func (d *DB) CountRecentBidCommitsByRFQBidder(
	ctx context.Context, rfqID int64, bidder string, windowSeconds int64, now time.Time,
) (int, error) {
	if windowSeconds <= 0 {
		return 0, errors.New("count recent bid_commits: windowSeconds must be > 0")
	}
	cutoff := now.UTC().Add(-time.Duration(windowSeconds) * time.Second).Format("2006-01-02 15:04:05")
	var count int
	err := d.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM bid_commits
         WHERE rfq_id = ? AND bidder = ?
           AND created_at >= ?`,
		rfqID, bidder, cutoff,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count recent bid_commits: %w", err)
	}
	return count, nil
}

// expireCommittedBidCommitsOn marks committed bid commits for the RFQ as expired.
// Precondition: the caller must ensure the RFQ's sealed-bid reveal deadline has passed.
func expireCommittedBidCommitsOn(ctx context.Context, q dbExecer, rfqID int64) error {
	_, err := q.ExecContext(
		ctx,
		`UPDATE bid_commits
         SET status = 'expired', updated_at = datetime('now')
         WHERE rfq_id = ? AND status = 'committed'`,
		rfqID,
	)
	if err != nil {
		return fmt.Errorf("ExpireCommittedBidCommits: %w", err)
	}
	return nil
}

// ExpireCommittedBidCommits marks committed bid commits for the RFQ as expired.
// Precondition: the caller must ensure the RFQ's sealed-bid reveal deadline has passed.
func (d *DB) ExpireCommittedBidCommits(ctx context.Context, rfqID int64) error {
	return expireCommittedBidCommitsOn(ctx, d.db, rfqID)
}

// ExpireCommittedBidCommitsTx marks committed bid commits for the RFQ as expired in a transaction.
// Precondition: the caller must ensure the RFQ's sealed-bid reveal deadline has passed.
func (d *DB) ExpireCommittedBidCommitsTx(ctx context.Context, tx *sql.Tx, rfqID int64) error {
	return expireCommittedBidCommitsOn(ctx, tx, rfqID)
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

// EventExistsForContract returns true if a chain log with the given event name
// exists for the specified contract address.
func (d *DB) EventExistsForContract(ctx context.Context, contractAddress, eventName string) (bool, error) {
	var count int
	err := d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM chain_logs WHERE contract_address = ? AND event_name = ?`,
		contractAddress, eventName,
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
        submission_hash, submission_uri, proof_hash, submitted_at, approved_at, disputed_at,
        dispute_reason_uri, created_at, updated_at`

func scanMilestone(scanner interface{ Scan(...any) error }) (*MilestoneRecord, error) {
	m := &MilestoneRecord{}
	var createdAt, updatedAt string
	var submittedAt, approvedAt, disputedAt sql.NullString
	err := scanner.Scan(&m.ID, &m.EscrowID, &m.MilestoneIndex, &m.Amount, &m.SubmissionDeadline, &m.Status,
		&m.SubmissionHash, &m.SubmissionURI, &m.ProofHash, &submittedAt, &approvedAt, &disputedAt,
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

func (d *DB) UpdateMilestoneSubmission(ctx context.Context, escrowID int64, milestoneIndex int, hash, uri, proofHash string) error {
	if proofHash == "" {
		proofHash = "0x0000000000000000000000000000000000000000000000000000000000000000"
	}
	res, err := d.db.ExecContext(ctx,
		`UPDATE milestones SET submission_hash = ?, submission_uri = ?, proof_hash = ?, submitted_at = datetime('now'),
		        status = 'submitted', updated_at = datetime('now')
		 WHERE escrow_id = ? AND milestone_index = ?`,
		hash, uri, proofHash, escrowID, milestoneIndex,
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

// ── Emergency response protocol queries ──

func (d *DB) UpsertFrozenAddress(ctx context.Context, address, reason, frozenBy string) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO frozen_addresses (address, reason, frozen_by) VALUES (?, ?, ?)
		 ON CONFLICT(address) DO UPDATE SET frozen_at = datetime('now'), reason = excluded.reason, frozen_by = excluded.frozen_by`,
		address, reason, frozenBy)
	return err
}

func (d *DB) DeleteFrozenAddress(ctx context.Context, address string) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM frozen_addresses WHERE address = ?`, address)
	return err
}

func (d *DB) ListFrozenAddresses(ctx context.Context) ([]*FrozenAddress, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT address, frozen_at, reason, frozen_by FROM frozen_addresses ORDER BY frozen_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list frozen addresses: %w", err)
	}
	defer rows.Close()

	var addrs []*FrozenAddress
	for rows.Next() {
		a := &FrozenAddress{}
		var frozenAt string
		if err := rows.Scan(&a.Address, &frozenAt, &a.Reason, &a.FrozenBy); err != nil {
			return nil, fmt.Errorf("scan frozen address: %w", err)
		}
		a.FrozenAt, err = parseSQLiteTime(frozenAt)
		if err != nil {
			return nil, fmt.Errorf("parse frozen_at: %w", err)
		}
		addrs = append(addrs, a)
	}
	return addrs, rows.Err()
}

func (d *DB) IsFrozenAddress(ctx context.Context, address string) (bool, error) {
	var count int
	err := d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM frozen_addresses WHERE address = ?`, address).Scan(&count)
	return count > 0, err
}

func (d *DB) UpdateEscrowFrozen(ctx context.Context, id int64, frozen bool) error {
	_, err := d.db.ExecContext(ctx,
		`UPDATE escrows SET frozen = ?, updated_at = datetime('now') WHERE id = ?`,
		boolToInt(frozen), id)
	return err
}

func (d *DB) CreateEmergencyAction(ctx context.Context, action, target, escrowID, reason, txHash string) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO emergency_actions (action, target, escrow_id, reason, tx_hash) VALUES (?, ?, ?, ?, ?)`,
		action, target, escrowID, reason, txHash)
	return err
}

func (d *DB) ListEmergencyActions(ctx context.Context, limit, offset int) ([]*EmergencyAction, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, action, target, escrow_id, reason, created_at, tx_hash FROM emergency_actions ORDER BY id DESC LIMIT ? OFFSET ?`,
		limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list emergency actions: %w", err)
	}
	defer rows.Close()

	var actions []*EmergencyAction
	for rows.Next() {
		a := &EmergencyAction{}
		var createdAt string
		if err := rows.Scan(&a.ID, &a.Action, &a.Target, &a.EscrowID, &a.Reason, &createdAt, &a.TxHash); err != nil {
			return nil, fmt.Errorf("scan emergency action: %w", err)
		}
		a.CreatedAt, err = parseSQLiteTime(createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}
		actions = append(actions, a)
	}
	return actions, rows.Err()
}

// ── Attestation chain queries (paper §4.8) ──

func createAttestationChainOn(ctx context.Context, q dbExecer, ac *AttestationChain) (*AttestationChain, error) {
	var milestoneIdx any
	if ac.MilestoneIndex != nil {
		milestoneIdx = *ac.MilestoneIndex
	}
	summaryJSON := ac.VerificationSummaryJSON
	if summaryJSON == "" {
		summaryJSON = "{}"
	}
	if !json.Valid([]byte(summaryJSON)) {
		return nil, errors.New("invalid verification_summary_json")
	}
	res, err := q.ExecContext(ctx,
		`INSERT INTO attestation_chains (escrow_id, milestone_index, root_hash, verified, verification_summary_json)
		 VALUES (?, ?, ?, ?, ?)`,
		ac.EscrowID, milestoneIdx, ac.RootHash, boolToInt(ac.Verified), summaryJSON,
	)
	if err != nil {
		return nil, fmt.Errorf("insert attestation_chain: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}

	out := &AttestationChain{}
	var createdAt, updatedAt string
	var verifiedInt int
	var storedMilestoneIdx sql.NullInt64
	err = q.QueryRowContext(ctx,
		`SELECT id, escrow_id, milestone_index, root_hash, verified, verification_summary_json, created_at, updated_at
		 FROM attestation_chains WHERE id = ?`, id,
	).Scan(&out.ID, &out.EscrowID, &storedMilestoneIdx, &out.RootHash, &verifiedInt, &out.VerificationSummaryJSON, &createdAt, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("get attestation_chain: %w", err)
	}
	out.Verified = verifiedInt != 0
	if storedMilestoneIdx.Valid {
		v := int(storedMilestoneIdx.Int64)
		out.MilestoneIndex = &v
	}
	out.CreatedAt, err = parseSQLiteTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	out.UpdatedAt, err = parseSQLiteTime(updatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse updated_at: %w", err)
	}
	return out, nil
}

func (d *DB) CreateAttestationChain(ctx context.Context, ac *AttestationChain) (*AttestationChain, error) {
	return createAttestationChainOn(ctx, d.db, ac)
}

func (d *DB) CreateAttestationChainTx(ctx context.Context, tx *sql.Tx, ac *AttestationChain) (*AttestationChain, error) {
	return createAttestationChainOn(ctx, tx, ac)
}

func (d *DB) GetAttestationChain(ctx context.Context, id int64) (*AttestationChain, error) {
	ac := &AttestationChain{}
	var createdAt, updatedAt string
	var verifiedInt int
	var milestoneIdx sql.NullInt64
	err := d.db.QueryRowContext(ctx,
		`SELECT id, escrow_id, milestone_index, root_hash, verified, verification_summary_json, created_at, updated_at
		 FROM attestation_chains WHERE id = ?`, id,
	).Scan(&ac.ID, &ac.EscrowID, &milestoneIdx, &ac.RootHash, &verifiedInt, &ac.VerificationSummaryJSON, &createdAt, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("get attestation_chain: %w", err)
	}
	ac.Verified = verifiedInt != 0
	if milestoneIdx.Valid {
		v := int(milestoneIdx.Int64)
		ac.MilestoneIndex = &v
	}
	ac.CreatedAt, err = parseSQLiteTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	ac.UpdatedAt, err = parseSQLiteTime(updatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse updated_at: %w", err)
	}
	return ac, nil
}

func (d *DB) GetAttestationChainsByEscrow(ctx context.Context, escrowID int64) ([]*AttestationChain, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, escrow_id, milestone_index, root_hash, verified, verification_summary_json, created_at, updated_at
		 FROM attestation_chains WHERE escrow_id = ? ORDER BY id DESC`, escrowID,
	)
	if err != nil {
		return nil, fmt.Errorf("list attestation_chains: %w", err)
	}
	defer rows.Close()

	var chains []*AttestationChain
	for rows.Next() {
		ac := &AttestationChain{}
		var createdAt, updatedAt string
		var verifiedInt int
		var milestoneIdx sql.NullInt64
		if err := rows.Scan(&ac.ID, &ac.EscrowID, &milestoneIdx, &ac.RootHash, &verifiedInt, &ac.VerificationSummaryJSON, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan attestation_chain: %w", err)
		}
		ac.Verified = verifiedInt != 0
		if milestoneIdx.Valid {
			v := int(milestoneIdx.Int64)
			ac.MilestoneIndex = &v
		}
		ac.CreatedAt, err = parseSQLiteTime(createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}
		ac.UpdatedAt, err = parseSQLiteTime(updatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse updated_at: %w", err)
		}
		chains = append(chains, ac)
	}
	return chains, rows.Err()
}

func (d *DB) UpdateAttestationChainVerification(ctx context.Context, id int64, verified bool, rootHash, summaryJSON string) error {
	if summaryJSON == "" {
		summaryJSON = "{}"
	}
	if !json.Valid([]byte(summaryJSON)) {
		return errors.New("invalid verification_summary_json")
	}
	res, err := d.db.ExecContext(ctx,
		`UPDATE attestation_chains SET verified = ?, root_hash = ?, verification_summary_json = ?, updated_at = datetime('now') WHERE id = ?`,
		boolToInt(verified), rootHash, summaryJSON, id,
	)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected update attestation_chain: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("update attestation_chain id=%d: %w", id, sql.ErrNoRows)
	}
	return nil
}

func createAttestationLinkOn(ctx context.Context, q dbExecer, link *AttestationLink) (*AttestationLink, error) {
	payloadJSON := link.PayloadJSON
	if payloadJSON == "" {
		payloadJSON = "{}"
	}
	if !json.Valid([]byte(payloadJSON)) {
		return nil, errors.New("invalid payload_json")
	}
	parentLinkID := sql.NullString{
		String: link.ParentLinkID,
		Valid:  link.ParentLinkID != "",
	}
	if !parentLinkID.Valid {
		// attestation_links.parent_link_id is currently NOT NULL in schema.
		parentLinkID = sql.NullString{String: "", Valid: true}
	}
	res, err := q.ExecContext(ctx,
		`INSERT INTO attestation_links (chain_id, link_id, parent_link_id, from_address, to_address, child_escrow_id, task_spec_hash, outcome_hash, issued_at, expires_at, nonce, signature, payload_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		link.ChainID, link.LinkID, parentLinkID, link.FromAddress, link.ToAddress,
		link.ChildEscrowID, link.TaskSpecHash, link.OutcomeHash,
		link.IssuedAt, link.ExpiresAt, link.Nonce, link.Signature, payloadJSON,
	)
	if err != nil {
		return nil, fmt.Errorf("insert attestation_link: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}

	out := &AttestationLink{}
	var createdAt string
	var childEscrowID sql.NullInt64
	var storedParentLinkID sql.NullString
	err = q.QueryRowContext(ctx,
		`SELECT id, chain_id, link_id, parent_link_id, from_address, to_address, child_escrow_id,
		        task_spec_hash, outcome_hash, issued_at, expires_at, nonce, signature, payload_json, created_at
		 FROM attestation_links WHERE id = ?`, id,
	).Scan(&out.ID, &out.ChainID, &out.LinkID, &storedParentLinkID, &out.FromAddress, &out.ToAddress,
		&childEscrowID, &out.TaskSpecHash, &out.OutcomeHash,
		&out.IssuedAt, &out.ExpiresAt, &out.Nonce, &out.Signature, &out.PayloadJSON, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("get attestation_link: %w", err)
	}
	if storedParentLinkID.Valid {
		out.ParentLinkID = storedParentLinkID.String
	}
	if childEscrowID.Valid {
		v := childEscrowID.Int64
		out.ChildEscrowID = &v
	}
	out.CreatedAt, err = parseSQLiteTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	return out, nil
}

func (d *DB) CreateAttestationLink(ctx context.Context, link *AttestationLink) (*AttestationLink, error) {
	return createAttestationLinkOn(ctx, d.db, link)
}

func (d *DB) CreateAttestationLinkTx(ctx context.Context, tx *sql.Tx, link *AttestationLink) (*AttestationLink, error) {
	return createAttestationLinkOn(ctx, tx, link)
}

func (d *DB) GetAttestationLink(ctx context.Context, id int64) (*AttestationLink, error) {
	link := &AttestationLink{}
	var createdAt string
	var childEscrowID sql.NullInt64
	var parentLinkID sql.NullString
	err := d.db.QueryRowContext(ctx,
		`SELECT id, chain_id, link_id, parent_link_id, from_address, to_address, child_escrow_id,
		        task_spec_hash, outcome_hash, issued_at, expires_at, nonce, signature, payload_json, created_at
		 FROM attestation_links WHERE id = ?`, id,
	).Scan(&link.ID, &link.ChainID, &link.LinkID, &parentLinkID, &link.FromAddress, &link.ToAddress,
		&childEscrowID, &link.TaskSpecHash, &link.OutcomeHash,
		&link.IssuedAt, &link.ExpiresAt, &link.Nonce, &link.Signature, &link.PayloadJSON, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("get attestation_link: %w", err)
	}
	if parentLinkID.Valid {
		link.ParentLinkID = parentLinkID.String
	}
	if childEscrowID.Valid {
		v := childEscrowID.Int64
		link.ChildEscrowID = &v
	}
	link.CreatedAt, err = parseSQLiteTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	return link, nil
}

func (d *DB) GetAttestationLinksByChain(ctx context.Context, chainID int64) ([]*AttestationLink, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, chain_id, link_id, parent_link_id, from_address, to_address, child_escrow_id,
		        task_spec_hash, outcome_hash, issued_at, expires_at, nonce, signature, payload_json, created_at
		 FROM attestation_links WHERE chain_id = ? ORDER BY id`, chainID,
	)
	if err != nil {
		return nil, fmt.Errorf("list attestation_links: %w", err)
	}
	defer rows.Close()

	var links []*AttestationLink
	for rows.Next() {
		link := &AttestationLink{}
		var createdAt string
		var childEscrowID sql.NullInt64
		var parentLinkID sql.NullString
		if err := rows.Scan(&link.ID, &link.ChainID, &link.LinkID, &parentLinkID, &link.FromAddress, &link.ToAddress,
			&childEscrowID, &link.TaskSpecHash, &link.OutcomeHash,
			&link.IssuedAt, &link.ExpiresAt, &link.Nonce, &link.Signature, &link.PayloadJSON, &createdAt); err != nil {
			return nil, fmt.Errorf("scan attestation_link: %w", err)
		}
		if parentLinkID.Valid {
			link.ParentLinkID = parentLinkID.String
		}
		if childEscrowID.Valid {
			v := childEscrowID.Int64
			link.ChildEscrowID = &v
		}
		link.CreatedAt, err = parseSQLiteTime(createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}
		links = append(links, link)
	}
	return links, rows.Err()
}

// ListChildEscrows returns escrows whose parent_escrow_id matches the given ID.
func (d *DB) ListChildEscrows(ctx context.Context, parentEscrowID int64) ([]*Escrow, error) {
	return d.queryEscrows(ctx, `SELECT `+escrowColumns+` FROM escrows WHERE parent_escrow_id = ? ORDER BY id`, parentEscrowID)
}

// Checkpoint queries (paper §6.1: checkpoint artifacts for mid-task agent swaps)

func scanCheckpoint(row interface{ Scan(...any) error }) (*Checkpoint, error) {
	cp := &Checkpoint{}
	var createdAt string
	var milestoneIdx sql.NullInt64
	var completionPct sql.NullInt64
	err := row.Scan(
		&cp.ID, &cp.EscrowID, &milestoneIdx,
		&cp.StateSnapshotURI, &cp.SnapshotHash, &cp.SchemaVersion,
		&cp.CommittedBy, &completionPct, &cp.MetadataJSON, &createdAt,
	)
	if err != nil {
		return nil, err
	}
	if milestoneIdx.Valid {
		v := int(milestoneIdx.Int64)
		cp.MilestoneIndex = &v
	}
	if completionPct.Valid {
		v := int(completionPct.Int64)
		cp.CompletionPct = &v
	}
	cp.CreatedAt, err = parseSQLiteTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	return cp, nil
}

const checkpointColumns = `id, escrow_id, milestone_index, state_snapshot_uri, snapshot_hash, schema_version, committed_by, completion_pct, metadata_json, created_at`

func createCheckpointOn(ctx context.Context, q dbExecer, cp *Checkpoint) (*Checkpoint, error) {
	if cp.EscrowID <= 0 {
		return nil, errors.New("escrow_id must be > 0")
	}
	if cp.StateSnapshotURI == "" {
		return nil, errors.New("state_snapshot_uri is required")
	}
	if cp.CommittedBy == "" {
		return nil, errors.New("committed_by is required")
	}
	if cp.SchemaVersion == "" {
		cp.SchemaVersion = "checkpoint-v1"
	}
	metadataJSON := cp.MetadataJSON
	if metadataJSON == "" {
		metadataJSON = "{}"
	}
	if !json.Valid([]byte(metadataJSON)) {
		return nil, errors.New("invalid metadata_json")
	}

	var milestoneIdx any
	if cp.MilestoneIndex != nil {
		if *cp.MilestoneIndex < 0 {
			return nil, errors.New("milestone_index must be >= 0")
		}
		milestoneIdx = *cp.MilestoneIndex
	}
	var completionPct any
	if cp.CompletionPct != nil {
		v := *cp.CompletionPct
		if v < 0 || v > 100 {
			return nil, errors.New("completion_pct must be 0-100")
		}
		completionPct = v
	}

	res, err := q.ExecContext(ctx,
		`INSERT INTO checkpoints (escrow_id, milestone_index, state_snapshot_uri, snapshot_hash, schema_version, committed_by, completion_pct, metadata_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		cp.EscrowID, milestoneIdx, cp.StateSnapshotURI, cp.SnapshotHash, cp.SchemaVersion, cp.CommittedBy, completionPct, metadataJSON,
	)
	if err != nil {
		return nil, fmt.Errorf("insert checkpoint: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}

	out, err := scanCheckpoint(q.QueryRowContext(ctx,
		`SELECT `+checkpointColumns+` FROM checkpoints WHERE id = ?`, id,
	))
	if err != nil {
		return nil, fmt.Errorf("get checkpoint: %w", err)
	}
	return out, nil
}

func (d *DB) CreateCheckpoint(ctx context.Context, cp *Checkpoint) (*Checkpoint, error) {
	return createCheckpointOn(ctx, d.db, cp)
}

func (d *DB) CreateCheckpointTx(ctx context.Context, tx *sql.Tx, cp *Checkpoint) (*Checkpoint, error) {
	return createCheckpointOn(ctx, tx, cp)
}

// ListCheckpointsByEscrow returns all checkpoints for an escrow, newest first.
// If milestoneIndex is non-nil, results are filtered to that milestone.
func (d *DB) ListCheckpointsByEscrow(ctx context.Context, escrowID int64, milestoneIndex *int) ([]*Checkpoint, error) {
	var rows *sql.Rows
	var err error
	if milestoneIndex != nil {
		rows, err = d.db.QueryContext(ctx,
			`SELECT `+checkpointColumns+` FROM checkpoints WHERE escrow_id = ? AND milestone_index = ? ORDER BY id DESC`,
			escrowID, *milestoneIndex,
		)
	} else {
		rows, err = d.db.QueryContext(ctx,
			`SELECT `+checkpointColumns+` FROM checkpoints WHERE escrow_id = ? ORDER BY id DESC`,
			escrowID,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list checkpoints: %w", err)
	}
	defer rows.Close()

	var checkpoints []*Checkpoint
	for rows.Next() {
		cp, scanErr := scanCheckpoint(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan checkpoint: %w", scanErr)
		}
		checkpoints = append(checkpoints, cp)
	}
	return checkpoints, rows.Err()
}

// GetLatestCheckpoint returns the most recent checkpoint for an escrow.
// If milestoneIndex is non-nil, returns the latest for that specific milestone.
func (d *DB) GetLatestCheckpoint(ctx context.Context, escrowID int64, milestoneIndex *int) (*Checkpoint, error) {
	var row *sql.Row
	if milestoneIndex != nil {
		row = d.db.QueryRowContext(ctx,
			`SELECT `+checkpointColumns+` FROM checkpoints WHERE escrow_id = ? AND milestone_index = ? ORDER BY id DESC LIMIT 1`,
			escrowID, *milestoneIndex,
		)
	} else {
		row = d.db.QueryRowContext(ctx,
			`SELECT `+checkpointColumns+` FROM checkpoints WHERE escrow_id = ? ORDER BY id DESC LIMIT 1`,
			escrowID,
		)
	}
	cp, err := scanCheckpoint(row)
	if err != nil {
		return nil, fmt.Errorf("get latest checkpoint: %w", err)
	}
	return cp, nil
}
