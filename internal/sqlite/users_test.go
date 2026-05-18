package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestUserStoreBootstrapsDefaultAdminInFreshInMemorySQLite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close() error = %v", err)
		}
	}()

	store := NewUserStore(db)

	report, err := store.EnsureBootstrap(ctx)
	if err != nil {
		t.Fatalf("EnsureBootstrap() error = %v", err)
	}

	user, found, err := store.FindUser(ctx, DefaultAdminUsername)
	if err != nil {
		t.Fatalf("FindUser() error = %v", err)
	}
	if !found {
		t.Fatalf("FindUser(%q) found = false, want true", DefaultAdminUsername)
	}
	if user.Username != DefaultAdminUsername {
		t.Fatalf("FindUser() username = %q, want %q", user.Username, DefaultAdminUsername)
	}
	if user.Password != DefaultAdminPassword {
		t.Fatalf("FindUser() password = %q, want %q", user.Password, DefaultAdminPassword)
	}
	if user.Role != RoleAdmin {
		t.Fatalf("FindUser() role = %q, want %q", user.Role, RoleAdmin)
	}
	if !report.DefaultAdminCreated {
		t.Fatal("EnsureBootstrap() DefaultAdminCreated = false, want true")
	}
}

func TestUserStoreBootstrapDoesNotOverwriteExistingAdmin(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close() error = %v", err)
		}
	}()

	store := NewUserStore(db)
	if _, err := store.EnsureBootstrap(ctx); err != nil {
		t.Fatalf("EnsureBootstrap() setup error = %v", err)
	}

	execTestPrepared(
		t,
		db,
		"UPDATE users SET password = ?, role_id = ? WHERE username = ?",
		"changed_password",
		string(RoleReadOnly),
		DefaultAdminUsername,
	)

	report, err := store.EnsureBootstrap(ctx)
	if err != nil {
		t.Fatalf("EnsureBootstrap() error = %v", err)
	}

	user, found, err := store.FindUser(ctx, DefaultAdminUsername)
	if err != nil {
		t.Fatalf("FindUser() error = %v", err)
	}
	if !found {
		t.Fatalf("FindUser(%q) found = false, want true", DefaultAdminUsername)
	}
	if user.Password != "changed_password" {
		t.Fatalf("FindUser() password = %q, want changed_password", user.Password)
	}
	if user.Role != RoleReadOnly {
		t.Fatalf("FindUser() role = %q, want %q", user.Role, RoleReadOnly)
	}
	if report.DefaultAdminCreated {
		t.Fatal("EnsureBootstrap() DefaultAdminCreated = true, want false")
	}
}

func TestUserStorePersistsAllBuiltInRoles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close() error = %v", err)
		}
	}()

	store := NewUserStore(db)

	report, err := store.EnsureBootstrap(ctx)
	if err != nil {
		t.Fatalf("EnsureBootstrap() error = %v", err)
	}

	got := collectPersistedRoles(t, db)

	want := []builtInRole{
		{role: RoleReadOnly, displayName: "Read Only", sortOrder: roleReadOnlySortOrder},
		{role: RoleReadWrite, displayName: "Read Write", sortOrder: roleReadWriteSortOrder},
		{role: RoleAdmin, displayName: "Admin", sortOrder: roleAdminSortOrder},
	}
	requireBuiltInRoles(t, got, want)
	if report.RolesEnsured != len(want) {
		t.Fatalf("EnsureBootstrap() RolesEnsured = %d, want %d", report.RolesEnsured, len(want))
	}
}

func TestUserStoreFindUserReturnsNotFoundForMissingUser(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close() error = %v", err)
		}
	}()

	store := NewUserStore(db)
	if _, err := store.EnsureBootstrap(ctx); err != nil {
		t.Fatalf("EnsureBootstrap() error = %v", err)
	}

	user, found, err := store.FindUser(ctx, "missing-user")
	if err != nil {
		t.Fatalf("FindUser() error = %v", err)
	}
	if found {
		t.Fatal("FindUser() found = true, want false")
	}
	if user != (User{}) {
		t.Fatalf("FindUser() user = %#v, want zero User", user)
	}
}

func TestUserStoreAddListAndDeleteUser(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close() error = %v", err)
		}
	}()

	store := NewUserStore(db)
	if _, err := store.EnsureBootstrap(ctx); err != nil {
		t.Fatalf("EnsureBootstrap() error = %v", err)
	}
	added, err := store.AddUser(ctx, User{
		Username: "alice",
		Password: "secret",
		Role:     RoleReadWrite,
	})
	if err != nil {
		t.Fatalf("AddUser() error = %v", err)
	}
	if added != (UserSummary{Username: "alice", Role: RoleReadWrite}) {
		t.Fatalf("AddUser() = %+v, want alice read-write summary", added)
	}

	users, err := store.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
	requireUserSummary(t, users, "alice", RoleReadWrite)

	deleted, err := store.DeleteUser(ctx, "alice")
	if err != nil {
		t.Fatalf("DeleteUser() error = %v", err)
	}
	if deleted != (UserSummary{Username: "alice", Role: RoleReadWrite}) {
		t.Fatalf("DeleteUser() = %+v, want alice read-write summary", deleted)
	}
	_, found, err := store.FindUser(ctx, "alice")
	if err != nil {
		t.Fatalf("FindUser() after delete error = %v", err)
	}
	if found {
		t.Fatal("FindUser() after delete found = true, want false")
	}
}

func TestUserStoreRejectsInvalidUserMutations(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close() error = %v", err)
		}
	}()

	store := NewUserStore(db)
	if _, err := store.EnsureBootstrap(ctx); err != nil {
		t.Fatalf("EnsureBootstrap() error = %v", err)
	}
	for _, tc := range []struct {
		name string
		user User
	}{
		{name: "empty username", user: User{Password: "secret", Role: RoleReadWrite}},
		{name: "unsafe username", user: User{Username: "bad/name", Password: "secret", Role: RoleReadWrite}},
		{name: "empty password", user: User{Username: "alice", Role: RoleReadWrite}},
		{name: "unknown role", user: User{Username: "alice", Password: "secret", Role: Role("superuser")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.AddUser(ctx, tc.user)
			if err == nil {
				t.Fatalf("AddUser(%+v) error = nil, want error", tc.user)
			}
		})
	}
	if _, err := store.DeleteUser(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("DeleteUser(missing) error = %v, want sql.ErrNoRows", err)
	}
}

func TestUserStoreFindUserReportsClosedDatabaseError(t *testing.T) {
	t.Parallel()

	db := openThenCloseDB(t)
	_, _, err := NewUserStore(db).FindUser(context.Background(), DefaultAdminUsername)
	if err == nil {
		t.Fatal("FindUser() error = nil, want error")
	}
}

func TestUserStoreEnsureBootstrapReportsCanceledContextError(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close() error = %v", err)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewUserStore(db).EnsureBootstrap(ctx)
	if err == nil {
		t.Fatal("EnsureBootstrap() error = nil, want error")
	}
}

func TestUserStoreEnsureBootstrapReportsInvalidExistingRolesSchema(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close() error = %v", err)
		}
	}()

	if _, err := db.ExecContext(ctx, "CREATE TABLE roles(role_id TEXT PRIMARY KEY)"); err != nil {
		t.Fatalf("CREATE TABLE roles error = %v", err)
	}

	_, err := NewUserStore(db).EnsureBootstrap(ctx)
	if err == nil {
		t.Fatal("EnsureBootstrap() error = nil, want error")
	}
}

func TestUserStoreEnsureBootstrapReportsInvalidExistingUsersSchema(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close() error = %v", err)
		}
	}()

	if _, err := db.ExecContext(ctx, "CREATE TABLE users(username TEXT PRIMARY KEY)"); err != nil {
		t.Fatalf("CREATE TABLE users error = %v", err)
	}

	_, err := NewUserStore(db).EnsureBootstrap(ctx)
	if err == nil {
		t.Fatal("EnsureBootstrap() error = nil, want error")
	}
}

func execTestPrepared(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()

	stmt, err := db.PrepareContext(context.Background(), query)
	if err != nil {
		t.Fatalf("PrepareContext() error = %v", err)
	}
	defer func() {
		if closeErr := stmt.Close(); closeErr != nil {
			t.Fatalf("stmt.Close() error = %v", closeErr)
		}
	}()

	result, err := stmt.ExecContext(context.Background(), args...)
	if err != nil {
		t.Fatalf("ExecContext() error = %v", err)
	}
	_, err = result.RowsAffected()
	if err != nil {
		t.Fatalf("RowsAffected() error = %v", err)
	}
}

func collectPersistedRoles(t *testing.T, db *sql.DB) []builtInRole {
	t.Helper()

	rows, err := db.QueryContext(
		context.Background(),
		"SELECT role_id, display_name, sort_order FROM roles ORDER BY sort_order",
	)
	if err != nil {
		t.Fatalf("QueryContext() error = %v", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("rows.Close() error = %v", closeErr)
		}
	}()

	got := make([]builtInRole, 0, len(builtInRoles))
	for rows.Next() {
		var role builtInRole
		var roleID string
		scanErr := rows.Scan(&roleID, &role.displayName, &role.sortOrder)
		if scanErr != nil {
			t.Fatalf("rows.Scan() error = %v", scanErr)
		}
		role.role = Role(roleID)
		got = append(got, role)
	}
	rowsErr := rows.Err()
	if rowsErr != nil {
		t.Fatalf("rows.Err() error = %v", rowsErr)
	}

	return got
}

func requireBuiltInRoles(t *testing.T, got []builtInRole, want []builtInRole) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("role count = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("role[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func requireUserSummary(t *testing.T, users []UserSummary, username string, role Role) {
	t.Helper()

	for _, user := range users {
		if user.Username == username {
			if user.Role != role {
				t.Fatalf("user %q role = %q, want %q", username, user.Role, role)
			}
			return
		}
	}
	t.Fatalf("user %q not found in %+v", username, users)
}
