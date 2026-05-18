package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const managerPoolTestManagerOne = "nsx-t-1"

func TestNormalizeManagerHostAcceptsDocumentedSafeHostForms(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		host string
		want string
	}{
		{name: "plain", host: "nsx-t-1", want: "nsx-t-1"},
		{name: "host port", host: "nsx-t-1:9443", want: "nsx-t-1"},
		{name: "uppercase", host: "NSX-T-2", want: "nsx-t-2"},
		{name: "trailing dot", host: "nsx-t-3.", want: "nsx-t-3"},
		{name: "fqdn", host: "nsx-t-4.example.test", want: "nsx-t-4.example.test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := NormalizeManagerHost(tc.host)
			if err != nil {
				t.Fatalf("NormalizeManagerHost(%q) error = %v", tc.host, err)
			}
			if got != tc.want {
				t.Fatalf("NormalizeManagerHost(%q) = %q, want %q", tc.host, got, tc.want)
			}
		})
	}
}

func TestNormalizeManagerHostRejectsEmptyAndPathUnsafeHosts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		host    string
		wantErr error
	}{
		{name: "empty", host: "", wantErr: ErrManagerHostEmpty},
		{name: "only trailing dot", host: ".", wantErr: ErrManagerHostEmpty},
		{name: "slash", host: "nsx/t-1", wantErr: ErrManagerHostUnsafe},
		{name: "backslash", host: `nsx\t-1`, wantErr: ErrManagerHostUnsafe},
		{name: "path traversal", host: "nsx..t-1", wantErr: ErrManagerHostUnsafe},
		{name: "colon without valid port", host: "nsx-t-1:notaport", wantErr: ErrManagerHostUnsafe},
		{name: "underscore", host: "nsx_t_1", wantErr: ErrManagerHostUnsafe},
		{name: "space", host: "nsx t 1", wantErr: ErrManagerHostUnsafe},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := NormalizeManagerHost(tc.host)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("NormalizeManagerHost(%q) = %q error %v, want %v", tc.host, got, err, tc.wantErr)
			}
		})
	}
}

func TestManagerDatabasePoolCreatesIndependentBootstrappedDatabaseFiles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := filepath.Join(t.TempDir(), "data")
	pool := newManagerPoolTestPool(t, dataDir)

	nsxOne, err := pool.ResolveManagerDatabase(ctx, managerPoolTestManagerOne)
	if err != nil {
		t.Fatalf("ResolveManagerDatabase(nsx-t-1) error = %v", err)
	}
	nsxTwo, err := pool.ResolveManagerDatabase(ctx, "nsx-t-2")
	if err != nil {
		t.Fatalf("ResolveManagerDatabase(nsx-t-2) error = %v", err)
	}

	requireManagerPoolTestPath(t, nsxOne, dataDir, managerPoolTestManagerOne)
	requireManagerPoolTestPath(t, nsxTwo, dataDir, "nsx-t-2")
	if nsxOne.DB == nsxTwo.DB {
		t.Fatal("nsx-t-1 and nsx-t-2 DB pointers match, want independent connections")
	}
	requireManagerPoolTestDefaultAdmin(t, nsxOne)
	if !tableExists(t, nsxTwo.DB, "resources") {
		t.Fatal("nsx-t-2 resources table missing after ResolveManagerDatabase")
	}
}

func TestManagerDatabasePoolReturnsCachedDatabaseForNormalizedHost(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := newManagerPoolTestPool(t, filepath.Join(t.TempDir(), "data"))

	first, err := pool.ResolveManagerDatabase(ctx, "NSX-T-1:443")
	if err != nil {
		t.Fatalf("ResolveManagerDatabase(first) error = %v", err)
	}
	second, err := pool.ResolveManagerDatabase(ctx, managerPoolTestManagerOne)
	if err != nil {
		t.Fatalf("ResolveManagerDatabase(second) error = %v", err)
	}

	if first.Name != managerPoolTestManagerOne || second.Name != managerPoolTestManagerOne {
		t.Fatalf("manager names = %q and %q, want normalized nsx-t-1", first.Name, second.Name)
	}
	if first.DB != second.DB {
		t.Fatal("cached manager DB pointers differ, want same connection")
	}
	if first.Path != second.Path {
		t.Fatalf("cached manager paths = %q and %q, want same path", first.Path, second.Path)
	}
}

func TestManagerDatabasePoolResolveRejectsNilPoolAndInvalidHost(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	var nilPool *ManagerDatabasePool
	_, err := nilPool.ResolveManagerDatabase(ctx, managerPoolTestManagerOne)
	if !errors.Is(err, errManagerDatabasePoolNil) {
		t.Fatalf("nil pool ResolveManagerDatabase() error = %v, want %v", err, errManagerDatabasePoolNil)
	}

	pool := newManagerPoolTestPool(t, filepath.Join(t.TempDir(), "data"))
	_, err = pool.ResolveManagerDatabase(ctx, "nsx/t-1")
	if !errors.Is(err, ErrManagerHostUnsafe) {
		t.Fatalf("invalid host ResolveManagerDatabase() error = %v, want %v", err, ErrManagerHostUnsafe)
	}
}

func TestManagerDatabasePoolResolveReportsParentDirectoryErrors(t *testing.T) {
	t.Parallel()

	dataPath := filepath.Join(t.TempDir(), "data-file")
	if err := os.WriteFile(dataPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	pool, err := NewManagerDatabasePool(ManagerDatabasePoolOptions{
		BasePath: filepath.Join(dataPath, "nsx-t-mockapi.db"),
	})
	if err != nil {
		t.Fatalf("NewManagerDatabasePool() error = %v", err)
	}

	_, err = pool.ResolveManagerDatabase(context.Background(), managerPoolTestManagerOne)
	if err == nil {
		t.Fatal("ResolveManagerDatabase() error = nil, want parent directory error")
	}
}

func TestManagerDatabasePoolResolveReportsOpenErrors(t *testing.T) {
	t.Parallel()

	dataDir := filepath.Join(t.TempDir(), "data")
	dbDir := filepath.Join(dataDir, "managers", "nsx-t-1", "nsx-t-mockapi.db")
	if err := os.MkdirAll(dbDir, 0o750); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	pool, err := NewManagerDatabasePool(ManagerDatabasePoolOptions{
		BasePath: filepath.Join(dataDir, "nsx-t-mockapi.db"),
	})
	if err != nil {
		t.Fatalf("NewManagerDatabasePool() error = %v", err)
	}

	_, err = pool.ResolveManagerDatabase(context.Background(), managerPoolTestManagerOne)
	if err == nil {
		t.Fatal("ResolveManagerDatabase() error = nil, want sqlite open error")
	}
}

func TestStaticManagerDatabaseProviderResolvesExistingDatabase(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openThenCloseDB(t)

	provider := NewStaticManagerDatabaseProvider(db)
	manager, err := provider.ResolveManagerDatabase(ctx, managerPoolTestManagerOne)
	if err != nil {
		t.Fatalf("ResolveManagerDatabase() error = %v", err)
	}
	if manager.DB != db {
		t.Fatal("ResolveManagerDatabase() DB does not match static DB")
	}
	if manager.Host != managerPoolTestManagerOne {
		t.Fatalf("ResolveManagerDatabase() host = %q, want nsx-t-1", manager.Host)
	}
}

func TestStaticManagerDatabaseProviderRejectsNilDatabase(t *testing.T) {
	t.Parallel()

	provider := NewStaticManagerDatabaseProvider(nil)
	_, err := provider.ResolveManagerDatabase(context.Background(), managerPoolTestManagerOne)
	if !errors.Is(err, errStaticManagerDatabaseNil) {
		t.Fatalf("ResolveManagerDatabase() error = %v, want %v", err, errStaticManagerDatabaseNil)
	}
}

func newManagerPoolTestPool(t *testing.T, dataDir string) *ManagerDatabasePool {
	t.Helper()

	pool, err := NewManagerDatabasePool(ManagerDatabasePoolOptions{
		BasePath: filepath.Join(dataDir, "nsx-t-mockapi.db"),
	})
	if err != nil {
		t.Fatalf("NewManagerDatabasePool() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := pool.Close(); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	})
	return pool
}

func requireManagerPoolTestPath(t *testing.T, manager ManagerDatabase, dataDir string, name string) {
	t.Helper()

	wantPath := filepath.Join(dataDir, "managers", name, "nsx-t-mockapi.db")
	if manager.Path != wantPath {
		t.Fatalf("%s path = %q, want %q", name, manager.Path, wantPath)
	}
}

func requireManagerPoolTestDefaultAdmin(t *testing.T, manager ManagerDatabase) {
	t.Helper()

	_, found, err := NewUserStore(manager.DB).FindUser(context.Background(), DefaultAdminUsername)
	if err != nil {
		t.Fatalf("FindUser(%s) error = %v", manager.Name, err)
	}
	if !found {
		t.Fatalf("FindUser(%s) found = false, want true", manager.Name)
	}
}
