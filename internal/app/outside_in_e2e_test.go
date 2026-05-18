package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nsx-t-mockapi/internal/clock"
	appsqlite "nsx-t-mockapi/internal/sqlite"

	"go.uber.org/zap"
)

const appOutsideInManagerHost = "nsx-t-e2e"

type appOutsideInHarness struct {
	built  *Application
	server *httptest.Server
	clock  *clock.FakeClock
}

type appOutsideInResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

type appOutsideInList struct {
	Results     []map[string]any `json:"results"`
	ResultCount int              `json:"result_count"` //nolint:tagliatelle // NSX APIs use snake_case.
}

//nolint:funlen // One client journey keeps the app boundary evidence connected.
func TestBuiltApplicationOutsideInPolicyManagerSearchWrongCodesAndSQLite(t *testing.T) {
	t.Parallel()

	harness := newAppOutsideInHarness(t)
	defer closeAppOutsideInHarness(t, harness)

	unauthorizedGroups := harness.do(t, http.MethodGet, "/policy/api/v1/infra/domains/default/groups", nil, false)
	requireAppOutsideInStatus(t, unauthorizedGroups, http.StatusUnauthorized, "missing auth groups list")

	groups := harness.do(t, http.MethodGet, "/policy/api/v1/infra/domains/default/groups", nil, true)
	requireAppOutsideInStatus(t, groups, http.StatusOK, "initial groups list")
	if list := decodeAppOutsideInList(t, groups); list.ResultCount != 0 {
		t.Fatalf("initial groups ResultCount = %d, want 0", list.ResultCount)
	}

	groupPath := "/policy/api/v1/infra/domains/default/groups/web"
	group := harness.doJSON(t, http.MethodPatch, groupPath, map[string]any{
		"display_name":  "E2EWebGroup",
		"resource_type": "Group",
	})
	requireAppOutsideInStatus(t, group, http.StatusOK, "PATCH policy group")

	group = harness.do(t, http.MethodGet, groupPath, nil, true)
	requireAppOutsideInStatus(t, group, http.StatusOK, "GET policy group before delay")
	groupObject := decodeAppOutsideInObject(t, group)
	requireAppOutsideInString(t, groupObject, "path", "/infra/domains/default/groups/web")
	requireAppOutsideInString(t, groupObject, "resource_type", "Group")
	requireAppOutsideInString(t, groupObject, "state", "IN_PROGRESS")
	requireAppOutsideInNumber(t, groupObject, "_revision", 0)
	groups = harness.do(t, http.MethodGet, "/policy/api/v1/infra/domains/default/groups", nil, true)
	requireAppOutsideInStatus(t, groups, http.StatusOK, "groups list after create")
	if list := decodeAppOutsideInList(t, groups); list.ResultCount != 1 {
		t.Fatalf("groups ResultCount = %d, want 1", list.ResultCount)
	}

	harness.clock.Advance(5 * time.Second)
	group = harness.do(t, http.MethodGet, groupPath, nil, true)
	requireAppOutsideInStatus(t, group, http.StatusOK, "GET policy group after delay")
	requireAppOutsideInString(t, decodeAppOutsideInObject(t, group), "state", "SUCCESS")

	segmentPath := "/policy/api/v1/infra/segments/web-seg"
	segment := harness.doJSON(t, http.MethodPut, segmentPath, map[string]any{
		"display_name":     "E2EWebSegment",
		"resource_type":    "Segment",
		"replication_mode": "MTEP",
		"admin_state":      "UP",
	})
	requireAppOutsideInStatus(t, segment, http.StatusOK, "PUT infra segment")
	requireAppOutsideInString(t, decodeAppOutsideInObject(t, segment), "state", "IN_PROGRESS")
	segmentState := harness.do(t, http.MethodGet, segmentPath+"/state", nil, true)
	requireAppOutsideInStatus(t, segmentState, http.StatusOK, "GET infra segment state before delay")
	requireAppOutsideInString(t, decodeAppOutsideInObject(t, segmentState), "state", "in_progress")
	harness.clock.Advance(5 * time.Second)
	segmentState = harness.do(t, http.MethodGet, segmentPath+"/state", nil, true)
	requireAppOutsideInStatus(t, segmentState, http.StatusOK, "GET infra segment state after delay")
	requireAppOutsideInString(t, decodeAppOutsideInObject(t, segmentState), "state", "success")

	policyPath := "/policy/api/v1/infra/domains/default/security-policies/web-policy"
	policy := harness.doJSON(t, http.MethodPut, policyPath, map[string]any{
		"display_name":    "E2EWebPolicy",
		"resource_type":   "SecurityPolicy",
		"category":        "Application",
		"sequence_number": float64(10),
	})
	requireAppOutsideInStatus(t, policy, http.StatusOK, "PUT security policy")
	policyObject := decodeAppOutsideInObject(t, policy)
	requireAppOutsideInString(t, policyObject, "path", "/infra/domains/default/security-policies/web-policy")
	requireAppOutsideInString(t, policyObject, "resource_type", "SecurityPolicy")
	requireAppOutsideInNumber(t, policyObject, "_revision", 0)

	policy = harness.doJSON(t, http.MethodPut, policyPath, map[string]any{
		"display_name":    "E2EWebPolicyUpdated",
		"category":        "Application",
		"sequence_number": float64(20),
		"_revision":       float64(0),
	})
	requireAppOutsideInStatus(t, policy, http.StatusOK, "PUT security policy with current revision")
	requireAppOutsideInString(t, decodeAppOutsideInObject(t, policy), "display_name", "E2EWebPolicyUpdated")

	rulePath := policyPath + "/rules/allow-web"
	rule := harness.doJSON(t, http.MethodPut, rulePath, map[string]any{
		"display_name":       "E2EAllowWeb",
		"resource_type":      "Rule",
		"action":             "ALLOW",
		"direction":          "IN_OUT",
		"ip_protocol":        "IPV4_IPV6",
		"source_groups":      []string{"ANY"},
		"destination_groups": []string{"/infra/domains/default/groups/web"},
		"services":           []string{"/infra/services/HTTP"},
		"sequence_number":    float64(5),
	})
	requireAppOutsideInStatus(t, rule, http.StatusOK, "PUT security policy rule")
	requireAppOutsideInString(
		t,
		decodeAppOutsideInObject(t, rule),
		"path",
		"/infra/domains/default/security-policies/web-policy/rules/allow-web",
	)
	policyStats := harness.do(t, http.MethodGet, policyPath+"/statistics", nil, true)
	requireAppOutsideInStatus(t, policyStats, http.StatusOK, "GET security policy statistics")
	if list := decodeAppOutsideInList(t, policyStats); list.ResultCount != 1 {
		t.Fatalf("policy statistics ResultCount = %d, want 1", list.ResultCount)
	}

	ipSetPath := "/api/v1/ip-sets/web-ips"
	ipSet := harness.doJSON(t, http.MethodPost, "/api/v1/ip-sets", map[string]any{
		"id":            "web-ips",
		"display_name":  "E2EWebIPs",
		"resource_type": "IPSet",
		"ip_addresses":  []string{"10.0.0.1"},
	})
	requireAppOutsideInStatus(t, ipSet, http.StatusCreated, "POST manager IPSet")
	requireAppOutsideInString(t, decodeAppOutsideInObject(t, ipSet), "path", "/api/v1/ip-sets/web-ips")
	ipSet = harness.do(t, http.MethodGet, ipSetPath, nil, true)
	requireAppOutsideInStatus(t, ipSet, http.StatusOK, "GET manager IPSet")
	requireAppOutsideInString(t, decodeAppOutsideInObject(t, ipSet), "display_name", "E2EWebIPs")

	staleIPSet := harness.doJSON(t, http.MethodPut, ipSetPath, map[string]any{
		"display_name": "E2EStaleIPs",
		"ip_addresses": []string{"10.0.0.2"},
		"_revision":    float64(7),
	})
	requireAppOutsideInStatus(t, staleIPSet, http.StatusConflict, "PUT stale manager IPSet")
	ipSet = harness.doJSON(t, http.MethodPut, ipSetPath, map[string]any{
		"display_name": "E2EUpdatedIPs",
		"ip_addresses": []string{"10.0.0.2"},
		"_revision":    float64(0),
	})
	requireAppOutsideInStatus(t, ipSet, http.StatusOK, "PUT manager IPSet with current revision")
	requireAppOutsideInString(t, decodeAppOutsideInObject(t, ipSet), "display_name", "E2EUpdatedIPs")

	added := harness.doJSON(t, http.MethodPost, ipSetPath+"?action=add_ip", map[string]any{
		"ip_address": "10.0.0.3",
	})
	requireAppOutsideInStatus(t, added, http.StatusCreated, "add IPSet member")
	members := harness.do(t, http.MethodGet, ipSetPath+"/members", nil, true)
	requireAppOutsideInStatus(t, members, http.StatusOK, "list IPSet members after add")
	requireAppOutsideInIPMembers(t, decodeAppOutsideInList(t, members), "10.0.0.2", "10.0.0.3")
	removed := harness.doJSON(t, http.MethodPost, ipSetPath+"?action=remove_ip", map[string]any{
		"ip_address": "10.0.0.2",
	})
	requireAppOutsideInStatus(t, removed, http.StatusCreated, "remove IPSet member")
	members = harness.do(t, http.MethodGet, ipSetPath+"/members", nil, true)
	requireAppOutsideInStatus(t, members, http.StatusOK, "list IPSet members after remove")
	requireAppOutsideInIPMembers(t, decodeAppOutsideInList(t, members), "10.0.0.3")

	policySearch := harness.do(
		t,
		http.MethodGet,
		"/policy/api/v1/search/query?query="+url.QueryEscape("display_name:E2EWebGroup AND resource_type:Group"),
		nil,
		true,
	)
	requireAppOutsideInStatus(t, policySearch, http.StatusOK, "policy search query")
	requireAppOutsideInSearchResult(
		t,
		decodeAppOutsideInList(t, policySearch),
		"/infra/domains/default/groups/web",
		"Group",
		"E2EWebGroup",
	)
	managerSearch := harness.do(
		t,
		http.MethodGet,
		"/api/v1/search/query?query="+url.QueryEscape("display_name:E2EUpdatedIPs AND resource_type:IPSet"),
		nil,
		true,
	)
	requireAppOutsideInStatus(t, managerSearch, http.StatusOK, "manager search query")
	requireAppOutsideInSearchResult(
		t,
		decodeAppOutsideInList(t, managerSearch),
		"/api/v1/ip-sets/web-ips",
		"IPSet",
		"E2EUpdatedIPs",
	)

	deleted := harness.do(t, http.MethodDelete, ipSetPath, nil, true)
	requireAppOutsideInStatus(t, deleted, http.StatusOK, "DELETE manager IPSet")
	missingIPSet := harness.do(t, http.MethodGet, ipSetPath, nil, true)
	requireAppOutsideInStatus(t, missingIPSet, http.StatusNotFound, "GET deleted manager IPSet")
	managerSearch = harness.do(
		t,
		http.MethodGet,
		"/api/v1/search/query?query="+url.QueryEscape("display_name:E2EUpdatedIPs AND resource_type:IPSet"),
		nil,
		true,
	)
	requireAppOutsideInStatus(t, managerSearch, http.StatusOK, "manager search query after hard delete")
	if list := decodeAppOutsideInList(t, managerSearch); list.ResultCount != 0 {
		t.Fatalf("manager search after delete ResultCount = %d, want 0", list.ResultCount)
	}

	missingGroup := harness.do(t, http.MethodGet, "/policy/api/v1/infra/domains/default/groups/missing", nil, true)
	requireAppOutsideInStatus(t, missingGroup, http.StatusNotFound, "GET missing policy group")
	badRequest := harness.do(
		t,
		http.MethodPut,
		"/policy/api/v1/infra/domains/default/groups/bad",
		[]byte(`{"display_name":`),
		true,
	)
	requireAppOutsideInStatus(t, badRequest, http.StatusBadRequest, "malformed policy group payload")
	methodMismatch := harness.do(t, http.MethodPost, "/policy/api/v1/infra/domains/default/groups", nil, true)
	requireAppOutsideInStatus(t, methodMismatch, http.StatusMethodNotAllowed, "method mismatch")
	if allow := methodMismatch.Header.Get("Allow"); allow == "" {
		t.Fatal("method mismatch Allow header is empty, want supported methods")
	}

	managerDB, err := harness.built.ManagerDatabases.ResolveManagerDatabase(context.Background(), appOutsideInManagerHost)
	if err != nil {
		t.Fatalf("ResolveManagerDatabase() error = %v", err)
	}
	if info, statErr := os.Stat(managerDB.Path); statErr != nil {
		t.Fatalf("manager SQLite database %q Stat() error = %v", managerDB.Path, statErr)
	} else if info.IsDir() {
		t.Fatalf("manager SQLite database %q is a directory, want file", managerDB.Path)
	}
	if count := appTestCountRows(t, managerDB.DB, "operation_log"); count != 10 {
		t.Fatalf("operation_log rows = %d, want 10", count)
	}
	t.Logf("outside-in SQLite evidence: manager=%s path=%s operation_log_rows=10", managerDB.Name, managerDB.Path)
}

func newAppOutsideInHarness(t *testing.T) appOutsideInHarness {
	t.Helper()

	fakeClock := clock.NewFakeClock(time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC))
	dbPath := filepath.Join(t.TempDir(), "data", "nsx-t-mockapi.db")
	configPath := writeAppOutsideInConfig(t, dbPath)
	built, err := Build(context.Background(), Options{ConfigPath: configPath, Logger: zap.NewNop(), Clock: fakeClock})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	server := httptest.NewServer(built.Server.Handler)
	return appOutsideInHarness{built: built, server: server, clock: fakeClock}
}

func closeAppOutsideInHarness(t *testing.T, harness appOutsideInHarness) {
	t.Helper()

	if harness.server != nil {
		harness.server.Close()
	}
	closeAppTestApplication(t, harness.built)
}

func writeAppOutsideInConfig(t *testing.T, dbPath string) string {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	config := fmt.Sprintf(`server:
  listen_addr: "127.0.0.1:0"
database:
  path: %q
realization:
  default_delay_ms: 0
  create_delay_ms: 5000
  update_delay_ms: 5000
  delete_delay_ms: 5000
search:
  default_page_size: 1000
  max_page_size: 1000
`, dbPath)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return configPath
}

func (h appOutsideInHarness) doJSON(
	t *testing.T,
	method string,
	path string,
	payload map[string]any,
) appOutsideInResponse {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return h.do(t, method, path, body, true)
}

func (h appOutsideInHarness) do(
	t *testing.T,
	method string,
	path string,
	body []byte,
	auth bool,
) appOutsideInResponse {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, h.server.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}
	req.Host = appOutsideInManagerHost
	if auth {
		req.SetBasicAuth(appsqlite.DefaultAdminUsername, appsqlite.DefaultAdminPassword)
	}
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("resp.Body.Close() error = %v", closeErr)
		}
	}()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	return appOutsideInResponse{StatusCode: resp.StatusCode, Header: resp.Header.Clone(), Body: responseBody}
}

func requireAppOutsideInStatus(t *testing.T, resp appOutsideInResponse, want int, action string) {
	t.Helper()

	if resp.StatusCode != want {
		t.Fatalf("%s StatusCode = %d, want %d, body %q", action, resp.StatusCode, want, string(resp.Body))
	}
}

func decodeAppOutsideInObject(t *testing.T, resp appOutsideInResponse) map[string]any {
	t.Helper()

	var decoded map[string]any
	if err := json.Unmarshal(resp.Body, &decoded); err != nil {
		t.Fatalf("Unmarshal object error = %v, body %q", err, string(resp.Body))
	}
	return decoded
}

func decodeAppOutsideInList(t *testing.T, resp appOutsideInResponse) appOutsideInList {
	t.Helper()

	var decoded appOutsideInList
	if err := json.Unmarshal(resp.Body, &decoded); err != nil {
		t.Fatalf("Unmarshal list error = %v, body %q", err, string(resp.Body))
	}
	return decoded
}

func requireAppOutsideInString(t *testing.T, payload map[string]any, field string, want string) {
	t.Helper()

	got, ok := payload[field].(string)
	if !ok {
		t.Fatalf("%s = %#v, want string %q", field, payload[field], want)
	}
	if got != want {
		t.Fatalf("%s = %q, want %q", field, got, want)
	}
}

func requireAppOutsideInNumber(t *testing.T, payload map[string]any, field string, want float64) {
	t.Helper()

	got, ok := payload[field].(float64)
	if !ok {
		t.Fatalf("%s = %#v, want number %v", field, payload[field], want)
	}
	if got != want {
		t.Fatalf("%s = %v, want %v", field, got, want)
	}
}

func requireAppOutsideInIPMembers(t *testing.T, list appOutsideInList, want ...string) {
	t.Helper()

	if list.ResultCount != len(want) {
		t.Fatalf("IPSet members ResultCount = %d, want %d", list.ResultCount, len(want))
	}
	got := make(map[string]bool, len(list.Results))
	for _, result := range list.Results {
		ip, ok := result["ip_address"].(string)
		if !ok {
			t.Fatalf("IPSet member ip_address = %#v, want string", result["ip_address"])
		}
		got[ip] = true
	}
	for _, ip := range want {
		if !got[ip] {
			t.Fatalf("IPSet members missing %q in %#v", ip, got)
		}
	}
}

func requireAppOutsideInSearchResult(
	t *testing.T,
	list appOutsideInList,
	wantPath string,
	wantResourceType string,
	wantDisplayName string,
) {
	t.Helper()

	for _, result := range list.Results {
		path, pathOK := result["path"].(string)
		resourceType, typeOK := result["resource_type"].(string)
		displayName, nameOK := result["display_name"].(string)
		if pathOK && typeOK && nameOK &&
			path == wantPath &&
			resourceType == wantResourceType &&
			displayName == wantDisplayName {
			return
		}
	}
	t.Fatalf(
		"search result not found: path %q resource_type %q display_name %q in %#v",
		wantPath,
		wantResourceType,
		wantDisplayName,
		list.Results,
	)
}
