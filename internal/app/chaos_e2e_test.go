package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"nsx-t-mockapi/internal/clock"
	appsqlite "nsx-t-mockapi/internal/sqlite"

	"go.uber.org/zap"
)

const appChaosRaceUpdates = 6

var errAppChaosUnexpectedStatus = errors.New("unexpected chaos HTTP status")

type appChaosHarness struct {
	built  *Application
	server *httptest.Server
	clock  *clock.FakeClock
}

type appChaosHost struct {
	Host   string
	Prefix string
}

type appChaosResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

type appChaosList struct {
	Results     []map[string]any `json:"results"`
	ResultCount int              `json:"result_count"` //nolint:tagliatelle // NSX APIs use snake_case.
}

//nolint:funlen,cyclop,gocognit // One outside-in chaos scenario keeps connected app-boundary evidence.
func TestBuiltApplicationSurvivesChaoticHTTPTrafficAndSettles(t *testing.T) {
	t.Parallel()

	harness := newAppChaosHarness(t)
	defer closeAppChaosHarness(t, harness)

	hosts := []appChaosHost{
		{Host: appTestManagerOne, Prefix: "ChaosOne"},
		{Host: appTestManagerTwo, Prefix: "ChaosTwo"},
	}
	for _, host := range hosts {
		appChaosSeedHost(t, harness, host)
	}

	for _, host := range hosts {
		resp := harness.do(
			t,
			host.Host,
			http.MethodPost,
			"/policy/api/v1/infra/domains/default/groups",
			nil,
		)
		requireAppChaosStatus(t, resp, http.StatusMethodNotAllowed, host.Host+" documented policy wrong method")
		if allow := resp.Header.Get("Allow"); !strings.Contains(allow, http.MethodGet) {
			t.Fatalf("%s policy wrong method Allow = %q, want GET", host.Host, allow)
		}
		resp = harness.do(t, host.Host, http.MethodPatch, "/api/v1/ip-sets", nil)
		requireAppChaosStatus(t, resp, http.StatusMethodNotAllowed, host.Host+" documented manager wrong method")
		if allow := resp.Header.Get("Allow"); !strings.Contains(allow, http.MethodPost) {
			t.Fatalf("%s manager wrong method Allow = %q, want POST", host.Host, allow)
		}
	}

	chaosOps := make([]func() error, 0, len(hosts)*12)
	for _, host := range hosts {
		current := host
		segmentBody := appChaosJSONBody(t, map[string]any{
			"display_name":  current.Prefix + "Segment",
			"resource_type": "Segment",
		})
		groupBody := appChaosJSONBody(t, map[string]any{
			"display_name":  current.Prefix + "Group",
			"resource_type": "Group",
			"tags":          []map[string]string{{"scope": "chaos", "tag": current.Prefix}},
		})
		managerStaleBody := appChaosJSONBody(t, map[string]any{
			"display_name":  current.Prefix + "ManagerStale",
			"resource_type": "IPSet",
			"ip_addresses":  []string{"192.0.2.44"},
			"_revision":     7,
		})
		policyStaleBody := appChaosJSONBody(t, map[string]any{
			"display_name":  current.Prefix + "PolicyStale",
			"resource_type": "Group",
			"_revision":     7,
		})
		chaosOps = append(chaosOps,
			func() error {
				return harness.expectStatus(
					current.Host,
					http.MethodPatch,
					"/policy/api/v1/infra/segments/"+strings.ToLower(current.Prefix)+"-segment",
					segmentBody,
					appsqlite.DefaultAdminPassword,
					http.StatusOK,
				)
			},
			func() error {
				return harness.expectStatus(
					current.Host,
					http.MethodPatch,
					"/policy/api/v1/infra/domains/default/groups/"+strings.ToLower(current.Prefix)+"-group",
					groupBody,
					appsqlite.DefaultAdminPassword,
					http.StatusOK,
				)
			},
			func() error {
				return harness.expectStatus(
					current.Host,
					http.MethodPatch,
					"/policy/api/v1/infra/domains/default/groups/"+strings.ToLower(current.Prefix)+"-malformed",
					[]byte(`{"display_name":`),
					appsqlite.DefaultAdminPassword,
					http.StatusBadRequest,
				)
			},
			func() error {
				return harness.expectStatus(
					current.Host,
					http.MethodGet,
					"/policy/api/v1/infra/domains/default/groups",
					nil,
					"",
					http.StatusUnauthorized,
				)
			},
			func() error {
				return harness.expectStatus(
					current.Host,
					http.MethodGet,
					"/policy/api/v1/infra/domains/default/does-not-exist",
					nil,
					appsqlite.DefaultAdminPassword,
					http.StatusNotFound,
				)
			},
			func() error {
				return harness.expectStatus(
					current.Host,
					http.MethodPut,
					"/api/v1/ip-sets/"+strings.ToLower(current.Prefix)+"-stale",
					managerStaleBody,
					appsqlite.DefaultAdminPassword,
					http.StatusConflict,
				)
			},
			func() error {
				return harness.expectStatus(
					current.Host,
					http.MethodPut,
					"/policy/api/v1/infra/domains/default/groups/"+strings.ToLower(current.Prefix)+"-stale",
					policyStaleBody,
					appsqlite.DefaultAdminPassword,
					http.StatusPreconditionFailed,
				)
			},
			func() error {
				return harness.expectStatus(
					current.Host,
					http.MethodGet,
					"/policy/api/v1/search/query",
					nil,
					appsqlite.DefaultAdminPassword,
					http.StatusBadRequest,
				)
			},
			func() error {
				return harness.expectStatus(
					current.Host,
					http.MethodGet,
					"/policy/api/v1/search/query?query="+url.QueryEscape("resource_type:Group AND"),
					nil,
					appsqlite.DefaultAdminPassword,
					http.StatusBadRequest,
				)
			},
			func() error {
				return harness.expectStatus(
					current.Host,
					http.MethodGet,
					"/policy/api/v1/search/dsl?query="+url.QueryEscape("Group where display_name ="),
					nil,
					appsqlite.DefaultAdminPassword,
					http.StatusBadRequest,
				)
			},
		)
	}
	if err := appConcurrentRun(chaosOps); err != nil {
		t.Fatalf("chaotic HTTP operations error = %v", err)
	}

	for _, host := range hosts {
		winnerCount, conflictCount := appChaosRaceManagerUpdates(t, harness, host)
		if winnerCount != 1 {
			t.Fatalf("%s concurrent manager revision winners = %d, want 1", host.Host, winnerCount)
		}
		if conflictCount != appChaosRaceUpdates-1 {
			t.Fatalf("%s concurrent manager revision conflicts = %d, want %d", host.Host, conflictCount, appChaosRaceUpdates-1)
		}
	}

	for _, host := range hosts {
		statePath := "/policy/api/v1/infra/segments/" + strings.ToLower(host.Prefix) + "-segment/state"
		state := harness.do(t, host.Host, http.MethodGet, statePath, nil)
		requireAppChaosStatus(t, state, http.StatusOK, host.Host+" segment state before fake-clock settle")
		requireAppChaosString(t, decodeAppChaosObject(t, state), "state", "in_progress")
	}
	harness.clock.Advance(5 * time.Second)
	for _, host := range hosts {
		statePath := "/policy/api/v1/infra/segments/" + strings.ToLower(host.Prefix) + "-segment/state"
		state := harness.do(t, host.Host, http.MethodGet, statePath, nil)
		requireAppChaosStatus(t, state, http.StatusOK, host.Host+" segment state after fake-clock settle")
		requireAppChaosString(t, decodeAppChaosObject(t, state), "state", "success")
	}

	for _, host := range hosts {
		deletePath := "/policy/api/v1/infra/domains/default/groups/" + strings.ToLower(host.Prefix) + "-delete"
		deleted := harness.do(t, host.Host, http.MethodDelete, deletePath, nil)
		requireAppChaosStatus(t, deleted, http.StatusOK, host.Host+" delete policy tombstone target")
		normalSearch := harness.do(
			t,
			host.Host,
			http.MethodGet,
			"/policy/api/v1/search/query?query="+url.QueryEscape("display_name:"+host.Prefix+"Delete"),
			nil,
		)
		requireAppChaosStatus(t, normalSearch, http.StatusOK, host.Host+" normal tombstone-hidden search")
		if list := decodeAppChaosList(t, normalSearch); list.ResultCount != 0 {
			t.Fatalf("%s normal tombstone search ResultCount = %d, want 0", host.Host, list.ResultCount)
		}
		tombstoneSearch := harness.do(
			t,
			host.Host,
			http.MethodGet,
			"/policy/api/v1/search/query?query="+url.QueryEscape("marked_for_delete:true AND resource_type:Group"),
			nil,
		)
		requireAppChaosStatus(t, tombstoneSearch, http.StatusOK, host.Host+" marked-for-delete search")
		requireAppChaosSearchName(t, decodeAppChaosList(t, tombstoneSearch), host.Prefix+"Delete")
	}

	for _, host := range hosts {
		other := hosts[0]
		if other.Host == host.Host {
			other = hosts[1]
		}
		groups := harness.do(t, host.Host, http.MethodGet, "/policy/api/v1/infra/domains/default/groups", nil)
		requireAppChaosStatus(t, groups, http.StatusOK, host.Host+" groups list after chaos")
		requireAppChaosNames(t, host.Host, decodeAppChaosList(t, groups).Results, host.Prefix, other.Prefix)
		segments := harness.do(t, host.Host, http.MethodGet, "/policy/api/v1/infra/segments", nil)
		requireAppChaosStatus(t, segments, http.StatusOK, host.Host+" segments list after chaos")
		requireAppChaosNames(t, host.Host, decodeAppChaosList(t, segments).Results, host.Prefix, other.Prefix)
		booleanQuery := "display_name:" + host.Prefix + "* AND (resource_type:Group OR resource_type:Segment)"
		query := harness.do(
			t,
			host.Host,
			http.MethodGet,
			"/policy/api/v1/search/query?query="+url.QueryEscape(booleanQuery),
			nil,
		)
		requireAppChaosStatus(t, query, http.StatusOK, host.Host+" boolean search after chaos")
		requireAppChaosNames(t, host.Host, decodeAppChaosList(t, query).Results, host.Prefix, other.Prefix)
		dslCaseInsensitive := harness.do(
			t,
			host.Host,
			http.MethodGet,
			"/policy/api/v1/search/dsl?query="+url.QueryEscape("group where display_name like "+host.Prefix),
			nil,
		)
		requireAppChaosStatus(t, dslCaseInsensitive, http.StatusOK, host.Host+" case-insensitive DSL search")
		requireAppChaosSearchName(t, decodeAppChaosList(t, dslCaseInsensitive), host.Prefix+"Group")
		dslNestedTags := harness.do(
			t,
			host.Host,
			http.MethodGet,
			"/policy/api/v1/search/dsl?query="+url.QueryEscape("Group where tags.scope = chaos"),
			nil,
		)
		requireAppChaosStatus(t, dslNestedTags, http.StatusOK, host.Host+" nested tag DSL search")
		requireAppChaosNames(t, host.Host, decodeAppChaosList(t, dslNestedTags).Results, host.Prefix, other.Prefix)
	}

	dbs := make(map[string]appsqlite.ManagerDatabase, len(hosts))
	for _, host := range hosts {
		managerDB, err := harness.built.ManagerDatabases.ResolveManagerDatabase(context.Background(), host.Host)
		if err != nil {
			t.Fatalf("ResolveManagerDatabase(%s) error = %v", host.Host, err)
		}
		dbs[host.Host] = managerDB
		if info, statErr := os.Stat(managerDB.Path); statErr != nil {
			t.Fatalf("manager database %q Stat() error = %v", managerDB.Path, statErr)
		} else if info.IsDir() {
			t.Fatalf("manager database %q is directory, want file", managerDB.Path)
		}
		operationRows := appChaosCountRows(t, managerDB.DB, "SELECT count(*) FROM operation_log")
		tombstoneRows := appChaosCountRows(t, managerDB.DB, "SELECT count(*) FROM resources WHERE marked_for_delete = 1")
		if operationRows < 7 {
			t.Fatalf("%s operation_log rows = %d, want at least 7", host.Host, operationRows)
		}
		if tombstoneRows != 1 {
			t.Fatalf("%s tombstone rows = %d, want 1", host.Host, tombstoneRows)
		}
		t.Logf(
			"chaos SQLite evidence: host=%s manager=%s path=%s operation_log_rows=%d tombstone_rows=%d",
			host.Host,
			managerDB.Name,
			managerDB.Path,
			operationRows,
			tombstoneRows,
		)
	}

	appConcurrentChangeAdminPassword(t, dbs[appTestManagerOne].DB, "chaos_one_password")
	authIsolationErr := appConcurrentRun([]func() error{
		func() error {
			return harness.expectStatus(
				appTestManagerOne,
				http.MethodGet,
				"/policy/api/v1/infra/segments",
				nil,
				appsqlite.DefaultAdminPassword,
				http.StatusUnauthorized,
			)
		},
		func() error {
			return harness.expectStatus(
				appTestManagerOne,
				http.MethodGet,
				"/policy/api/v1/infra/segments",
				nil,
				"chaos_one_password",
				http.StatusOK,
			)
		},
		func() error {
			return harness.expectStatus(
				appTestManagerTwo,
				http.MethodGet,
				"/policy/api/v1/infra/segments",
				nil,
				appsqlite.DefaultAdminPassword,
				http.StatusOK,
			)
		},
	})
	if authIsolationErr != nil {
		t.Fatalf("auth isolation after chaos error = %v", authIsolationErr)
	}
}

func newAppChaosHarness(t *testing.T) appChaosHarness {
	t.Helper()

	fakeClock := clock.NewFakeClock(time.Date(2026, 5, 18, 9, 0, 0, 0, time.UTC))
	dbPath := filepath.Join(t.TempDir(), "data", "nsx-t-mockapi.db")
	configPath := writeAppConcurrentConfig(t, dbPath)
	built, err := Build(context.Background(), Options{ConfigPath: configPath, Logger: zap.NewNop(), Clock: fakeClock})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	server := httptest.NewServer(built.Server.Handler)
	return appChaosHarness{built: built, server: server, clock: fakeClock}
}

func closeAppChaosHarness(t *testing.T, harness appChaosHarness) {
	t.Helper()

	if harness.server != nil {
		harness.server.Close()
	}
	closeAppTestApplication(t, harness.built)
}

func appChaosSeedHost(t *testing.T, harness appChaosHarness, host appChaosHost) {
	t.Helper()

	requests := []struct {
		method string
		path   string
		body   map[string]any
		status int
	}{
		{
			method: http.MethodPatch,
			path:   "/policy/api/v1/infra/domains/default/groups/" + strings.ToLower(host.Prefix) + "-stale",
			body: map[string]any{
				"display_name":  host.Prefix + "PolicyStaleBase",
				"resource_type": "Group",
			},
			status: http.StatusOK,
		},
		{
			method: http.MethodPatch,
			path:   "/policy/api/v1/infra/domains/default/groups/" + strings.ToLower(host.Prefix) + "-delete",
			body: map[string]any{
				"display_name":  host.Prefix + "Delete",
				"resource_type": "Group",
			},
			status: http.StatusOK,
		},
		{
			method: http.MethodPost,
			path:   "/api/v1/ip-sets",
			body: appConcurrentIPSetPayload(
				strings.ToLower(host.Prefix)+"-stale",
				host.Prefix+"ManagerStaleBase",
				"192.0.2.10",
			),
			status: http.StatusCreated,
		},
		{
			method: http.MethodPost,
			path:   "/api/v1/ip-sets",
			body: appConcurrentIPSetPayload(
				strings.ToLower(host.Prefix)+"-race",
				host.Prefix+"ManagerRaceBase",
				"192.0.2.20",
			),
			status: http.StatusCreated,
		},
	}
	for _, request := range requests {
		resp := harness.do(
			t,
			host.Host,
			request.method,
			request.path,
			appChaosJSONBody(t, request.body),
		)
		requireAppChaosStatus(t, resp, request.status, host.Host+" seed "+request.path)
	}
}

func appChaosRaceManagerUpdates(t *testing.T, harness appChaosHarness, host appChaosHost) (int, int) {
	t.Helper()

	results := make(chan appChaosRaceResult, appChaosRaceUpdates)
	var wg sync.WaitGroup
	for index := range appChaosRaceUpdates {
		updateIndex := index
		wg.Go(func() {
			body, err := json.Marshal(map[string]any{
				"display_name":  fmt.Sprintf("%sManagerRace%d", host.Prefix, updateIndex),
				"resource_type": "IPSet",
				"ip_addresses":  []string{fmt.Sprintf("198.51.100.%d", updateIndex+1)},
				"_revision":     0,
			})
			if err != nil {
				results <- appChaosRaceResult{Err: fmt.Errorf("marshal manager race update: %w", err)}
				return
			}
			resp, err := harness.request(
				host.Host,
				http.MethodPut,
				"/api/v1/ip-sets/"+strings.ToLower(host.Prefix)+"-race",
				body,
				appsqlite.DefaultAdminPassword,
			)
			if err != nil {
				results <- appChaosRaceResult{Err: err}
				return
			}
			results <- appChaosRaceResult{StatusCode: resp.StatusCode}
		})
	}
	wg.Wait()
	close(results)

	var winnerCount int
	var conflictCount int
	for result := range results {
		if result.Err != nil {
			t.Fatalf("%s race update error = %v", host.Host, result.Err)
		}
		switch result.StatusCode {
		case http.StatusOK:
			winnerCount++
		case http.StatusConflict:
			conflictCount++
		default:
			t.Fatalf("%s race update StatusCode = %d, want 200 or 409", host.Host, result.StatusCode)
		}
	}
	return winnerCount, conflictCount
}

type appChaosRaceResult struct {
	StatusCode int
	Err        error
}

func appChaosJSONBody(t testing.TB, payload map[string]any) []byte {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return body
}

func (h appChaosHarness) expectStatus(
	host string,
	method string,
	path string,
	body []byte,
	password string,
	want int,
) error {
	resp, err := h.request(host, method, path, body, password)
	if err != nil {
		return err
	}
	if resp.StatusCode != want {
		return fmt.Errorf(
			"%w: %s %s host %s StatusCode = %d, want %d, body %q",
			errAppChaosUnexpectedStatus,
			method,
			path,
			host,
			resp.StatusCode,
			want,
			string(resp.Body),
		)
	}
	return nil
}

func (h appChaosHarness) do(
	t *testing.T,
	host string,
	method string,
	path string,
	body []byte,
) appChaosResponse {
	t.Helper()

	resp, err := h.request(host, method, path, body, appsqlite.DefaultAdminPassword)
	if err != nil {
		t.Fatalf("request %s %s host %s error = %v", method, path, host, err)
	}
	return resp
}

func (h appChaosHarness) request(
	host string,
	method string,
	path string,
	body []byte,
	password string,
) (appChaosResponse, error) {
	req, err := http.NewRequestWithContext(context.Background(), method, h.server.URL+path, bytes.NewReader(body))
	if err != nil {
		return appChaosResponse{}, fmt.Errorf("new request %s %s host %s: %w", method, path, host, err)
	}
	req.Host = host
	if password != "" {
		req.SetBasicAuth(appsqlite.DefaultAdminUsername, password)
	}
	resp, err := h.server.Client().Do(req)
	if err != nil {
		return appChaosResponse{}, fmt.Errorf("do request %s %s host %s: %w", method, path, host, err)
	}
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			return appChaosResponse{}, fmt.Errorf(
				"read response body for %s %s host %s: %w; close response body: %w",
				method,
				path,
				host,
				err,
				closeErr,
			)
		}
		return appChaosResponse{}, fmt.Errorf("read response body for %s %s host %s: %w", method, path, host, err)
	}
	if closeErr := resp.Body.Close(); closeErr != nil {
		return appChaosResponse{}, fmt.Errorf("close response body for %s %s host %s: %w", method, path, host, closeErr)
	}
	return appChaosResponse{StatusCode: resp.StatusCode, Header: resp.Header.Clone(), Body: responseBody}, nil
}

func requireAppChaosStatus(t *testing.T, resp appChaosResponse, want int, action string) {
	t.Helper()

	if resp.StatusCode != want {
		t.Fatalf("%s StatusCode = %d, want %d, body %q", action, resp.StatusCode, want, string(resp.Body))
	}
}

func decodeAppChaosObject(t *testing.T, resp appChaosResponse) map[string]any {
	t.Helper()

	var decoded map[string]any
	if err := json.Unmarshal(resp.Body, &decoded); err != nil {
		t.Fatalf("Unmarshal object error = %v, body %q", err, string(resp.Body))
	}
	return decoded
}

func decodeAppChaosList(t *testing.T, resp appChaosResponse) appChaosList {
	t.Helper()

	var decoded appChaosList
	if err := json.Unmarshal(resp.Body, &decoded); err != nil {
		t.Fatalf("Unmarshal list error = %v, body %q", err, string(resp.Body))
	}
	return decoded
}

func requireAppChaosString(t *testing.T, payload map[string]any, field string, want string) {
	t.Helper()

	got, ok := payload[field].(string)
	if !ok {
		t.Fatalf("%s = %#v, want string %q", field, payload[field], want)
	}
	if got != want {
		t.Fatalf("%s = %q, want %q", field, got, want)
	}
}

func requireAppChaosNames(
	t *testing.T,
	host string,
	results []map[string]any,
	wantPrefix string,
	forbiddenPrefix string,
) {
	t.Helper()

	if len(results) == 0 {
		t.Fatalf("%s results empty, want prefix %q", host, wantPrefix)
	}
	var names []string
	for _, result := range results {
		displayName, ok := result["display_name"].(string)
		if !ok {
			continue
		}
		names = append(names, displayName)
		if strings.HasPrefix(displayName, forbiddenPrefix) {
			t.Fatalf("%s leaked display_name %q with forbidden prefix %q", host, displayName, forbiddenPrefix)
		}
	}
	if len(names) == 0 {
		t.Fatalf("%s has no named results in %#v", host, results)
	}
	if !slices.ContainsFunc(names, func(name string) bool { return strings.HasPrefix(name, wantPrefix) }) {
		t.Fatalf("%s names %v missing prefix %q", host, names, wantPrefix)
	}
}

func requireAppChaosSearchName(t *testing.T, list appChaosList, want string) {
	t.Helper()

	var names []string
	for _, result := range list.Results {
		name, ok := result["display_name"].(string)
		if !ok {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if !slices.Contains(names, want) {
		t.Fatalf("search display_name %q absent from %v", want, names)
	}
}

func appChaosCountRows(t *testing.T, db *sql.DB, query string) int {
	t.Helper()

	var count int
	if err := db.QueryRowContext(context.Background(), query).Scan(&count); err != nil {
		t.Fatalf("count rows query %q error = %v", query, err)
	}
	return count
}
