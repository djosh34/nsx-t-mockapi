package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nsx-t-mockapi/internal/clock"

	"go.uber.org/zap"
)

func TestPolicyGroupsListRequiresAuthAndReturnsEmptyList(t *testing.T) {
	t.Parallel()

	server := newHTTPAPITestServer(t, nil)
	groupsURL := server.URL + "/policy/api/v1/infra/domains/default/groups"

	req := newHTTPAPITestRequestWithoutAuth(t, http.MethodGet, groupsURL, nil)
	resp := doHTTPAPITestRequest(t, server, req)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated StatusCode = %d, want 401", resp.StatusCode)
	}

	req = newHTTPAPITestRequest(t, http.MethodGet, server.URL+"/policy/api/v1/infra/domains/default/groups", nil)
	resp = doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authenticated StatusCode = %d, want 200", resp.StatusCode)
	}

	var decoded listResult
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if decoded.ResultCount != 0 {
		t.Fatalf("ResultCount = %d, want 0", decoded.ResultCount)
	}
	if len(decoded.Results) != 0 {
		t.Fatalf("Results count = %d, want 0", len(decoded.Results))
	}
	if decoded.SortBy != "display_name" {
		t.Fatalf("SortBy = %q, want display_name", decoded.SortBy)
	}
	if !decoded.SortAscending {
		t.Fatal("SortAscending = false, want true")
	}
}

//nolint:cyclop // One end-to-end CRUD scenario keeps revision/tombstone behavior connected through real HTTP.
func TestPolicyGroupPutGetListPatchRevisionAndDeleteThroughHTTP(t *testing.T) {
	t.Parallel()

	server := newHTTPAPITestServer(t, nil)
	groupsURL := server.URL + "/policy/api/v1/infra/domains/default/groups"
	webURL := groupsURL + "/web"

	putResp := doJSONHTTPAPITestRequest(
		t,
		server,
		http.MethodPut,
		webURL,
		`{"display_name":"Web","description":"initial","resource_type":"Group"}`,
	)
	defer closeHTTPAPITestBody(t, putResp)
	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT create StatusCode = %d, want 200", putResp.StatusCode)
	}
	created := decodeHTTPAPITestObject(t, putResp)
	requireHTTPAPITestString(t, created, "id", "web")
	requireHTTPAPITestString(t, created, "display_name", "Web")
	requireHTTPAPITestString(t, created, "path", "/infra/domains/default/groups/web")
	requireHTTPAPITestString(t, created, "parent_path", "/infra/domains/default")
	requireHTTPAPITestString(t, created, "relative_path", "web")
	requireHTTPAPITestString(t, created, "resource_type", "Group")
	requireHTTPAPITestString(t, created, "state", "SUCCESS")
	requireHTTPAPITestRevision(t, created, 0)
	requireHTTPAPITestBool(t, created, "marked_for_delete", false)

	getResp := doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, webURL, nil))
	if getResp.StatusCode != http.StatusOK {
		closeHTTPAPITestBody(t, getResp)
		t.Fatalf("GET StatusCode = %d, want 200", getResp.StatusCode)
	}
	read := decodeHTTPAPITestObject(t, getResp)
	closeHTTPAPITestBody(t, getResp)
	requireHTTPAPITestString(t, read, "display_name", "Web")
	requireHTTPAPITestRevision(t, read, 0)

	for _, group := range []struct {
		id   string
		body string
	}{
		{id: "api", body: `{"display_name":"API"}`},
		{id: "zulu", body: `{"display_name":"Zulu"}`},
	} {
		resp := doJSONHTTPAPITestRequest(t, server, http.MethodPut, groupsURL+"/"+group.id, group.body)
		closeHTTPAPITestBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PUT %s StatusCode = %d, want 200", group.id, resp.StatusCode)
		}
	}

	listResp := doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, groupsURL, nil))
	defer closeHTTPAPITestBody(t, listResp)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list StatusCode = %d, want 200", listResp.StatusCode)
	}
	list := decodeHTTPAPITestList(t, listResp)
	if list.ResultCount != 3 {
		t.Fatalf("list ResultCount = %d, want 3", list.ResultCount)
	}
	requireHTTPAPITestResultNames(t, list.Results, "API", "Web", "Zulu")

	staleResp := doJSONHTTPAPITestRequest(t, server, http.MethodPut, webURL, `{"display_name":"stale"}`)
	closeHTTPAPITestBody(t, staleResp)
	if staleResp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("PUT missing revision StatusCode = %d, want 412", staleResp.StatusCode)
	}

	updateResp := doJSONHTTPAPITestRequest(
		t,
		server,
		http.MethodPut,
		webURL,
		`{"display_name":"Web Updated","description":"updated","_revision":0}`,
	)
	defer closeHTTPAPITestBody(t, updateResp)
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT update StatusCode = %d, want 200", updateResp.StatusCode)
	}
	updated := decodeHTTPAPITestObject(t, updateResp)
	requireHTTPAPITestString(t, updated, "display_name", "Web Updated")
	requireHTTPAPITestRevision(t, updated, 1)

	staleResp = doJSONHTTPAPITestRequest(t, server, http.MethodPut, webURL, `{"display_name":"stale","_revision":0}`)
	closeHTTPAPITestBody(t, staleResp)
	if staleResp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("PUT stale revision StatusCode = %d, want 412", staleResp.StatusCode)
	}

	patchResp := doJSONHTTPAPITestRequest(t, server, http.MethodPatch, webURL, `{"description":"patched"}`)
	closeHTTPAPITestBody(t, patchResp)
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH StatusCode = %d, want 200", patchResp.StatusCode)
	}
	getResp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, webURL, nil))
	if getResp.StatusCode != http.StatusOK {
		closeHTTPAPITestBody(t, getResp)
		t.Fatalf("GET patched StatusCode = %d, want 200", getResp.StatusCode)
	}
	patched := decodeHTTPAPITestObject(t, getResp)
	closeHTTPAPITestBody(t, getResp)
	requireHTTPAPITestString(t, patched, "display_name", "Web Updated")
	requireHTTPAPITestString(t, patched, "description", "patched")
	requireHTTPAPITestRevision(t, patched, 2)

	patchCreateResp := doJSONHTTPAPITestRequest(
		t,
		server,
		http.MethodPatch,
		server.URL+"/policy/api/v1/infra/domains/default/groups/patched-create",
		`{"display_name":"Patched Create"}`,
	)
	closeHTTPAPITestBody(t, patchCreateResp)
	if patchCreateResp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH create StatusCode = %d, want 200", patchCreateResp.StatusCode)
	}

	deleteResp := doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodDelete, webURL, nil))
	closeHTTPAPITestBody(t, deleteResp)
	if deleteResp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE StatusCode = %d, want 200", deleteResp.StatusCode)
	}
	getResp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, webURL, nil))
	closeHTTPAPITestBody(t, getResp)
	if getResp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET deleted StatusCode = %d, want 404", getResp.StatusCode)
	}
	deleteResp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodDelete, webURL, nil))
	closeHTTPAPITestBody(t, deleteResp)
	if deleteResp.StatusCode != http.StatusNotFound {
		t.Fatalf("DELETE missing StatusCode = %d, want 404", deleteResp.StatusCode)
	}
}

func TestPolicyGroupInvalidPayloadsReturnBadRequest(t *testing.T) {
	t.Parallel()

	server := newHTTPAPITestServer(t, nil)
	groupURL := server.URL + "/policy/api/v1/infra/domains/default/groups/invalid"
	longDisplayName := strings.Repeat("x", 256)

	for name, body := range map[string]string{
		"malformed":           `{"display_name":`,
		"wrong resource type": `{"resource_type":"IPAddressExpression"}`,
		"long display name":   `{"display_name":"` + longDisplayName + `"}`,
		"long description":    `{"description":"` + strings.Repeat("x", 1025) + `"}`,
		"group type max":      `{"group_type":["IPAddress","Generic"]}`,
		"extended max":        `{"extended_expression":[{"resource_type":"Condition"},{"resource_type":"Condition"}]}`,
		"bad expression even": `{"expression":[{"resource_type":"Condition"},{"resource_type":"Condition"}]}`,
		"bad expression pos":  `{"expression":[{"resource_type":"ConjunctionOperator"}]}`,
	} {
		resp := doJSONHTTPAPITestRequest(t, server, http.MethodPut, groupURL, body)
		closeHTTPAPITestBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s StatusCode = %d, want 400", name, resp.StatusCode)
		}
	}
}

func TestPolicyGroupExpressionsAndMemberListsThroughHTTP(t *testing.T) {
	t.Parallel()

	server := newHTTPAPITestServer(t, nil)
	groupURL := server.URL + "/policy/api/v1/infra/domains/default/groups/web"
	resp := doJSONHTTPAPITestRequest(t, server, http.MethodPut, groupURL, `{
		"display_name":"Web",
		"expression":[
			{"resource_type":"IPAddressExpression","ip_addresses":["10.0.0.1"]},
			{"resource_type":"ConjunctionOperator","conjunction_operator":"OR"},
			{"resource_type":"PathExpression","paths":["/infra/domains/default/groups/nested"]}
		]
	}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT group StatusCode = %d, want 200", resp.StatusCode)
	}

	missingExprResp := doJSONHTTPAPITestRequest(
		t,
		server,
		http.MethodPatch,
		server.URL+"/policy/api/v1/infra/domains/default/groups/missing/ip-address-expressions/ip1",
		`{"resource_type":"IPAddressExpression","ip_addresses":["10.0.0.2"]}`,
	)
	closeHTTPAPITestBody(t, missingExprResp)
	if missingExprResp.StatusCode != http.StatusNotFound {
		t.Fatalf("PATCH expression missing group StatusCode = %d, want 404", missingExprResp.StatusCode)
	}

	ipExpressionURL := groupURL + "/ip-address-expressions/ip1"
	resp = doJSONHTTPAPITestRequest(
		t,
		server,
		http.MethodPatch,
		ipExpressionURL,
		`{"resource_type":"IPAddressExpression","ip_addresses":["10.0.0.2","10.0.0.3"]}`,
	)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH IPAddressExpression StatusCode = %d, want 200", resp.StatusCode)
	}

	resp = doJSONHTTPAPITestRequest(
		t,
		server,
		http.MethodPost,
		ipExpressionURL+"?action=add",
		`{"ip_addresses":["10.0.0.3","10.0.0.4"]}`,
	)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST add StatusCode = %d, want 200", resp.StatusCode)
	}
	resp = doJSONHTTPAPITestRequest(
		t,
		server,
		http.MethodPost,
		ipExpressionURL+"?action=remove",
		`{"ip_addresses":["10.0.0.2"]}`,
	)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST remove StatusCode = %d, want 200", resp.StatusCode)
	}

	resp = doJSONHTTPAPITestRequest(
		t,
		server,
		http.MethodPost,
		ipExpressionURL+"?action=bad",
		`{"ip_addresses":["10.0.0.5"]}`,
	)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("POST invalid action StatusCode = %d, want 404", resp.StatusCode)
	}
	resp = doJSONHTTPAPITestRequest(
		t,
		server,
		http.MethodPatch,
		ipExpressionURL,
		`{"resource_type":"IPAddressExpression","ip_addresses":["10.0.0.1","fd00::1"]}`,
	)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("PATCH mixed IP family StatusCode = %d, want 400", resp.StatusCode)
	}

	pathExpressionURL := groupURL + "/path-expressions/paths1"
	resp = doJSONHTTPAPITestRequest(
		t,
		server,
		http.MethodPatch,
		pathExpressionURL,
		`{"resource_type":"PathExpression","paths":["/infra/domains/default/groups/app","/infra/segments/seg-a"]}`,
	)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH PathExpression StatusCode = %d, want 200", resp.StatusCode)
	}

	ipMembers := getHTTPAPITestList(t, server, groupURL+"/members/ip-addresses")
	requireHTTPAPITestStringResults(t, ipMembers.Results, "10.0.0.1", "10.0.0.3", "10.0.0.4")
	groupMembers := getHTTPAPITestList(t, server, groupURL+"/members/ip-groups")
	requireHTTPAPITestMemberPaths(
		t,
		groupMembers.Results,
		"/infra/domains/default/groups/nested",
		"/infra/domains/default/groups/app",
	)
	segmentMembers := getHTTPAPITestList(t, server, groupURL+"/members/segments")
	requireHTTPAPITestMemberPaths(t, segmentMembers.Results, "/infra/segments/seg-a")

	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodDelete, ipExpressionURL, nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE IPAddressExpression StatusCode = %d, want 200", resp.StatusCode)
	}
	ipMembers = getHTTPAPITestList(t, server, groupURL+"/members/ip-addresses")
	requireHTTPAPITestStringResults(t, ipMembers.Results, "10.0.0.1")
}

func TestPolicyGroupHTTPStateFollowsRealizationDelay(t *testing.T) {
	t.Parallel()

	fakeClock := clock.NewFakeClock(time.Date(2026, 5, 17, 10, 30, 0, 0, time.UTC))
	server := newHTTPAPITestServer(t, func(opts *AppOptions) {
		opts.Clock = fakeClock
		opts.Config.Realization.CreateDelayMS = 5000
	})
	groupURL := server.URL + "/policy/api/v1/infra/domains/default/groups/web"

	resp := doJSONHTTPAPITestRequest(t, server, http.MethodPut, groupURL, `{"display_name":"Web"}`)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT StatusCode = %d, want 200", resp.StatusCode)
	}
	created := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestString(t, created, "state", "IN_PROGRESS")

	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, groupURL, nil))
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET before delay StatusCode = %d, want 200", resp.StatusCode)
	}
	beforeDelay := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestString(t, beforeDelay, "state", "IN_PROGRESS")

	fakeClock.Advance(5 * time.Second)
	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, groupURL, nil))
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET after delay StatusCode = %d, want 200", resp.StatusCode)
	}
	afterDelay := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestString(t, afterDelay, "state", "SUCCESS")
}

func newHTTPAPITestServer(t *testing.T, configure func(*AppOptions)) *httptest.Server {
	t.Helper()

	db := openHTTPAPITestDB(t)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close() error = %v", err)
		}
	})

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

func doJSONHTTPAPITestRequest(
	t *testing.T,
	server *httptest.Server,
	method string,
	url string,
	body string,
) *http.Response {
	t.Helper()

	req := newHTTPAPITestRequest(t, method, url, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", contentTypeJSON)
	return doHTTPAPITestRequest(t, server, req)
}

func decodeHTTPAPITestObject(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()

	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("Decode() object error = %v", err)
	}
	return decoded
}

func decodeHTTPAPITestList(t *testing.T, resp *http.Response) listResult {
	t.Helper()

	var decoded listResult
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("Decode() list error = %v", err)
	}
	return decoded
}

func getHTTPAPITestList(t *testing.T, server *httptest.Server, url string) listResult {
	t.Helper()

	resp := doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, url, nil))
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s StatusCode = %d, want 200", url, resp.StatusCode)
	}
	return decodeHTTPAPITestList(t, resp)
}

func requireHTTPAPITestString(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()

	got, ok := payload[key].(string)
	if !ok {
		t.Fatalf("payload[%q] = %#v, want string %q", key, payload[key], want)
	}
	if got != want {
		t.Fatalf("payload[%q] = %q, want %q", key, got, want)
	}
}

func requireHTTPAPITestRevision(t *testing.T, payload map[string]any, want float64) {
	t.Helper()

	got, ok := payload["_revision"].(float64)
	if !ok {
		t.Fatalf("payload[_revision] = %#v, want number %.0f", payload["_revision"], want)
	}
	if got != want {
		t.Fatalf("payload[_revision] = %.0f, want %.0f", got, want)
	}
}

func requireHTTPAPITestBool(t *testing.T, payload map[string]any, key string, want bool) {
	t.Helper()

	got, ok := payload[key].(bool)
	if !ok {
		t.Fatalf("payload[%q] = %#v, want bool %v", key, payload[key], want)
	}
	if got != want {
		t.Fatalf("payload[%q] = %v, want %v", key, got, want)
	}
}

func requireHTTPAPITestResultNames(t *testing.T, results []json.RawMessage, want ...string) {
	t.Helper()

	if len(results) != len(want) {
		t.Fatalf("results count = %d, want %d", len(results), len(want))
	}
	for index, wantName := range want {
		if index >= len(results) {
			t.Fatalf("wanted index %d exceeds result count %d", index, len(results))
		}
		var payload map[string]any
		if err := json.Unmarshal(results[index], &payload); err != nil {
			t.Fatalf("Unmarshal() result %d error = %v", index, err)
		}
		requireHTTPAPITestString(t, payload, "display_name", wantName)
	}
}

func requireHTTPAPITestStringResults(t *testing.T, results []json.RawMessage, want ...string) {
	t.Helper()

	if len(results) != len(want) {
		t.Fatalf("string results count = %d, want %d", len(results), len(want))
	}
	for index, wantValue := range want {
		raw := results[index]
		var got string
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("Unmarshal() string result %d error = %v", index, err)
		}
		if got != wantValue {
			t.Fatalf("string result[%d] = %q, want %q", index, got, wantValue)
		}
	}
}

func requireHTTPAPITestMemberPaths(t *testing.T, results []json.RawMessage, want ...string) {
	t.Helper()

	if len(results) != len(want) {
		t.Fatalf("member results count = %d, want %d", len(results), len(want))
	}
	for index, wantPath := range want {
		raw := results[index]
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("Unmarshal() member result %d error = %v", index, err)
		}
		requireHTTPAPITestString(t, got, "path", wantPath)
	}
}
