package storage

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"strings"
	"sync/atomic"

	_ "modernc.org/sqlite" // SQLite driver registration
)

//go:embed migrations/001_create_tables.sql
var migrationSQL string

//go:embed migrations/002_add_milestones.sql
var migration002SQL string

//go:embed migrations/003_add_backup_agent.sql
var migration003SQL string

//go:embed migrations/004_add_reputation.sql
var migration004SQL string

//go:embed migrations/005_add_bidding.sql
var migration005SQL string

//go:embed migrations/006_add_a2a.sql
var migration006SQL string

//go:embed migrations/007_add_ap2_mandates.sql
var migration007SQL string

//go:embed migrations/008_add_emergency.sql
var migration008SQL string

//go:embed migrations/009_add_escrow_stake_token.sql
var migration009SQL string

//go:embed migrations/010_add_sealed_bidding.sql
var migration010SQL string

//go:embed migrations/011_sealed_bidding_compat_and_uniqueness.sql
var migration011SQL string

//go:embed migrations/012_sealed_bidding_indexes.sql
var migration012SQL string

//go:embed migrations/013_add_dct_tokens.sql
var migration013SQL string

//go:embed migrations/014_dct_profile_v1_hardening.sql
var migration014SQL string

//go:embed migrations/015_dct_authorization_audit.sql
var migration015SQL string

//go:embed migrations/016_add_bid_credentials.sql
var migration016SQL string

//go:embed migrations/017_add_attestation_chains.sql
var migration017SQL string

//go:embed migrations/018_add_checkpoints.sql
var migration018SQL string

//go:embed migrations/019_add_service_tier.sql
var migration019SQL string

//go:embed migrations/020_add_proof_hash.sql
var migration020SQL string

//go:embed migrations/021_add_quorum_fields.sql
var migration021SQL string

//go:embed migrations/022_add_reputation_events.sql
var migration022SQL string

//go:embed migrations/023_add_decompositions.sql
var migration023SQL string

//go:embed migrations/024_add_delegate_preference.sql
var migration024SQL string

//go:embed migrations/025_add_ucp_sessions.sql
var migration025SQL string

//go:embed migrations/026_add_escrow_create_intent.sql
var migration026SQL string

//go:embed migrations/027_add_sealed_bid_hardening.sql
var migration027SQL string

type DB struct {
	db *sql.DB
}

// ImmediateTx holds a single SQLite connection inside a BEGIN IMMEDIATE
// transaction so reads and writes share the same locked snapshot.
type ImmediateTx struct {
	conn *sql.Conn
	done atomic.Bool
}

var inMemoryDBCounter atomic.Uint64

func Open(dsn string) (*DB, error) {
	if dsn == ":memory:" {
		id := inMemoryDBCounter.Add(1)
		dsn = fmt.Sprintf("file:agent_escrow_mem_%d?mode=memory&cache=shared", id)
	}
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if strings.Contains(dsn, "mode=memory") {
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
	}

	ctx := context.Background()

	// Enable WAL mode for better concurrent read performance
	if _, err := sqlDB.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	if _, err := sqlDB.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	if _, err := sqlDB.ExecContext(ctx, migrationSQL); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	migrations := []struct {
		name string
		sql  string
	}{
		{"002", migration002SQL},
		{"003", migration003SQL},
		{"004", migration004SQL},
		{"005", migration005SQL},
		{"006", migration006SQL},
		{"007", migration007SQL},
		{"008", migration008SQL},
		{"009", migration009SQL},
		{"010", migration010SQL},
		{"011", migration011SQL},
		{"012", migration012SQL},
		{"013", migration013SQL},
		{"014", migration014SQL},
		{"015", migration015SQL},
		{"016", migration016SQL},
		{"017", migration017SQL},
		{"018", migration018SQL},
		{"019", migration019SQL},
		{"020", migration020SQL},
		{"021", migration021SQL},
		{"022", migration022SQL},
		{"023", migration023SQL},
		{"024", migration024SQL},
		{"025", migration025SQL},
		{"026", migration026SQL},
		{"027", migration027SQL},
	}

	for _, m := range migrations {
		if err := runIdempotentMigration(ctx, sqlDB, m.name, m.sql); err != nil {
			sqlDB.Close()
			return nil, err
		}
	}

	return &DB{db: sqlDB}, nil
}

func runIdempotentMigration(ctx context.Context, sqlDB *sql.DB, name, migrationSQL string) error {
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s tx: %w", name, err)
	}
	for _, stmt := range splitStatements(migrationSQL) {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			if !isDuplicateColumnError(err) && !isAlreadyExistsError(err) {
				tx.Rollback()
				return fmt.Errorf("run migration %s: %w", name, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		tx.Rollback()
		return fmt.Errorf("commit migration %s: %w", name, err)
	}
	return nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) BeginTx(ctx context.Context) (*sql.Tx, error) {
	return d.db.BeginTx(ctx, nil)
}

func (d *DB) BeginImmediateTx(ctx context.Context) (*ImmediateTx, error) {
	conn, err := d.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire db conn: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("begin immediate tx: %w", err)
	}
	return &ImmediateTx{conn: conn}, nil
}

func (tx *ImmediateTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return tx.conn.ExecContext(ctx, query, args...)
}

func (tx *ImmediateTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return tx.conn.QueryContext(ctx, query, args...)
}

func (tx *ImmediateTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return tx.conn.QueryRowContext(ctx, query, args...)
}

func (tx *ImmediateTx) Commit(ctx context.Context) error {
	if !tx.done.CompareAndSwap(false, true) {
		return nil
	}
	_, err := tx.conn.ExecContext(ctx, "COMMIT")
	closeErr := tx.conn.Close()
	if err != nil {
		return fmt.Errorf("commit immediate tx: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close immediate tx conn after commit: %w", closeErr)
	}
	return nil
}

func (tx *ImmediateTx) Rollback(ctx context.Context) error {
	if !tx.done.CompareAndSwap(false, true) {
		return nil
	}
	_, err := tx.conn.ExecContext(ctx, "ROLLBACK")
	closeErr := tx.conn.Close()
	if err != nil {
		return fmt.Errorf("rollback immediate tx: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close immediate tx conn after rollback: %w", closeErr)
	}
	return nil
}

func (d *DB) SQLDB() *sql.DB {
	return d.db
}

func splitStatements(sql string) []string {
	var stmts []string
	for s := range strings.SplitSeq(sql, ";") {
		s = strings.TrimSpace(s)
		if s != "" {
			stmts = append(stmts, s)
		}
	}
	return stmts
}

func isDuplicateColumnError(err error) bool {
	return strings.Contains(err.Error(), "duplicate column")
}

func isAlreadyExistsError(err error) bool {
	return strings.Contains(err.Error(), "already exists")
}
