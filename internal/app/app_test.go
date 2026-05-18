package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nsx-t-mockapi/internal/clock"
	"nsx-t-mockapi/internal/config"
	appsqlite "nsx-t-mockapi/internal/sqlite"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

const (
	appTestManagerOne = "nsx-t-1"
	appTestManagerTwo = "nsx-t-2"
)

func TestBuildUsesConfigDatabasePathAndBootstrapsUsers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "data", "nsx-t-mockapi.db")
	configPath := writeAppTestConfig(t, dbPath)

	built, err := Build(ctx, Options{ConfigPath: configPath, Logger: zap.NewNop()})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	defer closeAppTestApplication(t, built)

	if built.Config.Database.Path != dbPath {
		t.Fatalf("Config.Database.Path = %q, want %q", built.Config.Database.Path, dbPath)
	}
	managerDB, err := built.ManagerDatabases.ResolveManagerDatabase(ctx, appTestManagerOne)
	if err != nil {
		t.Fatalf("ResolveManagerDatabase() error = %v", err)
	}
	user, found, err := appsqlite.NewUserStore(managerDB.DB).FindUser(ctx, appsqlite.DefaultAdminUsername)
	if err != nil {
		t.Fatalf("FindUser() error = %v", err)
	}
	if !found {
		t.Fatalf("FindUser(%q) found = false, want true", appsqlite.DefaultAdminUsername)
	}
	if user.Password != appsqlite.DefaultAdminPassword {
		t.Fatalf("FindUser() password = %q, want %q", user.Password, appsqlite.DefaultAdminPassword)
	}
}

func TestBuildBootstrapsResourceStoreSchema(t *testing.T) {
	t.Parallel()

	built := buildAppTestApplication(t)
	defer closeAppTestApplication(t, built)
	managerDB, err := built.ManagerDatabases.ResolveManagerDatabase(context.Background(), appTestManagerOne)
	if err != nil {
		t.Fatalf("ResolveManagerDatabase() error = %v", err)
	}

	for _, table := range []string{"resources", "resource_realization", "resource_edges", "operation_log"} {
		if !appTableExists(t, managerDB.DB, table) {
			t.Fatalf("table %q does not exist after Build()", table)
		}
	}
}

func TestBuildUsesFakeClockForElapsedLogging(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	core, logs := observer.New(zap.DebugLevel)
	logger := zap.New(core)
	start := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFakeClock(start)
	dbPath := filepath.Join(t.TempDir(), "data", "nsx-t-mockapi.db")
	configPath := writeAppTestConfig(t, dbPath)

	built, err := Build(ctx, Options{ConfigPath: configPath, Logger: logger, Clock: fakeClock})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	defer closeAppTestApplication(t, built)

	entries := logs.FilterMessage("starting application build").All()
	if len(entries) != 1 {
		t.Fatalf("starting application build log count = %d, want 1", len(entries))
	}
	if got := entries[0].ContextMap()["started_at"]; got != start {
		t.Fatalf("started_at = %v, want %v", got, start)
	}
	fakeClock.Advance(time.Second)
	if fakeClock.Now() != start.Add(time.Second) {
		t.Fatal("fake clock did not advance deterministically")
	}
}

func TestBuildRejectsInvalidConfigAtStartup(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "invalid.yaml")
	if err := os.WriteFile(configPath, []byte("server:\n  listen_addr: \"127.0.0.1:0\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	built, err := Build(context.Background(), Options{ConfigPath: configPath, Logger: zap.NewNop()})
	if err == nil {
		closeAppTestApplication(t, built)
		t.Fatal("Build() error = nil, want error")
	}
	if built != nil {
		t.Fatalf("Build() app = %v, want nil", built)
	}
}

func TestBuildRejectsMissingConfigPath(t *testing.T) {
	t.Parallel()

	built, err := Build(context.Background(), Options{Logger: zap.NewNop()})
	if err == nil {
		closeAppTestApplication(t, built)
		t.Fatal("Build() error = nil, want error")
	}
	if built != nil {
		t.Fatalf("Build() app = %v, want nil", built)
	}
}

func TestRunRejectsInvalidConfigWithoutServing(t *testing.T) {
	t.Parallel()

	err := Run(context.Background(), Options{ConfigPath: filepath.Join(t.TempDir(), "missing.yaml"), Logger: zap.NewNop()})
	if err == nil {
		t.Fatal("Run() error = nil, want error")
	}
}

func TestRunReturnsServeErrorWhenListenerCannotBind(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "data", "nsx-t-mockapi.db")
	configPath := writeAppTestConfigWithListenAddr(t, dbPath, "203.0.113.1:1")

	err := Run(context.Background(), Options{ConfigPath: configPath, Logger: zap.NewNop()})
	if err == nil {
		t.Fatal("Run() error = nil, want listen error")
	}
}

func TestApplicationCloseAllowsNilReceiver(t *testing.T) {
	t.Parallel()

	var built *Application
	if err := built.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if err := (&Application{}).Close(); err != nil {
		t.Fatalf("empty Application Close() error = %v", err)
	}
}

func TestBuildHTTPServerReportsHandlerErrors(t *testing.T) {
	t.Parallel()

	server, err := buildHTTPServer(
		context.Background(),
		zap.NewNop(),
		appTestConfig(filepath.Join(t.TempDir(), "db.sqlite")),
		nil,
		nil,
	)
	if err == nil {
		t.Fatal("buildHTTPServer() error = nil, want error")
	}
	if server != nil {
		t.Fatalf("buildHTTPServer() server = %v, want nil", server)
	}
}

func TestBuildHTTPServerReportsServerConfigErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "data", "server-config.db")
	if err := appsqlite.EnsureParentDir(dbPath); err != nil {
		t.Fatalf("EnsureParentDir() error = %v", err)
	}
	db, err := appsqlite.Open(ctx, appsqlite.OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("db.Close() error = %v", closeErr)
		}
	}()
	if _, err = appsqlite.NewUserStore(db).EnsureBootstrap(ctx); err != nil {
		t.Fatalf("EnsureBootstrap() error = %v", err)
	}

	cfg := appTestConfig(dbPath)
	cfg.Server.ListenAddr = ""
	server, err := buildHTTPServer(ctx, zap.NewNop(), cfg, appsqlite.NewStaticManagerDatabaseProvider(db), nil)
	if err == nil {
		t.Fatal("buildHTTPServer() error = nil, want error")
	}
	if server != nil {
		t.Fatalf("buildHTTPServer() server = %v, want nil", server)
	}
}

func TestBuiltHandlerServesRealHTTPWithBasicAuth(t *testing.T) {
	t.Parallel()

	built := buildAppTestApplication(t)
	defer closeAppTestApplication(t, built)

	server := httptest.NewServer(built.Server.Handler)
	defer server.Close()

	req := newAppTestRequest(t, http.MethodGet, server.URL+"/policy/api/v1/infra/segments")
	resp := doAppTestRequest(t, server, req)
	defer closeAppTestBody(t, resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing auth StatusCode = %d, want 401", resp.StatusCode)
	}

	req = newAppTestRequest(t, http.MethodGet, server.URL+"/policy/api/v1/infra/segments")
	req.SetBasicAuth(appsqlite.DefaultAdminUsername, appsqlite.DefaultAdminPassword)
	resp = doAppTestRequest(t, server, req)
	defer closeAppTestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			t.Fatalf("ReadAll() error = %v", readErr)
		}
		t.Fatalf("authenticated StatusCode = %d, want 200, body %q", resp.StatusCode, string(body))
	}
}

func TestBuiltHandlerRoutesHostnamesToIndependentManagerDatabases(t *testing.T) {
	t.Parallel()

	dataDir := filepath.Join(t.TempDir(), "data")
	dbPath := filepath.Join(dataDir, "nsx-t-mockapi.db")
	configPath := writeAppTestConfig(t, dbPath)
	built, err := Build(context.Background(), Options{ConfigPath: configPath, Logger: zap.NewNop()})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	defer closeAppTestApplication(t, built)

	server := httptest.NewServer(built.Server.Handler)
	defer server.Close()

	appTestCreatePolicySegment(t, server, appTestManagerOne, "host-one-only", "HostOneOnly")

	nsxOneSegments := appTestListSegments(t, server, appTestManagerOne)
	if len(nsxOneSegments) != 1 {
		t.Fatalf("nsx-t-1 segments count = %d, want 1", len(nsxOneSegments))
	}
	nsxTwoSegments := appTestListSegments(t, server, appTestManagerTwo)
	if len(nsxTwoSegments) != 0 {
		t.Fatalf("nsx-t-2 segments count = %d, want 0: %#v", len(nsxTwoSegments), nsxTwoSegments)
	}

	for _, dbFile := range []string{
		filepath.Join(dataDir, "managers", appTestManagerOne, "nsx-t-mockapi.db"),
		filepath.Join(dataDir, "managers", appTestManagerTwo, "nsx-t-mockapi.db"),
	} {
		info, statErr := os.Stat(dbFile)
		if statErr != nil {
			t.Fatalf("manager database %q Stat() error = %v", dbFile, statErr)
		}
		if info.IsDir() {
			t.Fatalf("manager database %q is a directory, want file", dbFile)
		}
	}
}

func TestBuiltHandlerKeepsSearchIndexesIndependentByHostname(t *testing.T) {
	t.Parallel()

	built := buildAppTestApplication(t)
	defer closeAppTestApplication(t, built)

	server := httptest.NewServer(built.Server.Handler)
	defer server.Close()

	appTestCreateManagerIPSet(t, server, appTestManagerOne, map[string]any{
		"id":            "host-one-apps",
		"display_name":  "HostOneSearchOnly",
		"description":   "unique searchable manager resource",
		"resource_type": "IPSet",
		"ip_addresses":  []string{"192.0.2.100"},
	})

	searchQuery := "display_name:HostOneSearchOnly AND resource_type:IPSet"
	nsxOneNames := appTestSearchDisplayNames(t, server, appTestManagerOne, searchQuery)
	if len(nsxOneNames) != 1 || nsxOneNames[0] != "HostOneSearchOnly" {
		t.Fatalf("nsx-t-1 search display names = %#v, want HostOneSearchOnly", nsxOneNames)
	}
	nsxTwoNames := appTestSearchDisplayNames(t, server, appTestManagerTwo, searchQuery)
	if len(nsxTwoNames) != 0 {
		t.Fatalf("nsx-t-2 search display names = %#v, want empty", nsxTwoNames)
	}
}

func TestBuiltHandlerKeepsUsersIndependentByHostname(t *testing.T) {
	t.Parallel()

	built := buildAppTestApplication(t)
	defer closeAppTestApplication(t, built)

	server := httptest.NewServer(built.Server.Handler)
	defer server.Close()

	requireAppTestAuthenticatedSegmentsList(t, server, appTestManagerOne, appsqlite.DefaultAdminPassword, http.StatusOK)
	requireAppTestAuthenticatedSegmentsList(t, server, appTestManagerTwo, appsqlite.DefaultAdminPassword, http.StatusOK)

	ctx := context.Background()
	nsxOneDB, err := built.ManagerDatabases.ResolveManagerDatabase(ctx, appTestManagerOne)
	if err != nil {
		t.Fatalf("ResolveManagerDatabase(nsx-t-1) error = %v", err)
	}
	result, err := nsxOneDB.DB.ExecContext(
		ctx,
		"UPDATE users SET password = ? WHERE username = ?",
		"nsx_one_password",
		appsqlite.DefaultAdminUsername,
	)
	if err != nil {
		t.Fatalf("update nsx-t-1 user password error = %v", err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil {
		t.Fatalf("RowsAffected() error = %v", rowsErr)
	} else if rows != 1 {
		t.Fatalf("RowsAffected() = %d, want 1", rows)
	}

	requireAppTestAuthenticatedSegmentsList(
		t,
		server,
		appTestManagerOne,
		appsqlite.DefaultAdminPassword,
		http.StatusUnauthorized,
	)
	requireAppTestAuthenticatedSegmentsList(t, server, appTestManagerOne, "nsx_one_password", http.StatusOK)
	requireAppTestAuthenticatedSegmentsList(t, server, appTestManagerTwo, appsqlite.DefaultAdminPassword, http.StatusOK)
}

func TestBuiltHandlerKeepsOperationLogAndTombstonesIndependentByHostname(t *testing.T) {
	t.Parallel()

	built := buildAppTestApplication(t)
	defer closeAppTestApplication(t, built)

	server := httptest.NewServer(built.Server.Handler)
	defer server.Close()

	appTestCreatePolicySegment(t, server, appTestManagerOne, "delete-host-one-only", "DeleteHostOneOnly")
	appTestDeletePolicySegment(t, server, appTestManagerOne, "delete-host-one-only")

	nsxTwoSegments := appTestListSegments(t, server, appTestManagerTwo)
	if len(nsxTwoSegments) != 0 {
		t.Fatalf("nsx-t-2 segments count = %d, want 0: %#v", len(nsxTwoSegments), nsxTwoSegments)
	}

	ctx := context.Background()
	nsxOneDB, err := built.ManagerDatabases.ResolveManagerDatabase(ctx, appTestManagerOne)
	if err != nil {
		t.Fatalf("ResolveManagerDatabase(nsx-t-1) error = %v", err)
	}
	nsxTwoDB, err := built.ManagerDatabases.ResolveManagerDatabase(ctx, appTestManagerTwo)
	if err != nil {
		t.Fatalf("ResolveManagerDatabase(nsx-t-2) error = %v", err)
	}
	if got := appTestCountRows(t, nsxOneDB.DB, "operation_log"); got != 2 {
		t.Fatalf("nsx-t-1 operation_log rows = %d, want 2", got)
	}
	if got := appTestCountRows(t, nsxTwoDB.DB, "operation_log"); got != 0 {
		t.Fatalf("nsx-t-2 operation_log rows = %d, want 0", got)
	}
	if got := appTestCountMarkedForDelete(t, nsxOneDB.DB); got != 1 {
		t.Fatalf("nsx-t-1 tombstone rows = %d, want 1", got)
	}
	if got := appTestCountMarkedForDelete(t, nsxTwoDB.DB); got != 0 {
		t.Fatalf("nsx-t-2 tombstone rows = %d, want 0", got)
	}
}

func TestBuiltHandlerDistinguishesMethodAndAction(t *testing.T) {
	t.Parallel()

	built := buildAppTestApplication(t)
	defer closeAppTestApplication(t, built)

	server := httptest.NewServer(built.Server.Handler)
	defer server.Close()

	req := newAppTestRequest(t, http.MethodPost, server.URL+"/policy/api/v1/infra/segments")
	req.SetBasicAuth(appsqlite.DefaultAdminUsername, appsqlite.DefaultAdminPassword)
	resp := doAppTestRequest(t, server, req)
	defer closeAppTestBody(t, resp)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST StatusCode = %d, want 405", resp.StatusCode)
	}
	if got := resp.Header.Get("Allow"); got != http.MethodGet {
		t.Fatalf("Allow = %q, want GET", got)
	}

	req = newAppTestRequest(t, http.MethodGet, server.URL+"/policy/api/v1/infra/segments?action=revise")
	req.SetBasicAuth(appsqlite.DefaultAdminUsername, appsqlite.DefaultAdminPassword)
	resp = doAppTestRequest(t, server, req)
	defer closeAppTestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("action StatusCode = %d, want 404", resp.StatusCode)
	}
}

func buildAppTestApplication(t *testing.T) *Application {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "data", "nsx-t-mockapi.db")
	configPath := writeAppTestConfig(t, dbPath)
	built, err := Build(context.Background(), Options{ConfigPath: configPath, Logger: zap.NewNop()})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return built
}

func writeAppTestConfig(t *testing.T, dbPath string) string {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	config := appTestConfigYAML(dbPath, "127.0.0.1:0")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return configPath
}

func writeAppTestConfigWithListenAddr(t *testing.T, dbPath string, listenAddr string) string {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	config := appTestConfigYAML(dbPath, listenAddr)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return configPath
}

func appTestConfigYAML(dbPath string, listenAddr string) string {
	config := `
server:
  listen_addr: "` + listenAddr + `"
database:
  path: "` + dbPath + `"
realization:
  default_delay_ms: 0
  create_delay_ms: 0
  update_delay_ms: 0
  delete_delay_ms: 0
search:
  default_page_size: 1000
  max_page_size: 1000
`
	return config
}

func appTestConfig(dbPath string) config.Config {
	return config.Config{
		Server: config.ServerConfig{ListenAddr: "127.0.0.1:0"},
		Database: config.DatabaseConfig{
			Path: dbPath,
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

func closeAppTestApplication(t *testing.T, built *Application) {
	t.Helper()

	if built == nil {
		return
	}
	if err := built.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func newAppTestRequest(t *testing.T, method string, url string) *http.Request {
	t.Helper()

	return newAppTestRequestWithBody(t, method, url, nil)
}

func newAppTestRequestWithBody(t *testing.T, method string, url string, body []byte) *http.Request {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}
	return req
}

func doAppTestRequest(t *testing.T, server *httptest.Server, req *http.Request) *http.Response {
	t.Helper()

	//nolint:gosec // Tests only call local httptest.Server URLs created in this file.
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	return resp
}

func closeAppTestBody(t *testing.T, resp *http.Response) {
	t.Helper()

	if err := resp.Body.Close(); err != nil {
		t.Errorf("resp.Body.Close() error = %v", err)
	}
}

func appTestCreatePolicySegment(
	t *testing.T,
	server *httptest.Server,
	host string,
	segmentID string,
	displayName string,
) {
	t.Helper()

	payload := map[string]any{"display_name": displayName, "resource_type": "Segment"}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	req := newAppTestRequestWithBody(
		t,
		http.MethodPatch,
		server.URL+"/policy/api/v1/infra/segments/"+segmentID,
		body,
	)
	req.Host = host
	req.SetBasicAuth(appsqlite.DefaultAdminUsername, appsqlite.DefaultAdminPassword)
	resp := doAppTestRequest(t, server, req)
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("resp.Body.Close() error = %v", closeErr)
		}
	}()
	requireAppTestStatus(t, resp, http.StatusOK, "create segment")
}

func appTestDeletePolicySegment(t *testing.T, server *httptest.Server, host string, segmentID string) {
	t.Helper()

	req := newAppTestRequest(t, http.MethodDelete, server.URL+"/policy/api/v1/infra/segments/"+segmentID)
	req.Host = host
	req.SetBasicAuth(appsqlite.DefaultAdminUsername, appsqlite.DefaultAdminPassword)
	resp := doAppTestRequest(t, server, req)
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("resp.Body.Close() error = %v", closeErr)
		}
	}()
	requireAppTestStatus(t, resp, http.StatusOK, "delete segment")
}

func appTestListSegments(t *testing.T, server *httptest.Server, host string) []json.RawMessage {
	t.Helper()

	req := newAppTestRequest(t, http.MethodGet, server.URL+"/policy/api/v1/infra/segments")
	req.Host = host
	req.SetBasicAuth(appsqlite.DefaultAdminUsername, appsqlite.DefaultAdminPassword)
	resp := doAppTestRequest(t, server, req)
	defer closeAppTestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			t.Fatalf("ReadAll() error = %v", readErr)
		}
		t.Fatalf("list segments for host %q StatusCode = %d, want 200, body %q", host, resp.StatusCode, string(body))
	}

	var decoded struct {
		Results []json.RawMessage `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	return decoded.Results
}

func appTestCreateManagerIPSet(t *testing.T, server *httptest.Server, host string, payload map[string]any) {
	t.Helper()

	requestBody, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	req := newAppTestRequestWithBody(t, http.MethodPost, server.URL+"/api/v1/ip-sets", requestBody)
	req.Host = host
	req.SetBasicAuth(appsqlite.DefaultAdminUsername, appsqlite.DefaultAdminPassword)
	resp := doAppTestRequest(t, server, req)
	defer closeAppTestBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		responseBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			t.Fatalf("ReadAll() error = %v", readErr)
		}
		t.Fatalf("create manager ip set StatusCode = %d, want 201, body %q", resp.StatusCode, string(responseBody))
	}
}

func appTestSearchDisplayNames(t *testing.T, server *httptest.Server, host string, query string) []string {
	t.Helper()

	searchURL := server.URL + "/api/v1/search/query?query=" + url.QueryEscape(query)
	req := newAppTestRequest(t, http.MethodGet, searchURL)
	req.Host = host
	req.SetBasicAuth(appsqlite.DefaultAdminUsername, appsqlite.DefaultAdminPassword)
	resp := doAppTestRequest(t, server, req)
	defer closeAppTestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			t.Fatalf("ReadAll() error = %v", readErr)
		}
		t.Fatalf("search host %q StatusCode = %d, want 200, body %q", host, resp.StatusCode, string(body))
	}

	var decoded struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	names := make([]string, 0, len(decoded.Results))
	for _, result := range decoded.Results {
		name, ok := result["display_name"].(string)
		if !ok {
			t.Fatalf("display_name = %#v, want string", result["display_name"])
		}
		names = append(names, name)
	}
	return names
}

func requireAppTestAuthenticatedSegmentsList(
	t *testing.T,
	server *httptest.Server,
	host string,
	password string,
	wantStatus int,
) {
	t.Helper()

	req := newAppTestRequest(t, http.MethodGet, server.URL+"/policy/api/v1/infra/segments")
	req.Host = host
	req.SetBasicAuth(appsqlite.DefaultAdminUsername, password)
	resp := doAppTestRequest(t, server, req)
	defer closeAppTestBody(t, resp)
	if resp.StatusCode != wantStatus {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			t.Fatalf("ReadAll() error = %v", readErr)
		}
		t.Fatalf("segments list host %q StatusCode = %d, want %d, body %q", host, resp.StatusCode, wantStatus, string(body))
	}
}

func appTestCountRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()

	var query string
	switch table {
	case "operation_log":
		query = "SELECT count(*) FROM operation_log"
	default:
		t.Fatalf("unsupported count table %q", table)
	}
	var count int
	if err := db.QueryRowContext(context.Background(), query).Scan(&count); err != nil {
		t.Fatalf("count rows in %s error = %v", table, err)
	}
	return count
}

func appTestCountMarkedForDelete(t *testing.T, db *sql.DB) int {
	t.Helper()

	var count int
	err := db.QueryRowContext(
		context.Background(),
		"SELECT count(*) FROM resources WHERE marked_for_delete = 1",
	).Scan(&count)
	if err != nil {
		t.Fatalf("count tombstones error = %v", err)
	}
	return count
}

func requireAppTestStatus(t *testing.T, resp *http.Response, wantStatus int, action string) {
	t.Helper()

	if resp.StatusCode == wantStatus {
		return
	}
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		t.Fatalf("ReadAll() error = %v", readErr)
	}
	t.Fatalf("%s StatusCode = %d, want %d, body %q", action, resp.StatusCode, wantStatus, string(body))
}

func appTableExists(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()

	var count int
	err := db.QueryRowContext(
		context.Background(),
		"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
		table,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query sqlite_master for %q error = %v", table, err)
	}
	return count == 1
}
