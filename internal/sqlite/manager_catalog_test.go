package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestManagerCatalogDataDirLifecycle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := filepath.Join(t.TempDir(), "data")
	catalog := newManagerCatalogForTest(t, ManagerCatalogOptions{DataDir: dataDir})

	added, err := catalog.Add(ctx, "nsx-t-1")
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	wantPath := filepath.Join(dataDir, "managers", "nsx-t-1", "nsx-t-mockapi.db")
	if added.Path != wantPath {
		t.Fatalf("Add() path = %q, want %q", added.Path, wantPath)
	}

	managers, err := catalog.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	requireManagerCatalogList(t, managers, "nsx-t-1", wantPath)

	cleared, err := catalog.Clear(ctx, "nsx-t-1")
	if err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	if cleared.Path != wantPath {
		t.Fatalf("Clear() path = %q, want %q", cleared.Path, wantPath)
	}
	if !managerCatalogTestFileExists(t, wantPath) {
		t.Fatalf("database %q missing after Clear()", wantPath)
	}

	deleted, err := catalog.Delete(ctx, "nsx-t-1")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.Path != wantPath {
		t.Fatalf("Delete() path = %q, want %q", deleted.Path, wantPath)
	}
	if managerCatalogTestFileExists(t, wantPath) {
		t.Fatalf("database %q exists after Delete()", wantPath)
	}
}

func TestManagerCatalogManagerDirAndSingleDBModes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	managerDir := filepath.Join(t.TempDir(), "managers")
	managerDirCatalog := newManagerCatalogForTest(t, ManagerCatalogOptions{ManagerDir: managerDir})
	managerDirInfo, err := managerDirCatalog.Add(ctx, "nsx-t-1")
	if err != nil {
		t.Fatalf("manager-dir Add() error = %v", err)
	}
	requireManagerCatalogList(t, mustListManagers(ctx, t, managerDirCatalog), "nsx-t-1", managerDirInfo.Path)

	dbPath := filepath.Join(t.TempDir(), "single.db")
	singleDBCatalog := newManagerCatalogForTest(t, ManagerCatalogOptions{DBPath: dbPath})
	singleInfo, err := singleDBCatalog.Add(ctx, "nsx-t-2")
	if err != nil {
		t.Fatalf("--db Add() error = %v", err)
	}
	if singleInfo.Path != dbPath {
		t.Fatalf("--db Add() path = %q, want %q", singleInfo.Path, dbPath)
	}
	requireManagerCatalogList(t, mustListManagers(ctx, t, singleDBCatalog), "nsx-t-2", dbPath)
}

func TestNewManagerCatalogRejectsEmptyAndAmbiguousStorage(t *testing.T) {
	t.Parallel()

	_, err := NewManagerCatalog(ManagerCatalogOptions{})
	if !errors.Is(err, ErrEmptyPath) {
		t.Fatalf("NewManagerCatalog(empty) error = %v, want %v", err, ErrEmptyPath)
	}

	_, err = NewManagerCatalog(ManagerCatalogOptions{
		DataDir: filepath.Join(t.TempDir(), "data"),
		DBPath:  filepath.Join(t.TempDir(), "single.db"),
	})
	if err == nil {
		t.Fatal("NewManagerCatalog(ambiguous) error = nil, want error")
	}
}

func newManagerCatalogForTest(t *testing.T, opts ManagerCatalogOptions) ManagerCatalog {
	t.Helper()

	catalog, err := NewManagerCatalog(opts)
	if err != nil {
		t.Fatalf("NewManagerCatalog() error = %v", err)
	}
	return catalog
}

func mustListManagers(ctx context.Context, t *testing.T, catalog ManagerCatalog) []ManagerInfo {
	t.Helper()

	managers, err := catalog.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	return managers
}

func requireManagerCatalogList(t *testing.T, managers []ManagerInfo, name string, path string) {
	t.Helper()

	if len(managers) != 1 {
		t.Fatalf("List() returned %d managers, want 1", len(managers))
	}
	if managers[0].Name != name {
		t.Fatalf("List()[0].Name = %q, want %q", managers[0].Name, name)
	}
	if managers[0].Path != path {
		t.Fatalf("List()[0].Path = %q, want %q", managers[0].Path, path)
	}
}

func managerCatalogTestFileExists(t *testing.T, path string) bool {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false
		}
		t.Fatalf("Stat(%q) error = %v", path, err)
	}
	return !info.IsDir()
}
