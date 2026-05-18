package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	appsqlite "nsx-t-mockapi/internal/sqlite"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestRequireBasicAuthRejectsMissingCredentials(t *testing.T) {
	t.Parallel()

	var reached atomic.Bool
	server := newAuthTestServer(t, zap.NewNop(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached.Store(true)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	req := newAuthTestRequest(t, http.MethodGet, server.URL)
	resp := doAuthTestRequest(t, server, req)
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("resp.Body.Close() error = %v", closeErr)
		}
	}()

	requireAuthTestStatus(t, resp, http.StatusUnauthorized)
	requireAuthTestChallenge(t, resp)
	if reached.Load() {
		t.Fatal("authenticated probe handler was reached")
	}
}

func TestRequireBasicAuthRejectsBadCredentials(t *testing.T) {
	t.Parallel()

	var reached atomic.Bool
	server := newAuthTestServer(t, zap.NewNop(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached.Store(true)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	req := newAuthTestRequest(t, http.MethodGet, server.URL)
	req.SetBasicAuth(appsqlite.DefaultAdminUsername, "wrong-password")

	resp := doAuthTestRequest(t, server, req)
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("resp.Body.Close() error = %v", closeErr)
		}
	}()

	requireAuthTestStatus(t, resp, http.StatusUnauthorized)
	requireAuthTestChallenge(t, resp)
	if reached.Load() {
		t.Fatal("authenticated probe handler was reached")
	}
}

func TestRequireBasicAuthAcceptsDefaultAdminAndStampsMetadataProbe(t *testing.T) {
	t.Parallel()

	server := newAuthTestServer(t, zap.NewNop(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := UserFromContext(r.Context())
		if !ok {
			http.Error(w, "missing authenticated user", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(map[string]string{
			"_create_user":        user.Username,
			"_last_modified_user": user.Username,
		})
		if err != nil {
			t.Errorf("Encode() error = %v", err)
		}
	}))
	defer server.Close()

	req := newAuthTestRequest(t, http.MethodPost, server.URL)
	req.SetBasicAuth(appsqlite.DefaultAdminUsername, appsqlite.DefaultAdminPassword)

	resp := doAuthTestRequest(t, server, req)
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("resp.Body.Close() error = %v", closeErr)
		}
	}()

	requireAuthTestStatus(t, resp, http.StatusOK)
	requireNoAuthTestChallenge(t, resp)

	body := decodeAuthTestStringMap(t, resp.Body)
	if body["_create_user"] != appsqlite.DefaultAdminUsername {
		t.Fatalf("_create_user = %q, want %q", body["_create_user"], appsqlite.DefaultAdminUsername)
	}
	if body["_last_modified_user"] != appsqlite.DefaultAdminUsername {
		t.Fatalf("_last_modified_user = %q, want %q", body["_last_modified_user"], appsqlite.DefaultAdminUsername)
	}
}

func TestRequireBasicAuthIncludesRoleInContext(t *testing.T) {
	t.Parallel()

	server := newAuthTestServer(t, zap.NewNop(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := UserFromContext(r.Context())
		if !ok {
			http.Error(w, "missing authenticated user", http.StatusInternalServerError)
			return
		}
		if user.Role != appsqlite.RoleAdmin {
			http.Error(w, "unexpected role", http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	req := newAuthTestRequest(t, http.MethodGet, server.URL)
	req.SetBasicAuth(appsqlite.DefaultAdminUsername, appsqlite.DefaultAdminPassword)

	resp := doAuthTestRequest(t, server, req)
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("resp.Body.Close() error = %v", closeErr)
		}
	}()

	requireAuthTestStatus(t, resp, http.StatusNoContent)
}

func TestRequireBasicAuthAcceptsPersistedCLIUser(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openAuthTestDB(t)
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("db.Close() error = %v", closeErr)
		}
	})
	store := appsqlite.NewUserStore(db)
	if _, err := store.EnsureBootstrap(ctx); err != nil {
		t.Fatalf("EnsureBootstrap() error = %v", err)
	}
	_, err := store.AddUser(ctx, appsqlite.User{
		Username: "cli-user",
		Password: "secret",
		Role:     appsqlite.RoleReadWrite,
	})
	if err != nil {
		t.Fatalf("AddUser() error = %v", err)
	}

	handler := RequireBasicAuth(zap.NewNop(), store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := UserFromContext(r.Context())
		if !ok {
			http.Error(w, "missing authenticated user", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		encodeErr := json.NewEncoder(w).Encode(map[string]string{
			"_create_user":        user.Username,
			"_last_modified_user": user.Username,
			"role":                string(user.Role),
		})
		if encodeErr != nil {
			t.Errorf("Encode() error = %v", encodeErr)
		}
	}))
	server := httptest.NewServer(handler)
	defer server.Close()

	req := newAuthTestRequest(t, http.MethodPost, server.URL)
	req.SetBasicAuth("cli-user", "secret")

	resp := doAuthTestRequest(t, server, req)
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("resp.Body.Close() error = %v", closeErr)
		}
	}()

	requireAuthTestStatus(t, resp, http.StatusOK)
	body := decodeAuthTestStringMap(t, resp.Body)
	if body["_create_user"] != "cli-user" {
		t.Fatalf("_create_user = %q, want cli-user", body["_create_user"])
	}
	if body["_last_modified_user"] != "cli-user" {
		t.Fatalf("_last_modified_user = %q, want cli-user", body["_last_modified_user"])
	}
	if body["role"] != string(appsqlite.RoleReadWrite) {
		t.Fatalf("role = %q, want %q", body["role"], appsqlite.RoleReadWrite)
	}
}

func TestRequireBasicAuthReportsLookupErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openAuthTestDB(t)
	store := appsqlite.NewUserStore(db)
	if _, err := store.EnsureBootstrap(ctx); err != nil {
		t.Fatalf("EnsureBootstrap() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	core, logs := observer.New(zap.DebugLevel)
	logger := zap.New(core)
	var reached atomic.Bool
	server := httptest.NewServer(RequireBasicAuth(
		logger,
		store,
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			reached.Store(true)
			w.WriteHeader(http.StatusNoContent)
		}),
	))
	defer server.Close()

	req := newAuthTestRequest(t, http.MethodGet, server.URL)
	req.SetBasicAuth(appsqlite.DefaultAdminUsername, appsqlite.DefaultAdminPassword)

	resp := doAuthTestRequest(t, server, req)
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("resp.Body.Close() error = %v", closeErr)
		}
	}()

	requireAuthTestStatus(t, resp, http.StatusInternalServerError)
	if reached.Load() {
		t.Fatal("authenticated probe handler was reached")
	}

	entries := logs.FilterMessage("basic auth user lookup failed").All()
	if len(entries) != 1 {
		t.Fatalf("lookup error log count = %d, want 1", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["username"] != appsqlite.DefaultAdminUsername {
		t.Fatalf("logged username = %v, want %q", fields["username"], appsqlite.DefaultAdminUsername)
	}
	if _, ok := fields["error"]; !ok {
		t.Fatal("lookup error log missing structured error field")
	}
}

func newAuthTestServer(t *testing.T, logger *zap.Logger, next http.Handler) *httptest.Server {
	t.Helper()

	ctx := context.Background()
	db := openAuthTestDB(t)
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("db.Close() error = %v", closeErr)
		}
	})

	store := appsqlite.NewUserStore(db)
	if _, err := store.EnsureBootstrap(ctx); err != nil {
		t.Fatalf("EnsureBootstrap() error = %v", err)
	}

	return httptest.NewServer(RequireBasicAuth(logger, store, next))
}

func newAuthTestRequest(t *testing.T, method string, url string) *http.Request {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, url, nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}

	return req
}

func doAuthTestRequest(t *testing.T, server *httptest.Server, req *http.Request) *http.Response {
	t.Helper()

	//nolint:gosec // Tests only call local httptest.Server URLs created in this file.
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}

	return resp
}

func requireAuthTestStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()

	if resp.StatusCode != want {
		t.Fatalf("StatusCode = %d, want %d", resp.StatusCode, want)
	}
}

func requireAuthTestChallenge(t *testing.T, resp *http.Response) {
	t.Helper()

	if got := resp.Header.Get("WWW-Authenticate"); got != `Basic realm="nsx-t-mockapi"` {
		t.Fatalf("WWW-Authenticate = %q, want Basic challenge", got)
	}
}

func requireNoAuthTestChallenge(t *testing.T, resp *http.Response) {
	t.Helper()

	if got := resp.Header.Get("WWW-Authenticate"); got != "" {
		t.Fatalf("WWW-Authenticate = %q, want empty on success", got)
	}
}

func decodeAuthTestStringMap(t *testing.T, body io.Reader) map[string]string {
	t.Helper()

	var decoded map[string]string
	if err := json.NewDecoder(body).Decode(&decoded); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	return decoded
}

func openAuthTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "data", "nsx-t-mockapi.db")
	if err := appsqlite.EnsureParentDir(dbPath); err != nil {
		t.Fatalf("EnsureParentDir() error = %v", err)
	}

	db, err := appsqlite.Open(context.Background(), appsqlite.OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("sqlite.Open() error = %v", err)
	}

	return db
}
