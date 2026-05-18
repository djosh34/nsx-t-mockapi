package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unicode"

	"go.uber.org/zap"
)

const managerDatabaseFileName = "nsx-t-mockapi.db"

const (
	managerCatalogMetadataTableSQL = `
CREATE TABLE IF NOT EXISTS manager_catalog_metadata (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
)`
	upsertManagerCatalogMetadataSQL = `
INSERT INTO manager_catalog_metadata(key, value)
VALUES ('manager_name', ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`
	selectManagerCatalogNameSQL = `
SELECT value
FROM manager_catalog_metadata
WHERE key = 'manager_name'`
)

var (
	// ErrManagerHostEmpty reports that a request did not identify an NSX manager host.
	ErrManagerHostEmpty = errors.New("manager host is empty")
	// ErrManagerHostUnsafe reports that a request host cannot be mapped to a local database path.
	ErrManagerHostUnsafe = errors.New("manager host is not safe for a database path")

	errManagerDatabasePoolNil    = errors.New("manager database pool is nil")
	errStaticManagerDatabaseNil  = errors.New("static manager database is nil")
	errManagerHostPortOutOfRange = errors.New("manager host port is out of range")
)

// ManagerDatabaseProvider resolves request hostnames to SQLite database connections.
type ManagerDatabaseProvider interface {
	ResolveManagerDatabase(ctx context.Context, host string) (ManagerDatabase, error)
}

// ManagerDatabase is an opened and bootstrapped SQLite database for one mock NSX manager.
type ManagerDatabase struct {
	Name string
	Host string
	Path string
	DB   *sql.DB
}

// ManagerInfo describes one local manager database without holding it open.
type ManagerInfo struct {
	Name string
	Path string
}

// ManagerCatalogOptions configures direct local manager database lifecycle operations.
type ManagerCatalogOptions struct {
	DataDir       string
	ManagerDir    string
	DBPath        string
	ResourceStore ResourceStoreOptions
	Logger        *zap.Logger
}

// ManagerCatalog owns direct local manager database lifecycle operations.
type ManagerCatalog struct {
	managerDir    string
	dbPath        string
	resourceStore ResourceStoreOptions
	logger        *zap.Logger
}

// ManagerDatabasePoolOptions configures lazy per-manager SQLite ownership.
type ManagerDatabasePoolOptions struct {
	BasePath      string
	ResourceStore ResourceStoreOptions
	Logger        *zap.Logger
}

// ManagerDatabasePool owns lazy SQLite connections for manager hostnames.
type ManagerDatabasePool struct {
	dataDir       string
	resourceStore ResourceStoreOptions
	logger        *zap.Logger

	mu        sync.Mutex
	databases map[string]ManagerDatabase
}

// NewManagerDatabasePool builds a lazy pool from the configured database path.
func NewManagerDatabasePool(opts ManagerDatabasePoolOptions) (*ManagerDatabasePool, error) {
	if opts.BasePath == "" {
		return nil, ErrEmptyPath
	}
	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	dataDir := filepath.Dir(opts.BasePath)
	if dataDir == "." || dataDir == "" {
		dataDir = "."
	}
	logger.Debug(
		"constructed manager database pool",
		zap.String("data_dir", dataDir),
		zap.String("base_path", opts.BasePath),
	)
	return &ManagerDatabasePool{
		dataDir:       dataDir,
		resourceStore: opts.ResourceStore,
		logger:        logger,
		databases:     map[string]ManagerDatabase{},
	}, nil
}

// NewManagerCatalog builds a direct lifecycle catalog for HTTP-compatible data directory layout.
func NewManagerCatalog(opts ManagerCatalogOptions) (ManagerCatalog, error) {
	storageOptions := 0
	if opts.DataDir != "" {
		storageOptions++
	}
	if opts.ManagerDir != "" {
		storageOptions++
	}
	if opts.DBPath != "" {
		storageOptions++
	}
	if storageOptions > 1 {
		return ManagerCatalog{}, fmt.Errorf(
			"%w: manager catalog storage options are mutually exclusive",
			ErrManagerHostUnsafe,
		)
	}
	managerDir := opts.ManagerDir
	if managerDir == "" && opts.DataDir != "" {
		managerDir = filepath.Join(opts.DataDir, "managers")
	}
	if managerDir == "" && opts.DBPath == "" {
		return ManagerCatalog{}, ErrEmptyPath
	}
	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	logger.Debug(
		"constructed manager catalog",
		zap.String("manager_dir", managerDir),
		zap.String("db_path", opts.DBPath),
	)
	return ManagerCatalog{
		managerDir:    managerDir,
		dbPath:        opts.DBPath,
		resourceStore: opts.ResourceStore,
		logger:        logger,
	}, nil
}

// List returns existing manager database files sorted by manager name.
func (c ManagerCatalog) List(ctx context.Context) ([]ManagerInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list manager databases context: %w", err)
	}
	if c.dbPath != "" {
		return c.listSingleDatabase(ctx)
	}
	return c.listManagerDirectory()
}

// Add creates or bootstraps a manager database and returns its stable path.
func (c ManagerCatalog) Add(ctx context.Context, name string) (ManagerInfo, error) {
	normalized, err := NormalizeManagerHost(name)
	if err != nil {
		return ManagerInfo{}, err
	}
	manager, err := OpenManagerDatabase(ctx, OpenManagerDatabaseOptions{
		Name:          normalized,
		Host:          normalized,
		Path:          c.pathForManager(normalized),
		ResourceStore: c.resourceStore,
		Logger:        c.logger,
	})
	if err != nil {
		return ManagerInfo{}, err
	}
	if err = writeManagerCatalogName(ctx, manager.DB, normalized); err != nil {
		closeErr := manager.DB.Close()
		if closeErr != nil {
			return ManagerInfo{}, fmt.Errorf("%w; close manager sqlite database %q: %w", err, manager.Path, closeErr)
		}
		return ManagerInfo{}, err
	}
	if closeErr := manager.DB.Close(); closeErr != nil {
		return ManagerInfo{}, fmt.Errorf("close manager sqlite database %q: %w", manager.Path, closeErr)
	}
	return ManagerInfo{Name: manager.Name, Path: manager.Path}, nil
}

// Clear removes all stored manager state and recreates a bootstrapped database at the same path.
func (c ManagerCatalog) Clear(ctx context.Context, name string) (ManagerInfo, error) {
	normalized, err := NormalizeManagerHost(name)
	if err != nil {
		return ManagerInfo{}, err
	}
	dbPath := c.pathForManager(normalized)
	c.logger.Info(
		"clearing manager sqlite database",
		zap.String("manager_name", normalized),
		zap.String("db_path", dbPath),
	)
	if err = removeSQLiteDatabaseFiles(dbPath); err != nil {
		return ManagerInfo{}, fmt.Errorf("clear manager sqlite database %q: %w", dbPath, err)
	}
	return c.Add(ctx, normalized)
}

// Delete removes a manager database and SQLite sidecars.
func (c ManagerCatalog) Delete(ctx context.Context, name string) (ManagerInfo, error) {
	if err := ctx.Err(); err != nil {
		return ManagerInfo{}, fmt.Errorf("delete manager database context: %w", err)
	}
	normalized, err := NormalizeManagerHost(name)
	if err != nil {
		return ManagerInfo{}, err
	}
	dbPath := c.pathForManager(normalized)
	c.logger.Info(
		"deleting manager sqlite database",
		zap.String("manager_name", normalized),
		zap.String("db_path", dbPath),
	)
	if err = removeSQLiteDatabaseFiles(dbPath); err != nil {
		return ManagerInfo{}, fmt.Errorf("delete manager sqlite database %q: %w", dbPath, err)
	}
	managerDir := filepath.Dir(dbPath)
	if err = os.Remove(managerDir); err != nil && !errors.Is(err, os.ErrNotExist) && !isDirectoryNotEmpty(err) {
		return ManagerInfo{}, fmt.Errorf("remove manager directory %q: %w", managerDir, err)
	}
	return ManagerInfo{Name: normalized, Path: dbPath}, nil
}

// OpenSelected opens and bootstraps one manager database for direct local administration.
func (c ManagerCatalog) OpenSelected(ctx context.Context, name string) (ManagerDatabase, error) {
	normalized, err := c.normalizeSelectedName(ctx, name)
	if err != nil {
		return ManagerDatabase{}, err
	}
	manager, err := OpenManagerDatabase(ctx, OpenManagerDatabaseOptions{
		Name:          normalized,
		Host:          normalized,
		Path:          c.pathForManager(normalized),
		ResourceStore: c.resourceStore,
		Logger:        c.logger,
	})
	if err != nil {
		return ManagerDatabase{}, err
	}
	if writeErr := writeManagerCatalogName(ctx, manager.DB, normalized); writeErr != nil {
		closeErr := manager.DB.Close()
		if closeErr != nil {
			return ManagerDatabase{}, fmt.Errorf("%w; close manager sqlite database %q: %w", writeErr, manager.Path, closeErr)
		}
		return ManagerDatabase{}, writeErr
	}
	return manager, nil
}

func (c ManagerCatalog) normalizeSelectedName(ctx context.Context, name string) (string, error) {
	if name != "" {
		return NormalizeManagerHost(name)
	}
	if c.dbPath == "" {
		return "", ErrManagerHostEmpty
	}
	if _, err := os.Stat(c.dbPath); err == nil {
		return readManagerCatalogName(ctx, c.dbPath)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stat manager sqlite database %q: %w", c.dbPath, err)
	}
	return fallbackSingleDatabaseManagerName(c.dbPath)
}

func (c ManagerCatalog) listManagerDirectory() ([]ManagerInfo, error) {
	c.logger.Debug("listing manager databases", zap.String("manager_dir", c.managerDir))
	entries, err := os.ReadDir(c.managerDir)
	if errors.Is(err, os.ErrNotExist) {
		return []ManagerInfo{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read manager directory %q: %w", c.managerDir, err)
	}

	managers := make([]ManagerInfo, 0, len(entries))
	for _, entry := range entries {
		info, include, entryErr := c.managerInfoFromDirectoryEntry(entry)
		if entryErr != nil {
			return nil, entryErr
		}
		if !include {
			continue
		}
		managers = append(managers, info)
	}
	sort.Slice(managers, func(i int, j int) bool {
		return managers[i].Name < managers[j].Name
	})
	c.logger.Debug("listed manager databases", zap.Int("manager_count", len(managers)))
	return managers, nil
}

func (c ManagerCatalog) managerInfoFromDirectoryEntry(entry os.DirEntry) (ManagerInfo, bool, error) {
	if !entry.IsDir() {
		return ManagerInfo{}, false, nil
	}
	name, normalizeErr := NormalizeManagerHost(entry.Name())
	if normalizeErr != nil || name != entry.Name() {
		c.logger.Debug(
			"skipping unsafe manager directory",
			zap.String("manager_name", entry.Name()),
			zap.Error(normalizeErr),
		)
		return ManagerInfo{}, false, nil
	}
	info := ManagerInfo{Name: name, Path: managerDatabasePathInRoot(c.managerDir, name)}
	if _, statErr := os.Stat(info.Path); errors.Is(statErr, os.ErrNotExist) {
		return ManagerInfo{}, false, nil
	} else if statErr != nil {
		return ManagerInfo{}, false, fmt.Errorf("stat manager sqlite database %q: %w", info.Path, statErr)
	}
	return info, true, nil
}

func (c ManagerCatalog) pathForManager(name string) string {
	if c.dbPath != "" {
		return c.dbPath
	}
	return managerDatabasePathInRoot(c.managerDir, name)
}

func (c ManagerCatalog) listSingleDatabase(ctx context.Context) ([]ManagerInfo, error) {
	c.logger.Debug("listing single manager database", zap.String("db_path", c.dbPath))
	if _, err := os.Stat(c.dbPath); errors.Is(err, os.ErrNotExist) {
		return []ManagerInfo{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("stat manager sqlite database %q: %w", c.dbPath, err)
	}
	name, err := readManagerCatalogName(ctx, c.dbPath)
	if err != nil {
		return nil, err
	}
	return []ManagerInfo{{Name: name, Path: c.dbPath}}, nil
}

func writeManagerCatalogName(ctx context.Context, db *sql.DB, name string) error {
	if _, err := db.ExecContext(ctx, managerCatalogMetadataTableSQL); err != nil {
		return fmt.Errorf("create manager catalog metadata table: %w", err)
	}
	result, err := db.ExecContext(ctx, upsertManagerCatalogMetadataSQL, name)
	if err != nil {
		return fmt.Errorf("write manager catalog metadata: %w", err)
	}
	if _, err = result.RowsAffected(); err != nil {
		return fmt.Errorf("read manager catalog metadata rows affected: %w", err)
	}
	return nil
}

func readManagerCatalogName(ctx context.Context, dbPath string) (name string, retErr error) {
	db, err := Open(ctx, OpenOptions{Path: dbPath})
	if err != nil {
		return "", fmt.Errorf("open manager sqlite database %q to read metadata: %w", dbPath, err)
	}
	defer func() {
		closeErr := db.Close()
		if closeErr == nil {
			return
		}
		if retErr == nil {
			retErr = fmt.Errorf("close manager sqlite database %q after metadata read: %w", dbPath, closeErr)
			return
		}
		retErr = fmt.Errorf("%w; close manager sqlite database %q after metadata read: %w", retErr, dbPath, closeErr)
	}()

	exists, err := managerCatalogMetadataTableExists(ctx, db)
	if err != nil {
		return "", err
	}
	if !exists {
		return fallbackSingleDatabaseManagerName(dbPath)
	}
	err = db.QueryRowContext(ctx, selectManagerCatalogNameSQL).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return fallbackSingleDatabaseManagerName(dbPath)
	}
	if err != nil {
		return "", fmt.Errorf("query manager catalog metadata %q: %w", dbPath, err)
	}
	return name, nil
}

func managerCatalogMetadataTableExists(ctx context.Context, db *sql.DB) (bool, error) {
	var found int
	err := db.QueryRowContext(
		ctx,
		"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'manager_catalog_metadata'",
	).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("query manager catalog metadata table: %w", err)
	}
	return found == 1, nil
}

func fallbackSingleDatabaseManagerName(dbPath string) (string, error) {
	base := strings.TrimSuffix(filepath.Base(dbPath), filepath.Ext(dbPath))
	return NormalizeManagerHost(base)
}

func isDirectoryNotEmpty(err error) bool {
	return errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST)
}

func removeSQLiteDatabaseFiles(dbPath string) error {
	for _, path := range sqliteDatabaseFiles(dbPath) {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove sqlite file %q: %w", path, err)
		}
	}
	return nil
}

func sqliteDatabaseFiles(dbPath string) []string {
	return []string{dbPath, dbPath + "-wal", dbPath + "-shm"}
}

// ResolveManagerDatabase returns a bootstrapped database for host, opening it lazily when needed.
func (p *ManagerDatabasePool) ResolveManagerDatabase(ctx context.Context, host string) (ManagerDatabase, error) {
	if p == nil {
		return ManagerDatabase{}, errManagerDatabasePoolNil
	}
	name, err := NormalizeManagerHost(host)
	if err != nil {
		return ManagerDatabase{}, err
	}

	p.mu.Lock()
	if db, ok := p.databases[name]; ok {
		p.mu.Unlock()
		p.logger.Debug(
			"resolved cached manager database",
			zap.String("manager_name", db.Name),
			zap.String("request_host", host),
			zap.String("db_path", db.Path),
		)
		return db, nil
	}
	p.mu.Unlock()

	db, err := p.openManagerDatabase(ctx, host, name)
	if err != nil {
		return ManagerDatabase{}, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.databases[name]; ok {
		if closeErr := db.DB.Close(); closeErr != nil {
			return ManagerDatabase{}, fmt.Errorf("close duplicate manager database %q: %w", db.Path, closeErr)
		}
		p.logger.Debug(
			"resolved cached manager database after concurrent open",
			zap.String("manager_name", existing.Name),
			zap.String("request_host", host),
			zap.String("db_path", existing.Path),
		)
		return existing, nil
	}
	p.databases[name] = db
	return db, nil
}

// Close closes every opened manager database and reports all close failures.
func (p *ManagerDatabasePool) Close() error {
	if p == nil {
		return nil
	}

	p.mu.Lock()
	databases := make([]ManagerDatabase, 0, len(p.databases))
	for _, db := range p.databases {
		databases = append(databases, db)
	}
	p.databases = map[string]ManagerDatabase{}
	p.mu.Unlock()

	var errs []error
	for _, manager := range databases {
		p.logger.Info(
			"closing manager sqlite database",
			zap.String("manager_name", manager.Name),
			zap.String("db_path", manager.Path),
		)
		if err := manager.DB.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close manager sqlite database %q: %w", manager.Path, err))
		}
	}
	return errors.Join(errs...)
}

// ManagerDatabasePath returns the deterministic SQLite file path for a manager.
func ManagerDatabasePath(dataDir string, managerName string) string {
	return managerDatabasePathInRoot(filepath.Join(dataDir, "managers"), managerName)
}

func managerDatabasePathInRoot(managerDir string, managerName string) string {
	return filepath.Join(managerDir, managerName, managerDatabaseFileName)
}

// NormalizeManagerHost maps an HTTP Host header value to a path-safe manager name.
func NormalizeManagerHost(host string) (string, error) {
	candidate := strings.TrimSpace(host)
	if candidate == "" {
		return "", ErrManagerHostEmpty
	}
	candidate, err := stripManagerHostPort(candidate, host)
	if err != nil {
		return "", err
	}
	candidate = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(candidate)), ".")
	if candidate == "" {
		return "", ErrManagerHostEmpty
	}
	return validateManagerName(candidate, host)
}

func stripManagerHostPort(candidate string, originalHost string) (string, error) {
	splitHost, splitPort, err := net.SplitHostPort(candidate)
	if err != nil {
		if strings.Contains(candidate, ":") {
			return "", fmt.Errorf("%w: %q", ErrManagerHostUnsafe, originalHost)
		}
		return candidate, nil
	}
	port, parseErr := strconv.Atoi(splitPort)
	if parseErr != nil {
		return "", fmt.Errorf("%w: %q", ErrManagerHostUnsafe, originalHost)
	}
	if port < 0 || port > 65535 {
		return "", fmt.Errorf("%w: %w: %q", ErrManagerHostUnsafe, errManagerHostPortOutOfRange, originalHost)
	}
	return splitHost, nil
}

func validateManagerName(candidate string, originalHost string) (string, error) {
	if isReservedManagerName(candidate) {
		return "", fmt.Errorf("%w: %q", ErrManagerHostUnsafe, originalHost)
	}
	if strings.ContainsAny(candidate, `/\:`) {
		return "", fmt.Errorf("%w: %q", ErrManagerHostUnsafe, originalHost)
	}
	if containsUnsafeManagerNameRune(candidate) {
		return "", fmt.Errorf("%w: %q", ErrManagerHostUnsafe, originalHost)
	}
	if !managerNameIsCleanRelativePath(candidate) {
		return "", fmt.Errorf("%w: %q", ErrManagerHostUnsafe, originalHost)
	}
	return candidate, nil
}

func isReservedManagerName(candidate string) bool {
	return candidate == "." || candidate == ".." || strings.Contains(candidate, "..")
}

func containsUnsafeManagerNameRune(candidate string) bool {
	for _, r := range candidate {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '.' {
			return true
		}
	}
	return false
}

func managerNameIsCleanRelativePath(candidate string) bool {
	cleaned := filepath.Clean(candidate)
	return cleaned == candidate && !filepath.IsAbs(candidate)
}

// StaticManagerDatabaseProvider adapts a pre-opened database for older tests and focused handlers.
type StaticManagerDatabaseProvider struct {
	manager ManagerDatabase
}

// NewStaticManagerDatabaseProvider returns a provider that always resolves to db.
func NewStaticManagerDatabaseProvider(db *sql.DB) StaticManagerDatabaseProvider {
	return StaticManagerDatabaseProvider{
		manager: ManagerDatabase{Name: "default", Host: "default", Path: "", DB: db},
	}
}

// ResolveManagerDatabase returns the static database regardless of host.
func (p StaticManagerDatabaseProvider) ResolveManagerDatabase(_ context.Context, host string) (ManagerDatabase, error) {
	if p.manager.DB == nil {
		return ManagerDatabase{}, errStaticManagerDatabaseNil
	}
	manager := p.manager
	manager.Host = host
	return manager, nil
}

func (p *ManagerDatabasePool) openManagerDatabase(
	ctx context.Context,
	host string,
	name string,
) (ManagerDatabase, error) {
	return OpenManagerDatabase(ctx, OpenManagerDatabaseOptions{
		Name:          name,
		Host:          host,
		Path:          ManagerDatabasePath(p.dataDir, name),
		ResourceStore: p.resourceStore,
		Logger:        p.logger,
	})
}

// OpenManagerDatabaseOptions configures opening and bootstrapping one manager database.
type OpenManagerDatabaseOptions struct {
	Name          string
	Host          string
	Path          string
	ResourceStore ResourceStoreOptions
	Logger        *zap.Logger
}

// OpenManagerDatabase opens and bootstraps one manager SQLite database.
func OpenManagerDatabase(ctx context.Context, opts OpenManagerDatabaseOptions) (ManagerDatabase, error) {
	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	dbPath := opts.Path
	if err := EnsureParentDir(dbPath); err != nil {
		return ManagerDatabase{}, fmt.Errorf("ensure manager sqlite parent directory: %w", err)
	}
	logger.Info(
		"opening manager sqlite database",
		zap.String("manager_name", opts.Name),
		zap.String("request_host", opts.Host),
		zap.String("db_path", dbPath),
	)
	db, err := Open(ctx, OpenOptions{Path: dbPath})
	if err != nil {
		return ManagerDatabase{}, fmt.Errorf("open manager sqlite database %q: %w", dbPath, err)
	}
	manager := ManagerDatabase{Name: opts.Name, Host: opts.Host, Path: dbPath, DB: db}
	if err = bootstrapManagerDatabase(ctx, logger, manager, opts.ResourceStore); err != nil {
		closeErr := db.Close()
		if closeErr != nil {
			return ManagerDatabase{}, fmt.Errorf("%w; close manager sqlite database %q: %w", err, dbPath, closeErr)
		}
		return ManagerDatabase{}, err
	}
	logger.Info(
		"opened manager sqlite database",
		zap.String("manager_name", opts.Name),
		zap.String("request_host", opts.Host),
		zap.String("db_path", dbPath),
	)
	return manager, nil
}

func bootstrapManagerDatabase(
	ctx context.Context,
	logger *zap.Logger,
	manager ManagerDatabase,
	resourceStore ResourceStoreOptions,
) error {
	report, err := VerifyCapabilities(ctx, manager.DB)
	if err != nil {
		return fmt.Errorf("verify manager sqlite capabilities: %w", err)
	}
	logger.Debug(
		"verified manager sqlite capabilities",
		zap.String("manager_name", manager.Name),
		zap.String("db_path", manager.Path),
		zap.String("sqlite_version", report.SQLiteVersion),
		zap.Bool("raw_sql", report.RawSQL),
		zap.Bool("jsonb", report.JSONB),
		zap.Bool("fts5", report.FTS5),
	)

	logger.Info(
		"starting manager user bootstrap",
		zap.String("manager_name", manager.Name),
		zap.String("db_path", manager.Path),
	)
	userReport, err := NewUserStore(manager.DB).EnsureBootstrap(ctx)
	if err != nil {
		return fmt.Errorf("bootstrap manager users: %w", err)
	}
	logger.Debug(
		"manager user bootstrap completed",
		zap.String("manager_name", manager.Name),
		zap.Int("roles_ensured", userReport.RolesEnsured),
		zap.Bool("default_admin_created", userReport.DefaultAdminCreated),
	)

	resourceStore.Logger = logger
	logger.Info(
		"starting manager resource bootstrap",
		zap.String("manager_name", manager.Name),
		zap.String("db_path", manager.Path),
	)
	if err = NewResourceStore(manager.DB, resourceStore).EnsureBootstrap(ctx); err != nil {
		return fmt.Errorf("bootstrap manager resource store: %w", err)
	}
	logger.Debug(
		"manager resource bootstrap completed",
		zap.String("manager_name", manager.Name),
		zap.String("db_path", manager.Path),
	)
	return nil
}
