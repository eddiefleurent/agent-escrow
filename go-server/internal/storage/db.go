package storage

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"strings"

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

type DB struct {
	db *sql.DB
}

func Open(dsn string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
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
