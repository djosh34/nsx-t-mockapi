package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureParentDirCreatesDataDirectory(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "data", "nsx-t-mockapi.db")

	if err := EnsureParentDir(dbPath); err != nil {
		t.Fatalf("EnsureParentDir() error = %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("db.Close() error = %v", closeErr)
		}
	}()

	err = db.PingContext(context.Background())
	if err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}
}

func TestEnsureParentDirRejectsEmptyPath(t *testing.T) {
	t.Parallel()

	err := EnsureParentDir("")
	if !errors.Is(err, ErrEmptyPath) {
		t.Fatalf("EnsureParentDir(\"\") error = %v, want ErrEmptyPath", err)
	}
}

func TestEnsureParentDirAllowsPathWithoutParent(t *testing.T) {
	t.Parallel()

	if err := EnsureParentDir("nsx-t-mockapi.db"); err != nil {
		t.Fatalf("EnsureParentDir() error = %v", err)
	}
}

func TestEnsureParentDirReportsCreateFailure(t *testing.T) {
	t.Parallel()

	parentFile := filepath.Join(t.TempDir(), "parent-file")
	if err := os.WriteFile(parentFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := EnsureParentDir(filepath.Join(parentFile, "nsx-t-mockapi.db"))
	if err == nil {
		t.Fatal("EnsureParentDir() error = nil, want error")
	}
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	t.Parallel()

	db, err := Open(context.Background(), OpenOptions{})
	if db != nil {
		t.Fatalf("Open() db = %v, want nil", db)
	}
	if !errors.Is(err, ErrEmptyPath) {
		t.Fatalf("Open() error = %v, want ErrEmptyPath", err)
	}
}

func TestOpenExecutesRawSQLAgainstFileDatabase(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "data", "nsx-t-mockapi.db")
	if err := EnsureParentDir(dbPath); err != nil {
		t.Fatalf("EnsureParentDir() error = %v", err)
	}

	db, err := Open(ctx, OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("db.Close() error = %v", closeErr)
		}
	}()

	var got int
	err = db.QueryRowContext(ctx, "SELECT 1").Scan(&got)
	if err != nil {
		t.Fatalf("SELECT 1 error = %v", err)
	}
	if got != 1 {
		t.Fatalf("SELECT 1 = %d, want 1", got)
	}
}

func TestOpenReturnsPragmaErrorWhenContextIsCanceled(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "data", "nsx-t-mockapi.db")
	if err := EnsureParentDir(dbPath); err != nil {
		t.Fatalf("EnsureParentDir() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	db, err := Open(ctx, OpenOptions{Path: dbPath})
	if db != nil {
		t.Fatalf("Open() db = %v, want nil", db)
	}
	if err == nil {
		t.Fatal("Open() error = nil, want error")
	}
}

func TestVerifyCapabilitiesReportsRawSQLJSONBAndFTS5(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "data", "nsx-t-mockapi.db")
	if err := EnsureParentDir(dbPath); err != nil {
		t.Fatalf("EnsureParentDir() error = %v", err)
	}

	db, err := Open(ctx, OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("db.Close() error = %v", closeErr)
		}
	}()

	report, err := VerifyCapabilities(ctx, db)
	if err != nil {
		t.Fatalf("VerifyCapabilities() error = %v", err)
	}

	if report.SQLiteVersion == "" {
		t.Fatal("VerifyCapabilities() SQLiteVersion is empty")
	}
	if !report.RawSQL {
		t.Fatal("VerifyCapabilities() RawSQL = false, want true")
	}
	if !report.JSONB {
		t.Fatal("VerifyCapabilities() JSONB = false, want true")
	}
	if !report.FTS5 {
		t.Fatal("VerifyCapabilities() FTS5 = false, want true")
	}
}

func TestVerifyCapabilitiesReportsClosedDatabaseError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "data", "nsx-t-mockapi.db")
	if err := EnsureParentDir(dbPath); err != nil {
		t.Fatalf("EnsureParentDir() error = %v", err)
	}

	db, err := Open(ctx, OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	err = db.Close()
	if err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	_, err = VerifyCapabilities(ctx, db)
	if err == nil {
		t.Fatal("VerifyCapabilities() error = nil, want error")
	}
}

func TestVerifyJSONBReportsClosedDatabaseError(t *testing.T) {
	t.Parallel()

	db := openThenCloseDB(t)

	if _, err := verifyJSONB(context.Background(), db); err == nil {
		t.Fatal("verifyJSONB() error = nil, want error")
	}
}

func TestVerifyJSONBReportsCanceledContextError(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close() error = %v", err)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := verifyJSONB(ctx, db); err == nil {
		t.Fatal("verifyJSONB() error = nil, want error")
	}
}

func TestVerifyFTS5ReportsClosedDatabaseError(t *testing.T) {
	t.Parallel()

	db := openThenCloseDB(t)

	_, err := verifyFTS5(context.Background(), db)
	if err == nil {
		t.Fatal("verifyFTS5() error = nil, want error")
	}
}

func TestVerifyFTS5ReportsCreateVirtualTableError(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close() error = %v", err)
		}
	}()

	_, err := db.ExecContext(
		context.Background(),
		"CREATE TEMP TABLE nsx_mockapi_fts(resource_type TEXT, payload TEXT)",
	)
	if err != nil {
		t.Fatalf("CREATE TEMP TABLE error = %v", err)
	}

	_, err = verifyFTS5(context.Background(), db)
	if err == nil {
		t.Fatal("verifyFTS5() error = nil, want error")
	}
}

func TestApplyPragmasReportsCanceledContextError(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "data", "nsx-t-mockapi.db")
	if err := EnsureParentDir(dbPath); err != nil {
		t.Fatalf("EnsureParentDir() error = %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("db.Close() error = %v", closeErr)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = applyPragmas(ctx, db)
	if err == nil {
		t.Fatal("applyPragmas() error = nil, want error")
	}
}

func openThenCloseDB(t *testing.T) *sql.DB {
	t.Helper()

	db := openTestDB(t)
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	return db
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "data", "nsx-t-mockapi.db")
	if err := EnsureParentDir(dbPath); err != nil {
		t.Fatalf("EnsureParentDir() error = %v", err)
	}

	db, err := Open(context.Background(), OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	return db
}
