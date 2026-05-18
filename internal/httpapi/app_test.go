package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"nsx-t-mockapi/internal/config"
	appsqlite "nsx-t-mockapi/internal/sqlite"

	"go.uber.org/zap"
)

func TestNewHandlerRejectsMissingDatabase(t *testing.T) {
	t.Parallel()

	handler, err := NewHandler(context.Background(), AppOptions{Config: httpAPITestConfig(t)})
	if err == nil {
		t.Fatal("NewHandler() error = nil, want error")
	}
	if handler != nil {
		t.Fatalf("NewHandler() handler = %v, want nil", handler)
	}
}

func TestNewServerRejectsMissingHandler(t *testing.T) {
	t.Parallel()

	server, err := NewServer(ServerOptions{Config: httpAPITestConfig(t)})
	if err == nil {
		t.Fatal("NewServer() error = nil, want error")
	}
	if server != nil {
		t.Fatalf("NewServer() server = %v, want nil", server)
	}
}

func TestNewServerRejectsMissingListenAddress(t *testing.T) {
	t.Parallel()

	cfg := httpAPITestConfig(t)
	cfg.Server.ListenAddr = ""
	server, err := NewServer(ServerOptions{
		Config:  cfg,
		Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	})
	if err == nil {
		t.Fatal("NewServer() error = nil, want error")
	}
	if server != nil {
		t.Fatalf("NewServer() server = %v, want nil", server)
	}
}

func TestNewServerBuildsConfiguredServer(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	server, err := NewServer(ServerOptions{Config: httpAPITestConfig(t), Handler: handler})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	if server.Addr != "127.0.0.1:0" {
		t.Fatalf("Addr = %q, want 127.0.0.1:0", server.Addr)
	}
	if server.ReadHeaderTimeout == 0 {
		t.Fatal("ReadHeaderTimeout = 0, want configured timeout")
	}
}

func TestNewHandlerReturnsEmptySegmentList(t *testing.T) {
	t.Parallel()

	db := openHTTPAPITestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close() error = %v", err)
		}
	}()

	handler, err := NewHandler(context.Background(), AppOptions{
		Config: httpAPITestConfig(t),
		DB:     db,
		Logger: zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	req := newHTTPAPITestRequest(t, http.MethodGet, server.URL+"/policy/api/v1/infra/segments", nil)
	resp := doHTTPAPITestRequest(t, server, req)
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("resp.Body.Close() error = %v", closeErr)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", resp.StatusCode)
	}

	var decoded listResult
	if err = json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if decoded.ResultCount != 0 {
		t.Fatalf("ResultCount = %d, want 0", decoded.ResultCount)
	}
	if len(decoded.Results) != 0 {
		t.Fatalf("Results count = %d, want 0", len(decoded.Results))
	}
}

func TestNewHandlerAcceptsPersistedCLIUserThroughBasicAuth(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openHTTPAPITestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close() error = %v", err)
		}
	}()

	users := appsqlite.NewUserStore(db)
	if _, err := users.EnsureBootstrap(ctx); err != nil {
		t.Fatalf("EnsureBootstrap() error = %v", err)
	}
	if _, err := users.AddUser(ctx, appsqlite.User{
		Username: "cli-user",
		Password: "secret",
		Role:     appsqlite.RoleReadWrite,
	}); err != nil {
		t.Fatalf("AddUser() error = %v", err)
	}

	handler, err := NewHandler(ctx, AppOptions{
		Config: httpAPITestConfig(t),
		DB:     db,
		Logger: zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	req := newHTTPAPITestRequest(t, http.MethodGet, server.URL+"/policy/api/v1/infra/segments", nil)
	req.SetBasicAuth("cli-user", "secret")
	resp := doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", resp.StatusCode)
	}

	body := strings.NewReader(`{
		"display_name":"CLI Segment",
		"transport_zone_path":"/infra/sites/default/enforcement-points/default/transport-zones/tz1"
	}`)
	req = newHTTPAPITestRequest(t, http.MethodPut, server.URL+"/policy/api/v1/infra/segments/cli-segment", body)
	req.SetBasicAuth("cli-user", "secret")
	req.Header.Set("Content-Type", contentTypeJSON)
	resp = doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT segment StatusCode = %d, want 200", resp.StatusCode)
	}
	created := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestString(t, created, "_create_user", "cli-user")
	requireHTTPAPITestString(t, created, "_last_modified_user", "cli-user")
}

func TestNewHandlerReturnsMethodAndActionFailures(t *testing.T) {
	t.Parallel()

	db := openHTTPAPITestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close() error = %v", err)
		}
	}()

	handler, err := NewHandler(context.Background(), AppOptions{
		Config: httpAPITestConfig(t),
		DB:     db,
		Logger: zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	req := newHTTPAPITestRequest(t, http.MethodPost, server.URL+"/policy/api/v1/infra/segments", nil)
	resp := doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST StatusCode = %d, want 405", resp.StatusCode)
	}

	req = newHTTPAPITestRequest(t, http.MethodGet, server.URL+"/policy/api/v1/infra/segments?action=revise", nil)
	resp = doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("action StatusCode = %d, want 404", resp.StatusCode)
	}
}

func httpAPITestConfig(t *testing.T) config.Config {
	t.Helper()

	return config.Config{
		Server: config.ServerConfig{ListenAddr: "127.0.0.1:0"},
		Database: config.DatabaseConfig{
			Path: filepath.Join(t.TempDir(), "data", "nsx-t-mockapi.db"),
		},
		Realization: config.RealizationConfig{
			DefaultDelayMS: 0,
			CreateDelayMS:  0,
			UpdateDelayMS:  0,
			DeleteDelayMS:  0,
			KindDelayMS:    map[string]int{},
		},
		Search: config.SearchConfig{
			DefaultPageSize: 1000,
			MaxPageSize:     1000,
		},
	}
}

func openHTTPAPITestDB(t *testing.T) *sql.DB {
	t.Helper()

	dbPath := ":memory:"
	if err := appsqlite.EnsureParentDir(dbPath); err != nil {
		t.Fatalf("EnsureParentDir() error = %v", err)
	}
	db, err := appsqlite.Open(context.Background(), appsqlite.OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err = appsqlite.NewUserStore(db).EnsureBootstrap(context.Background()); err != nil {
		t.Fatalf("EnsureBootstrap() error = %v", err)
	}
	return db
}

func newHTTPAPITestRequest(t *testing.T, method string, url string, body io.Reader) *http.Request {
	t.Helper()

	req := newHTTPAPITestRequestWithoutAuth(t, method, url, body)
	req.SetBasicAuth(appsqlite.DefaultAdminUsername, appsqlite.DefaultAdminPassword)
	return req
}

func newHTTPAPITestRequestWithoutAuth(t *testing.T, method string, url string, body io.Reader) *http.Request {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, url, body)
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}
	return req
}

func doHTTPAPITestRequest(t *testing.T, server *httptest.Server, req *http.Request) *http.Response {
	t.Helper()

	//nolint:gosec // Tests only call local httptest.Server URLs created in this file.
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	return resp
}

func closeHTTPAPITestBody(t *testing.T, resp *http.Response) {
	t.Helper()

	if err := resp.Body.Close(); err != nil {
		t.Errorf("resp.Body.Close() error = %v", err)
	}
}
