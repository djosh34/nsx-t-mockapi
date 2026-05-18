package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nsx-t-mockapi/internal/clock"
	appsqlite "nsx-t-mockapi/internal/sqlite"

	"go.uber.org/zap"
)

func TestPolicyEULAAcceptanceRequiresAuthAndReturnsAcceptanceObject(t *testing.T) {
	t.Parallel()

	server := newHTTPAPITestServer(t, nil)
	eulaURL := server.URL + "/policy/api/v1/eula/acceptance"

	resp := doHTTPAPITestRequest(t, server, newHTTPAPITestRequestWithoutAuth(t, http.MethodGet, eulaURL, nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated StatusCode = %d, want 401", resp.StatusCode)
	}

	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, eulaURL, nil))
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authenticated StatusCode = %d, want 200", resp.StatusCode)
	}
	payload := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestString(t, payload, "resource_type", "EULAAcceptance")
	requireHTTPAPITestString(t, payload, "id", "acceptance")
	requireHTTPAPITestBool(t, payload, "acceptance", true)
}

//nolint:cyclop // One HTTP scenario keeps policy, rule, embedded-rule, statistics, and tombstone behavior connected.
func TestSecurityPolicyAndRuleRoutesThroughHTTP(t *testing.T) {
	t.Parallel()

	server := newHTTPAPITestServer(t, nil)
	policiesURL := server.URL + "/policy/api/v1/infra/domains/default/security-policies"
	policyURL := policiesURL + "/web-policy"

	list := getHTTPAPITestList(t, server, policiesURL)
	if list.ResultCount != 0 {
		t.Fatalf("initial security policy ResultCount = %d, want 0", list.ResultCount)
	}

	resp := doJSONHTTPAPITestRequest(t, server, http.MethodPut, policyURL, `{
		"display_name":"Web Policy",
		"resource_type":"SecurityPolicy",
		"category":"Application",
		"sequence_number":10
	}`)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT policy create StatusCode = %d, want 200", resp.StatusCode)
	}
	created := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestString(t, created, "path", "/infra/domains/default/security-policies/web-policy")
	requireHTTPAPITestString(t, created, "resource_type", "SecurityPolicy")
	requireHTTPAPITestRevision(t, created, 0)

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPut, policyURL, `{"display_name":"stale"}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("PUT policy missing revision StatusCode = %d, want 412", resp.StatusCode)
	}

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPatch, policyURL, `{"display_name":"Web Policy Patched"}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH policy StatusCode = %d, want 200", resp.StatusCode)
	}

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, policyURL+"?action=revise", `{
		"display_name":"Web Policy Revised",
		"sequence_number":20
	}`)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revise policy StatusCode = %d, want 200", resp.StatusCode)
	}
	revised := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestNumber(t, revised, "sequence_number", 20)

	rulesURL := policyURL + "/rules"
	ruleURL := rulesURL + "/allow-web"
	list = getHTTPAPITestList(t, server, rulesURL)
	if list.ResultCount != 0 {
		t.Fatalf("initial rule ResultCount = %d, want 0", list.ResultCount)
	}

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPut, ruleURL, `{
		"display_name":"Allow Web",
		"resource_type":"Rule",
		"action":"ALLOW",
		"direction":"IN_OUT",
		"ip_protocol":"IPV4_IPV6",
		"source_groups":["ANY"],
		"destination_groups":["/infra/domains/default/groups/app"],
		"services":["/infra/services/HTTP"],
		"sequence_number":5
	}`)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT rule StatusCode = %d, want 200", resp.StatusCode)
	}
	rule := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestString(t, rule, "path", "/infra/domains/default/security-policies/web-policy/rules/allow-web")
	requireHTTPAPITestRevision(t, rule, 0)

	resp = doJSONHTTPAPITestRequest(
		t,
		server,
		http.MethodPatch,
		ruleURL,
		`{"display_name":"Allow Web Patched","action":"DROP"}`,
	)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH rule StatusCode = %d, want 200", resp.StatusCode)
	}

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, ruleURL+"?action=revise", `{
		"display_name":"Allow Web Revised",
		"action":"ALLOW",
		"sequence_number":15
	}`)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revise rule StatusCode = %d, want 200", resp.StatusCode)
	}
	revisedRule := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestNumber(t, revisedRule, "sequence_number", 15)

	stats := getHTTPAPITestList(t, server, ruleURL+"/statistics")
	if stats.ResultCount != 0 {
		t.Fatalf("rule stats ResultCount = %d, want 0", stats.ResultCount)
	}
	policyStats := getHTTPAPITestList(t, server, policyURL+"/statistics")
	if policyStats.ResultCount != 1 {
		t.Fatalf("policy stats ResultCount = %d, want 1", policyStats.ResultCount)
	}

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPut, policyURL, `{
		"display_name":"Web Policy Replacement",
		"_revision":2,
		"rules":[
			{"id":"deny-db","display_name":"Deny DB","resource_type":"Rule","action":"DROP","sequence_number":10},
			{"id":"allow-api","display_name":"Allow API","resource_type":"Rule","action":"ALLOW","sequence_number":10}
		]
	}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT policy embedded rules StatusCode = %d, want 200", resp.StatusCode)
	}
	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, policyURL, nil))
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET policy with embedded rules StatusCode = %d, want 200", resp.StatusCode)
	}
	policyWithRules := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestEmbeddedRuleNames(t, policyWithRules, "Allow API", "Deny DB")

	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodDelete, ruleURL, nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("DELETE replaced rule StatusCode = %d, want 404", resp.StatusCode)
	}

	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodDelete, policyURL, nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE policy StatusCode = %d, want 200", resp.StatusCode)
	}
	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, policyURL, nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET deleted policy StatusCode = %d, want 404", resp.StatusCode)
	}
}

func TestSecurityPolicyAndRuleInvalidPayloadsReturnBadRequest(t *testing.T) {
	t.Parallel()

	server := newHTTPAPITestServer(t, nil)
	policyURL := server.URL + "/policy/api/v1/infra/domains/default/security-policies/invalid"
	for name, body := range map[string]string{
		"malformed":           `{"display_name":`,
		"wrong resource type": `{"resource_type":"Rule"}`,
		"long display name":   `{"display_name":"` + strings.Repeat("x", 256) + `"}`,
		"long description":    `{"description":"` + strings.Repeat("x", 1025) + `"}`,
		"too many tags":       `{"tags":[` + strings.Repeat(`{},`, 30) + `{}]}`,
		"bad category":        `{"category":"Bad"}`,
		"bad sequence":        `{"sequence_number":1000000}`,
		"too much scope":      `{"scope":[` + quotedCSV("g", 129) + `]}`,
		"bad rule":            `{"rules":[{"resource_type":"SecurityPolicy"}]}`,
	} {
		resp := doJSONHTTPAPITestRequest(t, server, http.MethodPut, policyURL, body)
		closeHTTPAPITestBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s policy StatusCode = %d, want 400", name, resp.StatusCode)
		}
	}

	resp := doJSONHTTPAPITestRequest(t, server, http.MethodPut, policyURL, `{"display_name":"Valid"}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT valid parent policy StatusCode = %d, want 200", resp.StatusCode)
	}

	ruleURL := policyURL + "/rules/invalid-rule"
	for name, body := range map[string]string{
		"wrong resource type": `{"resource_type":"SecurityPolicy"}`,
		"bad action":          `{"action":"PASS"}`,
		"bad direction":       `{"direction":"SIDEWAYS"}`,
		"bad protocol":        `{"ip_protocol":"IPV10"}`,
		"bad sequence":        `{"sequence_number":-1}`,
		"any mixed":           `{"source_groups":["ANY","/infra/domains/default/groups/app"]}`,
		"too many services":   `{"services":[` + quotedCSV("s", 129) + `]}`,
		"long notes":          `{"notes":"` + strings.Repeat("x", 2049) + `"}`,
	} {
		resp = doJSONHTTPAPITestRequest(t, server, http.MethodPut, ruleURL, body)
		closeHTTPAPITestBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s rule StatusCode = %d, want 400", name, resp.StatusCode)
		}
	}
}

func TestSegmentRoutesStateStatisticsAndValidationThroughHTTP(t *testing.T) {
	t.Parallel()

	fakeClock := clock.NewFakeClock(time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC))
	server := newHTTPAPITestServer(t, func(opts *AppOptions) {
		opts.Clock = fakeClock
		opts.Config.Realization.CreateDelayMS = 5000
	})
	segmentsURL := server.URL + "/policy/api/v1/infra/segments"
	segmentURL := segmentsURL + "/web-seg"

	resp := doJSONHTTPAPITestRequest(t, server, http.MethodPut, segmentURL, `{
		"display_name":"Web Segment",
		"resource_type":"Segment",
		"replication_mode":"MTEP",
		"admin_state":"UP",
		"subnets":[{"gateway_address":"10.10.0.1/24","dhcp_ranges":["10.10.0.10-10.10.0.20"]}]
	}`)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT infra segment StatusCode = %d, want 200", resp.StatusCode)
	}
	created := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestString(t, created, "state", "IN_PROGRESS")

	state := getHTTPAPITestObject(t, server, segmentURL+"/state")
	requireHTTPAPITestString(t, state, "segment_path", "/infra/segments/web-seg")
	requireHTTPAPITestString(t, state, "state", "in_progress")
	stateList := getHTTPAPITestList(t, server, segmentsURL+"/state")
	if stateList.ResultCount != 1 {
		t.Fatalf("segment state ResultCount = %d, want 1", stateList.ResultCount)
	}

	fakeClock.Advance(5 * time.Second)
	state = getHTTPAPITestObject(t, server, segmentURL+"/state")
	requireHTTPAPITestString(t, state, "state", "success")
	stats := getHTTPAPITestObject(t, server, segmentURL+"/statistics")
	requireHTTPAPITestString(t, stats, "logical_switch_id", "web-seg")

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPut, segmentURL, `{"display_name":"stale"}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("PUT segment missing revision StatusCode = %d, want 412", resp.StatusCode)
	}
	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPatch, segmentURL, `{"display_name":"Patched Segment"}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH segment StatusCode = %d, want 200", resp.StatusCode)
	}
	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodDelete, segmentURL+"?force=true", nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("force DELETE segment StatusCode = %d, want 200", resp.StatusCode)
	}

	for name, body := range map[string]string{
		"wrong resource type": `{"resource_type":"Rule"}`,
		"bad replication":     `{"replication_mode":"BAD"}`,
		"bad admin":           `{"admin_state":"MAYBE"}`,
		"too many subnets":    `{"subnets":[{},{}]}`,
		"bad cidr":            `{"subnets":[{"gateway_address":"10.10.0.1"}]}`,
		"too many tags":       `{"tags":[` + strings.Repeat(`{},`, 30) + `{}]}`,
	} {
		resp = doJSONHTTPAPITestRequest(t, server, http.MethodPut, segmentsURL+"/invalid-"+name, body)
		closeHTTPAPITestBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s segment StatusCode = %d, want 400", name, resp.StatusCode)
		}
	}
}

//nolint:cyclop // One end-to-end route interaction test keeps gateway, segment, and global-derived behavior connected.
func TestTierOneSegmentGatewayAndGlobalDerivedRoutesThroughHTTP(t *testing.T) {
	t.Parallel()

	server := newHTTPAPITestServer(t, nil)
	tier1URL := server.URL + "/policy/api/v1/infra/tier-1s/app-t1"
	tier1SegmentsURL := tier1URL + "/segments"

	tier0List := getHTTPAPITestList(t, server, server.URL+"/policy/api/v1/infra/tier-0s")
	if tier0List.ResultCount != 0 {
		t.Fatalf("tier0 ResultCount = %d, want 0", tier0List.ResultCount)
	}
	tier1List := getHTTPAPITestList(t, server, server.URL+"/policy/api/v1/infra/tier-1s")
	if tier1List.ResultCount != 0 {
		t.Fatalf("tier1 ResultCount = %d, want 0", tier1List.ResultCount)
	}
	resp := doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, tier1URL, nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET missing tier1 StatusCode = %d, want 404", resp.StatusCode)
	}

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPut, tier1SegmentsURL+"/app-seg", `{
		"display_name":"App Segment",
		"resource_type":"Segment",
		"connectivity_path":"/infra/tier-1s/app-t1"
	}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT tier1 segment StatusCode = %d, want 200", resp.StatusCode)
	}
	segment := getHTTPAPITestObject(t, server, tier1SegmentsURL+"/app-seg")
	requireHTTPAPITestString(t, segment, "parent_path", "/infra/tier-1s/app-t1")

	state := getHTTPAPITestObject(t, server, tier1SegmentsURL+"/app-seg/state")
	requireHTTPAPITestString(t, state, "segment_path", "/infra/tier-1s/app-t1/segments/app-seg")
	stats := getHTTPAPITestObject(t, server, tier1SegmentsURL+"/app-seg/statistics")
	requireHTTPAPITestString(t, stats, "logical_switch_id", "app-seg")
	stateList := getHTTPAPITestList(t, server, tier1SegmentsURL+"/state")
	if stateList.ResultCount != 1 {
		t.Fatalf("tier1 segment state ResultCount = %d, want 1", stateList.ResultCount)
	}

	globalState := getHTTPAPITestObject(
		t,
		server,
		server.URL+"/policy/api/v1/global-infra/tier-1s/app-t1/segments/app-seg/state",
	)
	requireHTTPAPITestString(t, globalState, "state", "success")
	globalStats := getHTTPAPITestObject(
		t,
		server,
		server.URL+"/policy/api/v1/global-infra/tier-1s/app-t1/segments/app-seg/statistics",
	)
	requireHTTPAPITestString(t, globalStats, "logical_switch_id", "app-seg")

	groupURL := server.URL + "/policy/api/v1/infra/domains/default/groups/web"
	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPut, groupURL, `{"display_name":"Web"}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT group StatusCode = %d, want 200", resp.StatusCode)
	}
	globalMembers := getHTTPAPITestList(
		t,
		server,
		server.URL+"/policy/api/v1/global-infra/domains/default/groups/web/members/consolidated-effective-ip-addresses",
	)
	if globalMembers.ResultCount != 0 {
		t.Fatalf("global members ResultCount = %d, want 0", globalMembers.ResultCount)
	}

	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodPost, tier1SegmentsURL+"/app-seg", nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST segment StatusCode = %d, want 405", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); !strings.Contains(allow, http.MethodGet) ||
		!strings.Contains(allow, http.MethodPut) {
		t.Fatalf("Allow = %q, want GET and PUT", allow)
	}
}

func TestTierOneReadListAndStateUseStoredGatewayRealization(t *testing.T) {
	t.Parallel()

	fakeClock := clock.NewFakeClock(time.Date(2026, 5, 17, 13, 0, 0, 0, time.UTC))
	server := newHTTPAPITestServerWithSeed(t, func(db *sql.DB) {
		cfg := httpAPITestConfig(t)
		cfg.Realization.CreateDelayMS = 5000
		store := appsqlite.NewResourceStore(db, appsqlite.ResourceStoreOptions{
			Clock:       fakeClock,
			Realization: cfg.Realization,
			Logger:      zap.NewNop(),
		})
		if err := store.EnsureBootstrap(context.Background()); err != nil {
			t.Fatalf("EnsureBootstrap() error = %v", err)
		}
		_, err := store.Mutate(context.Background(), appsqlite.Mutation{
			Spec:          tier1Spec("edge-a"),
			Body:          json.RawMessage(`{"display_name":"Edge A"}`),
			Username:      appsqlite.DefaultAdminUsername,
			Operation:     appsqlite.ResourceOperationCreate,
			RequestPath:   "/policy/api/v1/infra/tier-1s/edge-a",
			RouteTemplate: tier1RouteTemplate,
			StatusCode:    http.StatusOK,
		})
		if err != nil {
			t.Fatalf("Mutate() tier1 error = %v", err)
		}
	}, func(opts *AppOptions) {
		opts.Clock = fakeClock
		opts.Config.Realization.CreateDelayMS = 5000
	})

	list := getHTTPAPITestList(t, server, server.URL+"/policy/api/v1/infra/tier-1s")
	if list.ResultCount != 1 {
		t.Fatalf("tier1 ResultCount = %d, want 1", list.ResultCount)
	}
	tier1 := getHTTPAPITestObject(t, server, server.URL+"/policy/api/v1/infra/tier-1s/edge-a")
	requireHTTPAPITestString(t, tier1, "display_name", "Edge A")
	state := getHTTPAPITestObject(t, server, server.URL+"/policy/api/v1/infra/tier-1s/edge-a/state")
	requireHTTPAPITestNestedString(t, state, "tier1_state", "state", "in_progress")

	fakeClock.Advance(5 * time.Second)
	state = getHTTPAPITestObject(t, server, server.URL+"/policy/api/v1/infra/tier-1s/edge-a/state")
	requireHTTPAPITestNestedString(t, state, "tier1_state", "state", "success")
}

func TestPolicyBodyRoutesRejectNonObjectBodies(t *testing.T) {
	t.Parallel()

	server := newHTTPAPITestServer(t, nil)
	policyURL := server.URL + "/policy/api/v1/infra/domains/default/security-policies/body"
	for name, body := range map[string]string{
		"empty":  "",
		"array":  `[]`,
		"scalar": `"bad"`,
		"null":   `null`,
	} {
		resp := doJSONHTTPAPITestRequest(t, server, http.MethodPut, policyURL+"-"+name, body)
		closeHTTPAPITestBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s body StatusCode = %d, want 400", name, resp.StatusCode)
		}
	}
}

func getHTTPAPITestObject(t *testing.T, server *httptest.Server, url string) map[string]any {
	t.Helper()

	resp := doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, url, nil))
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s StatusCode = %d, want 200", url, resp.StatusCode)
	}
	return decodeHTTPAPITestObject(t, resp)
}

func newHTTPAPITestServerWithSeed(
	t *testing.T,
	seed func(*sql.DB),
	configure func(*AppOptions),
) *httptest.Server {
	t.Helper()

	db := openHTTPAPITestDB(t)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close() error = %v", err)
		}
	})
	seed(db)

	opts := AppOptions{Config: httpAPITestConfig(t), DB: db, Logger: zap.NewNop()}
	if configure != nil {
		configure(&opts)
	}
	handler, err := NewHandler(context.Background(), opts)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func requireHTTPAPITestNumber(t *testing.T, payload map[string]any, key string, want float64) {
	t.Helper()

	got, ok := payload[key].(float64)
	if !ok {
		t.Fatalf("payload[%q] = %#v, want number %.0f", key, payload[key], want)
	}
	if got != want {
		t.Fatalf("payload[%q] = %.0f, want %.0f", key, got, want)
	}
}

func requireHTTPAPITestNestedString(t *testing.T, payload map[string]any, key string, nestedKey string, want string) {
	t.Helper()

	nested, ok := payload[key].(map[string]any)
	if !ok {
		t.Fatalf("payload[%q] = %#v, want object", key, payload[key])
	}
	requireHTTPAPITestString(t, nested, nestedKey, want)
}

func requireHTTPAPITestEmbeddedRuleNames(t *testing.T, payload map[string]any, want ...string) {
	t.Helper()

	rawRules, ok := payload["rules"].([]any)
	if !ok {
		t.Fatalf("payload[rules] = %#v, want array", payload["rules"])
	}
	if len(rawRules) != len(want) {
		t.Fatalf("rules count = %d, want %d", len(rawRules), len(want))
	}
	for index, wantName := range want {
		rule, isObject := rawRules[index].(map[string]any)
		if !isObject {
			t.Fatalf("rules[%d] = %#v, want object", index, rawRules[index])
		}
		requireHTTPAPITestString(t, rule, "display_name", wantName)
	}
}

func quotedCSV(prefix string, count int) string {
	values := make([]string, 0, count)
	for index := range count {
		values = append(values, `"`+prefix+string(rune('a'+index%26))+`"`)
	}
	return strings.Join(values, ",")
}
