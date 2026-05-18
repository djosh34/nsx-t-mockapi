// Package sqlite owns the local SQLite foundation for the mock NSX-T API.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // Register the modernc SQLite database driver.
)

const parentDirPerm = 0o750

const sqliteStorageBlob = "blob"

// ErrEmptyPath reports that a SQLite path was required but not provided.
var ErrEmptyPath = errors.New("sqlite path is empty")

var (
	errInvalidRawSQLResult = errors.New("invalid raw sql verification result")
	errInvalidJSONBResult  = errors.New("invalid JSONB verification result")
	errInvalidFTS5Result   = errors.New("invalid FTS5 verification result")
)

// OpenOptions configures a SQLite database connection.
type OpenOptions struct {
	Path string
}

// CapabilityReport describes the SQLite features required by this project.
type CapabilityReport struct {
	SQLiteVersion string
	JSONB         bool
	FTS5          bool
	RawSQL        bool
}

// EnsureParentDir creates the parent directory for path when one is present.
func EnsureParentDir(path string) error {
	if path == "" {
		return ErrEmptyPath
	}

	parent := filepath.Dir(path)
	if parent == "." || parent == "" {
		return nil
	}

	if err := os.MkdirAll(parent, parentDirPerm); err != nil {
		return fmt.Errorf("create sqlite parent directory %q: %w", parent, err)
	}
	return nil
}

// Open opens a SQLite database and applies required connection pragmas.
func Open(ctx context.Context, opts OpenOptions) (*sql.DB, error) {
	if opts.Path == "" {
		return nil, ErrEmptyPath
	}

	db, err := sql.Open("sqlite", opts.Path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database %q: %w", opts.Path, err)
	}

	db.SetMaxOpenConns(1)

	err = applyPragmas(ctx, db)
	if err != nil {
		closeErr := db.Close()
		if closeErr != nil {
			return nil, fmt.Errorf("apply sqlite pragmas: %w; close sqlite database: %w", err, closeErr)
		}
		return nil, fmt.Errorf("apply sqlite pragmas: %w", err)
	}

	return db, nil
}

// VerifyCapabilities checks that SQLite supports the features this service requires.
func VerifyCapabilities(ctx context.Context, db *sql.DB) (CapabilityReport, error) {
	var report CapabilityReport

	if err := db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&report.SQLiteVersion); err != nil {
		return report, fmt.Errorf("query sqlite version: %w", err)
	}

	var rawSQL int
	if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&rawSQL); err != nil {
		return report, fmt.Errorf("verify raw sql: %w", err)
	}
	report.RawSQL = rawSQL == 1
	if !report.RawSQL {
		return report, fmt.Errorf("%w: got %d, want 1", errInvalidRawSQLResult, rawSQL)
	}

	jsonb, err := verifyJSONB(ctx, db)
	if err != nil {
		return report, err
	}
	report.JSONB = jsonb

	fts5, err := verifyFTS5(ctx, db)
	if err != nil {
		return report, err
	}
	report.FTS5 = fts5

	return report, nil
}

func applyPragmas(ctx context.Context, db *sql.DB) error {
	var journalMode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode = WAL").Scan(&journalMode); err != nil {
		return fmt.Errorf("set journal_mode WAL: %w", err)
	}

	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("enable foreign_keys: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		return fmt.Errorf("set busy_timeout: %w", err)
	}

	return nil
}

func verifyJSONB(ctx context.Context, db *sql.DB) (bool, error) {
	const payload = `{"resource_type":"PolicySegment","display_name":"web","tags":["nsx","mock"]}`

	var storageType string
	var displayName string
	var valid int
	err := db.QueryRowContext(
		ctx,
		"SELECT typeof(jsonb(?)), json_extract(jsonb(?), '$.display_name'), json_valid(jsonb(?), 8)",
		payload,
		payload,
		payload,
	).Scan(&storageType, &displayName, &valid)
	if err != nil {
		return false, fmt.Errorf("verify JSONB functions: %w", err)
	}

	if storageType != sqliteStorageBlob {
		return false, fmt.Errorf("%w: storage type got %q, want blob", errInvalidJSONBResult, storageType)
	}
	if displayName != "web" {
		return false, fmt.Errorf("%w: extraction got %q, want web", errInvalidJSONBResult, displayName)
	}
	if valid != 1 {
		return false, fmt.Errorf("%w: validation got %d, want 1", errInvalidJSONBResult, valid)
	}

	return true, nil
}

func verifyFTS5(ctx context.Context, db *sql.DB) (ok bool, retErr error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin FTS5 verification transaction: %w", err)
	}
	defer func() {
		if retErr == nil {
			return
		}
		rollbackErr := tx.Rollback()
		if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			retErr = fmt.Errorf("%w; rollback FTS5 verification transaction: %w", retErr, rollbackErr)
		}
	}()

	_, err = tx.ExecContext(
		ctx,
		"CREATE VIRTUAL TABLE temp.nsx_mockapi_fts USING fts5(resource_type, payload)",
	)
	if err != nil {
		return false, fmt.Errorf("create FTS5 virtual table: %w", err)
	}
	_, err = tx.ExecContext(
		ctx,
		"INSERT INTO nsx_mockapi_fts(resource_type, payload) VALUES (?, ?)",
		"PolicySegment",
		"segment web tier",
	)
	if err != nil {
		return false, fmt.Errorf("insert FTS5 verification row: %w", err)
	}

	var matches int
	err = tx.QueryRowContext(
		ctx,
		"SELECT count(*) FROM nsx_mockapi_fts WHERE nsx_mockapi_fts MATCH ?",
		"web",
	).Scan(&matches)
	if err != nil {
		return false, fmt.Errorf("query FTS5 MATCH: %w", err)
	}
	if matches != 1 {
		return false, fmt.Errorf("%w: FTS5 MATCH got %d, want 1", errInvalidFTS5Result, matches)
	}

	if err = tx.Rollback(); err != nil {
		return false, fmt.Errorf("rollback FTS5 verification transaction: %w", err)
	}

	return true, nil
}
