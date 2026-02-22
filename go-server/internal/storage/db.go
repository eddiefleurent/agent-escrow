package storage

import (
	"database/sql"
	_ "embed"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
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

type DB struct {
	db *sql.DB
}

func Open(dsn string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Enable WAL mode for better concurrent read performance
	if _, err := sqlDB.Exec("PRAGMA journal_mode=WAL"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	if _, err := sqlDB.Exec("PRAGMA foreign_keys=ON"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	if _, err := sqlDB.Exec(migrationSQL); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	// Run migration 002 idempotently inside a transaction for atomicity
	tx, err := sqlDB.Begin()
	if err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("begin migration 002 tx: %w", err)
	}
	for _, stmt := range splitStatements(migration002SQL) {
		if _, err := tx.Exec(stmt); err != nil {
			if !isDuplicateColumnError(err) {
				tx.Rollback()
				sqlDB.Close()
				return nil, fmt.Errorf("run migration 002: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		tx.Rollback()
		sqlDB.Close()
		return nil, fmt.Errorf("commit migration 002: %w", err)
	}

	// Run migration 003 (backup agent) idempotently
	tx3, err := sqlDB.Begin()
	if err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("begin migration 003 tx: %w", err)
	}
	for _, stmt := range splitStatements(migration003SQL) {
		if _, err := tx3.Exec(stmt); err != nil {
			if !isDuplicateColumnError(err) {
				tx3.Rollback()
				sqlDB.Close()
				return nil, fmt.Errorf("run migration 003: %w", err)
			}
		}
	}
	if err := tx3.Commit(); err != nil {
		tx3.Rollback()
		sqlDB.Close()
		return nil, fmt.Errorf("commit migration 003: %w", err)
	}

	// Run migration 004 (reputation) idempotently
	tx4, err := sqlDB.Begin()
	if err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("begin migration 004 tx: %w", err)
	}
	for _, stmt := range splitStatements(migration004SQL) {
		if _, err := tx4.Exec(stmt); err != nil {
			if !isDuplicateColumnError(err) {
				tx4.Rollback()
				sqlDB.Close()
				return nil, fmt.Errorf("run migration 004: %w", err)
			}
		}
	}
	if err := tx4.Commit(); err != nil {
		tx4.Rollback()
		sqlDB.Close()
		return nil, fmt.Errorf("commit migration 004: %w", err)
	}

	// Run migration 005 (bidding) idempotently
	tx5, err := sqlDB.Begin()
	if err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("begin migration 005 tx: %w", err)
	}
	for _, stmt := range splitStatements(migration005SQL) {
		if _, err := tx5.Exec(stmt); err != nil {
			if !isDuplicateColumnError(err) && !isAlreadyExistsError(err) {
				tx5.Rollback()
				sqlDB.Close()
				return nil, fmt.Errorf("run migration 005: %w", err)
			}
		}
	}
	if err := tx5.Commit(); err != nil {
		tx5.Rollback()
		sqlDB.Close()
		return nil, fmt.Errorf("commit migration 005: %w", err)
	}

	return &DB{db: sqlDB}, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) SqlDB() *sql.DB {
	return d.db
}

func splitStatements(sql string) []string {
	var stmts []string
	for _, s := range strings.Split(sql, ";") {
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
