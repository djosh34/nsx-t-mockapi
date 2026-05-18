package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const (
	userBootstrapMigrationVersion = "001_create_users_roles"
	roleReadOnlySortOrder         = 10
	roleReadWriteSortOrder        = 20
	roleAdminSortOrder            = 30

	createSchemaMigrationsSQL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version TEXT PRIMARY KEY,
	applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`

	createRolesSQL = `
CREATE TABLE IF NOT EXISTS roles (
	role_id TEXT PRIMARY KEY,
	display_name TEXT NOT NULL,
	sort_order INTEGER NOT NULL UNIQUE
)`

	createUsersSQL = `
CREATE TABLE IF NOT EXISTS users (
	username TEXT PRIMARY KEY,
	password TEXT NOT NULL,
	role_id TEXT NOT NULL REFERENCES roles(role_id) ON UPDATE RESTRICT ON DELETE RESTRICT
)`

	insertSchemaMigrationSQL = `
INSERT INTO schema_migrations(version)
VALUES (?)
ON CONFLICT(version) DO NOTHING`

	upsertRoleSQL = `
INSERT INTO roles(role_id, display_name, sort_order)
VALUES (?, ?, ?)
ON CONFLICT(role_id) DO UPDATE SET
	display_name = excluded.display_name,
	sort_order = excluded.sort_order`

	insertDefaultAdminSQL = `
INSERT INTO users(username, password, role_id)
VALUES (?, ?, ?)
ON CONFLICT(username) DO NOTHING`

	listUsersSQL = `
SELECT username, role_id
FROM users
ORDER BY username`

	insertUserSQL = `
INSERT INTO users(username, password, role_id)
VALUES (?, ?, ?)`

	findUserSQL = `
SELECT username, password, role_id
FROM users
WHERE username = ?`

	deleteUserSQL = `
DELETE FROM users
WHERE username = ?`
)

const (
	// RoleReadOnly allows read-only API access.
	RoleReadOnly Role = "read-only"
	// RoleReadWrite allows read and write API access.
	RoleReadWrite Role = "read-write"
	// RoleAdmin allows full administrative API access.
	RoleAdmin Role = "admin"
)

const (
	// DefaultAdminUsername is the fixed bootstrap user expected by this mock.
	DefaultAdminUsername = "nsx_admin"
	// DefaultAdminPassword is stored raw by request for later Basic auth validation.
	DefaultAdminPassword = "nsx_password"
)

// Role identifies a persisted local NSX-T user role.
type Role string

// User is the local SQLite representation used by authentication and metadata.
type User struct {
	Username string
	Password string
	Role     Role
}

// UserSummary is the password-free user shape for administration views.
type UserSummary struct {
	Username string
	Role     Role
}

// UserBootstrapReport describes the idempotent startup bootstrap result.
type UserBootstrapReport struct {
	RolesEnsured        int
	DefaultAdminCreated bool
}

// UserStore owns local SQLite user schema, bootstrap, and lookup behavior.
type UserStore struct {
	db *sql.DB
}

type builtInRole struct {
	role        Role
	displayName string
	sortOrder   int
}

var builtInRoles = []builtInRole{
	{role: RoleReadOnly, displayName: "Read Only", sortOrder: roleReadOnlySortOrder},
	{role: RoleReadWrite, displayName: "Read Write", sortOrder: roleReadWriteSortOrder},
	{role: RoleAdmin, displayName: "Admin", sortOrder: roleAdminSortOrder},
}

var (
	errUserPasswordRequired = errors.New("user password is required")
	errUserRoleUnknown      = errors.New("user role is unknown")
	errUsernameRequired     = errors.New("username is required")
	errUsernameUnsafe       = errors.New("username is not safe")
)

// NewUserStore creates a user persistence boundary over db.
func NewUserStore(db *sql.DB) UserStore {
	return UserStore{db: db}
}

// EnsureBootstrap creates required user tables and idempotently seeds roles and nsx_admin.
func (s UserStore) EnsureBootstrap(ctx context.Context) (report UserBootstrapReport, retErr error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return report, fmt.Errorf("begin user bootstrap transaction: %w", err)
	}
	defer func() {
		if retErr == nil {
			return
		}
		rollbackErr := tx.Rollback()
		if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			retErr = fmt.Errorf("%w; rollback user bootstrap transaction: %w", retErr, rollbackErr)
		}
	}()

	err = ensureUserSchema(ctx, tx)
	if err != nil {
		return report, err
	}

	rolesEnsured, err := seedBuiltInRoles(ctx, tx)
	if err != nil {
		return report, err
	}
	report.RolesEnsured = rolesEnsured

	created, err := seedDefaultAdmin(ctx, tx)
	if err != nil {
		return report, err
	}
	report.DefaultAdminCreated = created

	if err = tx.Commit(); err != nil {
		return report, fmt.Errorf("commit user bootstrap transaction: %w", err)
	}

	return report, nil
}

// FindUser looks up a local user by username.
func (s UserStore) FindUser(ctx context.Context, username string) (user User, found bool, retErr error) {
	stmt, err := s.db.PrepareContext(ctx, findUserSQL)
	if err != nil {
		return User{}, false, fmt.Errorf("prepare find user: %w", err)
	}
	defer func() {
		closeErr := stmt.Close()
		if closeErr != nil && retErr == nil {
			retErr = fmt.Errorf("close find user statement: %w", closeErr)
		}
	}()

	err = stmt.QueryRowContext(ctx, username).Scan(&user.Username, &user.Password, &user.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, fmt.Errorf("query user %q: %w", username, err)
	}

	return user, true, nil
}

// ListUsers returns password-free local users sorted by username.
func (s UserStore) ListUsers(ctx context.Context) (users []UserSummary, retErr error) {
	rows, err := s.db.QueryContext(ctx, listUsersSQL)
	if err != nil {
		return nil, fmt.Errorf("query users: %w", err)
	}
	defer func() {
		closeErr := rows.Close()
		if closeErr == nil {
			return
		}
		if retErr == nil {
			retErr = fmt.Errorf("close list users rows: %w", closeErr)
			return
		}
		retErr = fmt.Errorf("%w; close list users rows: %w", retErr, closeErr)
	}()

	for rows.Next() {
		var user UserSummary
		if scanErr := rows.Scan(&user.Username, &user.Role); scanErr != nil {
			return nil, fmt.Errorf("scan user summary: %w", scanErr)
		}
		users = append(users, user)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user summaries: %w", err)
	}
	return users, nil
}

// AddUser inserts a local user with raw password storage for Basic auth validation.
func (s UserStore) AddUser(ctx context.Context, user User) (UserSummary, error) {
	if err := ValidateUsername(user.Username); err != nil {
		return UserSummary{}, err
	}
	if err := ValidateRole(user.Role); err != nil {
		return UserSummary{}, err
	}
	if user.Password == "" {
		return UserSummary{}, errUserPasswordRequired
	}
	result, err := s.db.ExecContext(ctx, insertUserSQL, user.Username, user.Password, string(user.Role))
	if err != nil {
		return UserSummary{}, fmt.Errorf("insert user %q: %w", user.Username, err)
	}
	if _, err = result.RowsAffected(); err != nil {
		return UserSummary{}, fmt.Errorf("read inserted user %q rows affected: %w", user.Username, err)
	}
	return UserSummary{Username: user.Username, Role: user.Role}, nil
}

// DeleteUser removes a local user and returns its password-free previous state.
func (s UserStore) DeleteUser(ctx context.Context, username string) (UserSummary, error) {
	if err := ValidateUsername(username); err != nil {
		return UserSummary{}, err
	}
	existing, found, err := s.FindUser(ctx, username)
	if err != nil {
		return UserSummary{}, err
	}
	if !found {
		return UserSummary{}, sql.ErrNoRows
	}
	result, err := s.db.ExecContext(ctx, deleteUserSQL, username)
	if err != nil {
		return UserSummary{}, fmt.Errorf("delete user %q: %w", username, err)
	}
	if _, err = result.RowsAffected(); err != nil {
		return UserSummary{}, fmt.Errorf("read deleted user %q rows affected: %w", username, err)
	}
	return UserSummary{Username: existing.Username, Role: existing.Role}, nil
}

// ValidateRole reports whether role is one of the mock's built-in NSX roles.
func ValidateRole(role Role) error {
	for _, builtIn := range builtInRoles {
		if role == builtIn.role {
			return nil
		}
	}
	return fmt.Errorf("%w: %q", errUserRoleUnknown, role)
}

// ValidateUsername applies the local safety rule for user persistence.
func ValidateUsername(username string) error {
	if username == "" {
		return errUsernameRequired
	}
	for _, r := range username {
		if r <= ' ' || r == '/' || r == '\\' || r == ':' {
			return fmt.Errorf("%w: %q", errUsernameUnsafe, username)
		}
	}
	return nil
}

func ensureUserSchema(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []struct {
		name string
		sql  string
	}{
		{name: "schema_migrations", sql: createSchemaMigrationsSQL},
		{name: "roles", sql: createRolesSQL},
		{name: "users", sql: createUsersSQL},
	} {
		if _, err := tx.ExecContext(ctx, statement.sql); err != nil {
			return fmt.Errorf("create %s table: %w", statement.name, err)
		}
	}

	result, err := execPrepared(ctx, tx, insertSchemaMigrationSQL, userBootstrapMigrationVersion)
	if err != nil {
		return fmt.Errorf("record user schema migration: %w", err)
	}
	if _, err = result.RowsAffected(); err != nil {
		return fmt.Errorf("read user schema migration rows affected: %w", err)
	}

	return nil
}

func seedBuiltInRoles(ctx context.Context, tx *sql.Tx) (int, error) {
	ensured := 0
	for _, role := range builtInRoles {
		result, err := execPrepared(ctx, tx, upsertRoleSQL, string(role.role), role.displayName, role.sortOrder)
		if err != nil {
			return 0, fmt.Errorf("upsert role %q: %w", role.role, err)
		}
		if _, err = result.RowsAffected(); err != nil {
			return 0, fmt.Errorf("read role %q rows affected: %w", role.role, err)
		}
		ensured++
	}
	return ensured, nil
}

func seedDefaultAdmin(ctx context.Context, tx *sql.Tx) (bool, error) {
	result, err := execPrepared(
		ctx,
		tx,
		insertDefaultAdminSQL,
		DefaultAdminUsername,
		DefaultAdminPassword,
		string(RoleAdmin),
	)
	if err != nil {
		return false, fmt.Errorf("insert default admin user: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read default admin rows affected: %w", err)
	}

	return rowsAffected == 1, nil
}

func execPrepared(ctx context.Context, tx *sql.Tx, query string, args ...any) (result sql.Result, retErr error) {
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("prepare statement: %w", err)
	}
	defer func() {
		closeErr := stmt.Close()
		if closeErr == nil {
			return
		}
		if retErr == nil {
			retErr = fmt.Errorf("close prepared statement: %w", closeErr)
			return
		}
		retErr = fmt.Errorf("%w; close prepared statement: %w", retErr, closeErr)
	}()

	result, err = stmt.ExecContext(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("execute prepared statement: %w", err)
	}

	return result, nil
}
