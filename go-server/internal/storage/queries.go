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
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
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
	msCount := e.MilestoneCount
	if msCount == 0 {
		msCount = 1
	}
	activeWorker := e.ActiveWorker
	if activeWorker == "" {
		activeWorker = e.Worker
	}
	res, err := d.db.Exec(
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
	return d.GetEscrow(id)
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
	e.CreatedAt, err = time.Parse("2006-01-02 15:04:05", createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	e.UpdatedAt, err = time.Parse("2006-01-02 15:04:05", updatedAt)
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

// Milestone queries

func (d *DB) CreateMilestone(m *MilestoneRecord) (*MilestoneRecord, error) {
	res, err := d.db.Exec(
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
	return d.GetMilestone(id)
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
	m.CreatedAt, err = time.Parse("2006-01-02 15:04:05", createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	m.UpdatedAt, err = time.Parse("2006-01-02 15:04:05", updatedAt)
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

func (d *DB) UpdateEscrowBackupActivated(id int64, activeWorker string) error {
	_, err := d.db.Exec(
		`UPDATE escrows SET active_worker = ?, backup_activated = 1, updated_at = datetime('now') WHERE id = ?`,
		activeWorker, id,
	)
	if err != nil {
		return fmt.Errorf("UpdateEscrowBackupActivated: %w", err)
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func parseNullTime(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid || ns.String == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02 15:04:05", ns.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
