package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"nsx-t-mockapi/internal/config"

	"go.uber.org/zap"
)

const (
	searchSortByDisplayName = "display_name"
	searchWebAlphaName      = "WebAlpha"
	policyWebAlphaPath      = "/infra/domains/default/groups/web-alpha"
)

func TestPolicySearchQueryRequiresAuthAndReturnsFieldedANDResults(t *testing.T) {
	t.Parallel()

	server := newSearchHTTPTestServer(t, httpAPITestConfig(t))
	createPolicySearchGroup(t, server, "web-alpha", map[string]any{
		"display_name": searchWebAlphaName,
		"description":  "frontend payment gateway",
	})
	createPolicySearchGroup(t, server, "db-beta", map[string]any{
		"display_name": "DbBeta",
		"description":  "database",
	})

	searchURL := server.URL + "/policy/api/v1/search/query?query=" +
		url.QueryEscape("display_name:"+searchWebAlphaName+" AND resource_type:Group")
	req := newHTTPAPITestRequestWithoutAuth(t, http.MethodGet, searchURL, nil)
	resp := doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated StatusCode = %d, want 401", resp.StatusCode)
	}

	req = newHTTPAPITestRequest(t, http.MethodGet, searchURL, nil)
	resp = doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authenticated StatusCode = %d, want 200", resp.StatusCode)
	}

	decoded := decodeSearchHTTPResponse(t, resp)
	requireDefaultSearchResponseMeta(t, decoded, 1)
	expectedSelfHref := "/policy/api/v1/search/query?query=" +
		url.QueryEscape("display_name:"+searchWebAlphaName+" AND resource_type:Group")
	requireDocumentedSearchResponseLinks(t, decoded, expectedSelfHref)
	result := requireSingleSearchResult(t, decoded)
	if result["display_name"] != searchWebAlphaName {
		t.Fatalf("display_name = %#v, want %s", result["display_name"], searchWebAlphaName)
	}
	if result["resource_type"] != "Group" {
		t.Fatalf("resource_type = %#v, want Group", result["resource_type"])
	}
	if result["path"] != policyWebAlphaPath {
		t.Fatalf("path = %#v, want policy group path", result["path"])
	}
}

func TestPolicySearchDSLReturnsEntityResults(t *testing.T) {
	t.Parallel()

	server := newSearchHTTPTestServer(t, httpAPITestConfig(t))
	createPolicySearchGroup(t, server, "web-alpha", map[string]any{
		"display_name": searchWebAlphaName,
		"description":  "frontend payment gateway",
	})
	createManagerSearchIPSet(t, server, map[string]any{
		"id":            "web-alpha-service",
		"display_name":  searchWebAlphaName,
		"description":   "same display name wrong entity type",
		"resource_type": "IPSet",
		"ip_addresses":  []string{"192.0.2.10"},
	})

	searchURL := server.URL + "/policy/api/v1/search/dsl?query=" + url.QueryEscape("Group")
	req := newHTTPAPITestRequest(t, http.MethodGet, searchURL, nil)
	resp := doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", resp.StatusCode)
	}

	decoded := decodeSearchHTTPResponse(t, resp)
	requireDefaultSearchResponseMeta(t, decoded, 1)
	requireDocumentedSearchResponseLinks(t, decoded, "/policy/api/v1/search/dsl?query=Group")
	result := requireSingleSearchResult(t, decoded)
	if result["display_name"] != searchWebAlphaName {
		t.Fatalf("display_name = %#v, want %s", result["display_name"], searchWebAlphaName)
	}
	if result["resource_type"] != "Group" {
		t.Fatalf("resource_type = %#v, want Group", result["resource_type"])
	}
}

func TestManagerSearchQueryReturnsDocumentedShape(t *testing.T) {
	t.Parallel()

	server := newSearchHTTPTestServer(t, httpAPITestConfig(t))
	createManagerSearchIPSet(t, server, map[string]any{
		"id":            "app-services",
		"display_name":  "AppServices",
		"description":   "manager search target",
		"resource_type": "IPSet",
		"ip_addresses":  []string{"192.0.2.10"},
	})

	searchURL := server.URL + "/api/v1/search/query?query=" +
		url.QueryEscape("display_name:AppServices AND resource_type:IPSet")
	req := newHTTPAPITestRequest(t, http.MethodGet, searchURL, nil)
	resp := doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", resp.StatusCode)
	}

	decoded := decodeSearchHTTPResponse(t, resp)
	requireDefaultSearchResponseMeta(t, decoded, 1)
	expectedSelfHref := "/api/v1/search/query?query=" +
		url.QueryEscape("display_name:AppServices AND resource_type:IPSet")
	requireDocumentedSearchResponseLinks(t, decoded, expectedSelfHref)
	result := requireSingleSearchResult(t, decoded)
	if result["display_name"] != "AppServices" {
		t.Fatalf("display_name = %#v, want AppServices", result["display_name"])
	}
	if result["resource_type"] != "IPSet" {
		t.Fatalf("resource_type = %#v, want IPSet", result["resource_type"])
	}
	if result["path"] != "/api/v1/ip-sets/app-services" {
		t.Fatalf("path = %#v, want manager ip-set path", result["path"])
	}
}

func TestManagerSearchDSLReturnsEntityResults(t *testing.T) {
	t.Parallel()

	server := newSearchHTTPTestServer(t, httpAPITestConfig(t))
	createManagerSearchIPSet(t, server, map[string]any{
		"id":            "app-services",
		"display_name":  "AppServices",
		"description":   "manager search target",
		"resource_type": "IPSet",
		"ip_addresses":  []string{"192.0.2.10"},
	})
	createPolicySearchGroup(t, server, "app-services", map[string]any{
		"display_name": "AppServices",
		"description":  "same display name wrong entity type",
	})

	searchURL := server.URL + "/api/v1/search/dsl?query=" + url.QueryEscape("IPSet")
	req := newHTTPAPITestRequest(t, http.MethodGet, searchURL, nil)
	resp := doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", resp.StatusCode)
	}

	decoded := decodeSearchHTTPResponse(t, resp)
	requireDefaultSearchResponseMeta(t, decoded, 1)
	requireDocumentedSearchResponseLinks(t, decoded, "/api/v1/search/dsl?query=IPSet")
	result := requireSingleSearchResult(t, decoded)
	if result["display_name"] != "AppServices" {
		t.Fatalf("display_name = %#v, want AppServices", result["display_name"])
	}
	if result["resource_type"] != "IPSet" {
		t.Fatalf("resource_type = %#v, want IPSet", result["resource_type"])
	}
}

func TestManagerSearchQuerySupportsFieldedORThroughHTTP(t *testing.T) {
	t.Parallel()

	server := newSearchHTTPTestServer(t, httpAPITestConfig(t))
	createManagerSearchIPSet(t, server, map[string]any{
		"id":            "app-services",
		"display_name":  "AppServices",
		"description":   "first manager OR target",
		"resource_type": "IPSet",
		"ip_addresses":  []string{"192.0.2.10"},
	})
	createManagerSearchIPSet(t, server, map[string]any{
		"id":            "db-services",
		"display_name":  "DbServices",
		"description":   "second manager OR target",
		"resource_type": "IPSet",
		"ip_addresses":  []string{"192.0.2.11"},
	})
	createManagerSearchIPSet(t, server, map[string]any{
		"id":            "cache-services",
		"display_name":  "CacheServices",
		"description":   "non target",
		"resource_type": "IPSet",
		"ip_addresses":  []string{"192.0.2.12"},
	})

	searchURL := server.URL + "/api/v1/search/query?query=" +
		url.QueryEscape("display_name:(AppServices OR DbServices) AND resource_type:IPSet")
	req := newHTTPAPITestRequest(t, http.MethodGet, searchURL, nil)
	resp := doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", resp.StatusCode)
	}

	decoded := decodeSearchHTTPResponse(t, resp)
	requireDefaultSearchResponseMeta(t, decoded, 2)
	requireSearchResponseNoCursor(t, decoded)
	requireSearchResultDisplayNames(t, decoded, "AppServices", "DbServices")
}

func TestSearchQuerySupportsWildcardsThroughHTTP(t *testing.T) {
	t.Parallel()

	server := newSearchHTTPTestServer(t, httpAPITestConfig(t))
	createPolicySearchGroup(t, server, "app-vm-1", map[string]any{"display_name": "App-VM-1"})
	createPolicySearchGroup(t, server, "app-vm-22", map[string]any{"display_name": "App-VM-22"})
	createPolicySearchGroup(t, server, "db-vm-1", map[string]any{"display_name": "Db-VM-1"})

	searchURL := server.URL + "/policy/api/v1/search/query?query=" + url.QueryEscape("display_name:App-VM-?")
	req := newHTTPAPITestRequest(t, http.MethodGet, searchURL, nil)
	resp := doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", resp.StatusCode)
	}

	decoded := decodeSearchHTTPResponse(t, resp)
	requireDefaultSearchResponseMeta(t, decoded, 1)
	requireSearchResultDisplayNames(t, decoded, "App-VM-1")

	searchURL = server.URL + "/policy/api/v1/search/query?query=" + url.QueryEscape("display_name:App*")
	req = newHTTPAPITestRequest(t, http.MethodGet, searchURL, nil)
	resp = doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("star wildcard StatusCode = %d, want 200", resp.StatusCode)
	}

	decoded = decodeSearchHTTPResponse(t, resp)
	requireDefaultSearchResponseMeta(t, decoded, 2)
	requireSearchResultDisplayNames(t, decoded, "App-VM-1", "App-VM-22")
}

func TestSearchQueryIncludedFieldsProjectsTopLevelResultFields(t *testing.T) {
	t.Parallel()

	server := newSearchHTTPTestServer(t, httpAPITestConfig(t))
	createPolicySearchGroup(t, server, "web-alpha", map[string]any{
		"display_name": "WebAlpha",
		"description":  "should be omitted",
		"tags":         []map[string]string{{"scope": "prod", "tag": "frontend"}},
	})

	searchURL := server.URL + "/policy/api/v1/search/query?query=" +
		url.QueryEscape("display_name:WebAlpha") +
		"&included_fields=" + url.QueryEscape("display_name,path")
	req := newHTTPAPITestRequest(t, http.MethodGet, searchURL, nil)
	resp := doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", resp.StatusCode)
	}

	decoded := decodeSearchHTTPResponse(t, resp)
	results, ok := decoded["results"].([]any)
	if !ok {
		t.Fatalf("results = %#v, want array", decoded["results"])
	}
	if len(results) != 1 {
		t.Fatalf("results count = %d, want 1", len(results))
	}
	result, ok := results[0].(map[string]any)
	if !ok {
		t.Fatalf("results[0] = %#v, want object", results[0])
	}
	if len(result) != 2 {
		t.Fatalf("projected field count = %d, want 2: %#v", len(result), result)
	}
	if result["display_name"] != "WebAlpha" {
		t.Fatalf("display_name = %#v, want WebAlpha", result["display_name"])
	}
	if result["path"] != policyWebAlphaPath {
		t.Fatalf("path = %#v, want policy group path", result["path"])
	}
}

func TestSearchQueryHidesTombstonesUnlessMarkedForDeleteIsQueried(t *testing.T) {
	t.Parallel()

	server := newSearchHTTPTestServer(t, httpAPITestConfig(t))
	createPolicySearchGroup(t, server, "deleted", map[string]any{"display_name": "Deleted Resource"})
	deletePolicySearchGroup(t, server, "deleted")

	searchURL := server.URL + "/policy/api/v1/search/query?query=" + url.QueryEscape("display_name:Deleted*")
	req := newHTTPAPITestRequest(t, http.MethodGet, searchURL, nil)
	resp := doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("normal search StatusCode = %d, want 200", resp.StatusCode)
	}
	decoded := decodeSearchHTTPResponse(t, resp)
	if decoded["result_count"] != float64(0) {
		t.Fatalf("normal result_count = %#v, want 0", decoded["result_count"])
	}

	searchURL = server.URL + "/policy/api/v1/search/query?query=" +
		url.QueryEscape("marked_for_delete:true AND resource_type:Group")
	req = newHTTPAPITestRequest(t, http.MethodGet, searchURL, nil)
	resp = doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tombstone search StatusCode = %d, want 200", resp.StatusCode)
	}
	decoded = decodeSearchHTTPResponse(t, resp)
	if decoded["result_count"] != float64(1) {
		t.Fatalf("tombstone result_count = %#v, want 1", decoded["result_count"])
	}
	results, ok := decoded["results"].([]any)
	if !ok {
		t.Fatalf("results = %#v, want array", decoded["results"])
	}
	if len(results) != 1 {
		t.Fatalf("results count = %d, want 1", len(results))
	}
	result, ok := results[0].(map[string]any)
	if !ok {
		t.Fatalf("results[0] = %#v, want object", results[0])
	}
	if result["marked_for_delete"] != true {
		t.Fatalf("marked_for_delete = %#v, want true", result["marked_for_delete"])
	}
}

func TestSearchQueryPaginatesWithTotalResultCountAndCursor(t *testing.T) {
	t.Parallel()

	server := newSearchHTTPTestServer(t, httpAPITestConfig(t))
	createPolicySearchGroup(t, server, "alpha", map[string]any{"display_name": "Alpha"})
	createPolicySearchGroup(t, server, "beta", map[string]any{"display_name": "Beta"})
	createPolicySearchGroup(t, server, "gamma", map[string]any{"display_name": "Gamma"})

	searchURL := server.URL + "/policy/api/v1/search/query?query=" +
		url.QueryEscape("resource_type:Group") + "&page_size=1"
	req := newHTTPAPITestRequest(t, http.MethodGet, searchURL, nil)
	resp := doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first page StatusCode = %d, want 200", resp.StatusCode)
	}
	decoded := decodeSearchHTTPResponse(t, resp)
	if decoded["result_count"] != float64(3) {
		t.Fatalf("first page result_count = %#v, want 3", decoded["result_count"])
	}
	if decoded["cursor"] != "1" {
		t.Fatalf("first page cursor = %#v, want 1", decoded["cursor"])
	}
	requireSearchResultDisplayNames(t, decoded, "Alpha")

	cursor, ok := decoded["cursor"].(string)
	if !ok {
		t.Fatalf("first page cursor = %#v, want string", decoded["cursor"])
	}
	searchURL += "&cursor=" + url.QueryEscape(cursor)
	req = newHTTPAPITestRequest(t, http.MethodGet, searchURL, nil)
	resp = doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second page StatusCode = %d, want 200", resp.StatusCode)
	}
	decoded = decodeSearchHTTPResponse(t, resp)
	requireSearchResponseNoResultCount(t, decoded)
	if decoded["cursor"] != "2" {
		t.Fatalf("second page cursor = %#v, want 2", decoded["cursor"])
	}
	requireSearchResultDisplayNames(t, decoded, "Beta")
}

func TestPolicySearchQueryPaginatesMoreThanOneThousandResources(t *testing.T) {
	t.Parallel()

	server := newSearchHTTPTestServer(t, httpAPITestConfig(t))
	const resourceCount = 1005
	for index := range resourceCount {
		groupID := fmt.Sprintf("bulk-%04d", index)
		createPolicySearchGroup(t, server, groupID, map[string]any{
			"display_name": fmt.Sprintf("Bulk-%04d", index),
			"description":  "bulk page boundary target",
		})
	}

	searchURL := server.URL + "/policy/api/v1/search/query?query=" +
		url.QueryEscape("resource_type:Group")
	req := newHTTPAPITestRequest(t, http.MethodGet, searchURL, nil)
	resp := doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first page StatusCode = %d, want 200", resp.StatusCode)
	}
	decoded := decodeSearchHTTPResponse(t, resp)
	requireDefaultSearchResponseMeta(t, decoded, resourceCount)
	requireSearchResponseCursor(t, decoded, "1000")
	requireSearchResultsCount(t, decoded, 1000)
	requireSearchResultDisplayNamesAt(t, decoded, map[int]string{
		0:   "Bulk-0000",
		999: "Bulk-0999",
	})

	cursor := requireSearchResponseCursor(t, decoded, "1000")
	secondPageURL := searchURL + "&cursor=" + url.QueryEscape(cursor)
	req = newHTTPAPITestRequest(t, http.MethodGet, secondPageURL, nil)
	resp = doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second page StatusCode = %d, want 200", resp.StatusCode)
	}
	decoded = decodeSearchHTTPResponse(t, resp)
	requireSearchResponseMetaWithoutResultCount(t, decoded)
	requireSearchResponseNoCursor(t, decoded)
	requireSearchResultsCount(t, decoded, 5)
	requireSearchResultDisplayNamesAt(t, decoded, map[int]string{
		0: "Bulk-1000",
		4: "Bulk-1004",
	})

	oversizedPageURL := searchURL + "&page_size=1001"
	req = newHTTPAPITestRequest(t, http.MethodGet, oversizedPageURL, nil)
	resp = doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("oversized page StatusCode = %d, want 200", resp.StatusCode)
	}
	decoded = decodeSearchHTTPResponse(t, resp)
	requireDefaultSearchResponseMeta(t, decoded, resourceCount)
	requireSearchResponseCursor(t, decoded, "1000")
	requireSearchResultsCount(t, decoded, 1000)
}

func TestSearchQuerySortsByDisplayNameDescending(t *testing.T) {
	t.Parallel()

	server := newSearchHTTPTestServer(t, httpAPITestConfig(t))
	createPolicySearchGroup(t, server, "alpha", map[string]any{"display_name": "Alpha"})
	createPolicySearchGroup(t, server, "beta", map[string]any{"display_name": "Beta"})
	createPolicySearchGroup(t, server, "gamma", map[string]any{"display_name": "Gamma"})

	searchURL := server.URL + "/policy/api/v1/search/query?query=" +
		url.QueryEscape("resource_type:Group") + "&sort_by=display_name&sort_ascending=false"
	req := newHTTPAPITestRequest(t, http.MethodGet, searchURL, nil)
	resp := doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", resp.StatusCode)
	}
	decoded := decodeSearchHTTPResponse(t, resp)
	if decoded["sort_by"] != searchSortByDisplayName {
		t.Fatalf("sort_by = %#v, want display_name", decoded["sort_by"])
	}
	if decoded["sort_ascending"] != false {
		t.Fatalf("sort_ascending = %#v, want false", decoded["sort_ascending"])
	}
	requireSearchResultDisplayNames(t, decoded, "Gamma", "Beta", "Alpha")
}

func TestPolicySearchQueryAddsDerivedStatus(t *testing.T) {
	t.Parallel()

	cfg := httpAPITestConfig(t)
	cfg.Realization.CreateDelayMS = 60000
	server := newSearchHTTPTestServer(t, cfg)
	createPolicySearchGroup(t, server, "web-alpha", map[string]any{"display_name": "WebAlpha"})

	searchURL := server.URL + "/policy/api/v1/search/query?query=" + url.QueryEscape("display_name:WebAlpha")
	req := newHTTPAPITestRequest(t, http.MethodGet, searchURL, nil)
	resp := doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", resp.StatusCode)
	}
	decoded := decodeSearchHTTPResponse(t, resp)
	result := requireSingleSearchResult(t, decoded)
	requirePolicySearchStatus(t, result, policyWebAlphaPath, "IN_PROGRESS")
}

func TestSearchQueryRejectsInvalidRequestParameters(t *testing.T) {
	t.Parallel()

	server := newSearchHTTPTestServer(t, httpAPITestConfig(t))

	for _, tc := range []struct {
		name  string
		query string
	}{
		{name: "missing query", query: ""},
		{name: "blank query", query: "?query=%20%20"},
		{name: "malformed page size", query: "?query=resource_type:Group&page_size=nope"},
		{name: "negative page size", query: "?query=resource_type:Group&page_size=-1"},
		{name: "malformed cursor", query: "?query=resource_type:Group&cursor=nope"},
		{name: "negative cursor", query: "?query=resource_type:Group&cursor=-1"},
		{name: "malformed sort ascending", query: "?query=resource_type:Group&sort_ascending=yes"},
		{name: "unknown sort field", query: "?query=resource_type:Group&sort_by=unsupported"},
		{name: "malformed search query", query: "?query=" + url.QueryEscape("resource_type:Group AND")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := newHTTPAPITestRequest(t, http.MethodGet, server.URL+"/policy/api/v1/search/query"+tc.query, nil)
			resp := doHTTPAPITestRequest(t, server, req)
			defer closeHTTPAPITestBody(t, resp)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("StatusCode = %d, want 400", resp.StatusCode)
			}
		})
	}
}

func TestSearchDSLRejectsMalformedPredicateThroughHTTP(t *testing.T) {
	t.Parallel()

	server := newSearchHTTPTestServer(t, httpAPITestConfig(t))

	searchURL := server.URL + "/policy/api/v1/search/dsl?query=" +
		url.QueryEscape("Group where display_name =")
	req := newHTTPAPITestRequest(t, http.MethodGet, searchURL, nil)
	resp := doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("StatusCode = %d, want 400", resp.StatusCode)
	}
}

func TestSearchQueryRejectsInjectionThroughHTTP(t *testing.T) {
	t.Parallel()

	server := newSearchHTTPTestServer(t, httpAPITestConfig(t))
	createPolicySearchGroup(t, server, "safe-target", map[string]any{"display_name": "SafeTarget"})
	createPolicySearchGroup(t, server, "other-target", map[string]any{"display_name": "OtherTarget"})

	injectionURL := server.URL + "/policy/api/v1/search/query?query=" +
		url.QueryEscape("display_name:SafeTarget OR 1=1")
	req := newHTTPAPITestRequest(t, http.MethodGet, injectionURL, nil)
	resp := doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("injection StatusCode = %d, want 400", resp.StatusCode)
	}

	normalURL := server.URL + "/policy/api/v1/search/query?query=" +
		url.QueryEscape("display_name:SafeTarget AND resource_type:Group")
	req = newHTTPAPITestRequest(t, http.MethodGet, normalURL, nil)
	resp = doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("normal search StatusCode = %d, want 200", resp.StatusCode)
	}
	decoded := decodeSearchHTTPResponse(t, resp)
	requireDefaultSearchResponseMeta(t, decoded, 1)
	requireSearchResultDisplayNames(t, decoded, "SafeTarget")
}

func newSearchHTTPTestServer(t *testing.T, cfg config.Config) *httptest.Server {
	t.Helper()

	db := openHTTPAPITestDB(t)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close() error = %v", err)
		}
	})
	handler, err := NewHandler(t.Context(), AppOptions{
		Config: cfg,
		DB:     db,
		Logger: zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func decodeSearchHTTPResponse(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()

	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	return decoded
}

func requireDefaultSearchResponseMeta(t *testing.T, decoded map[string]any, resultCount int) {
	t.Helper()

	if decoded["result_count"] != float64(resultCount) {
		t.Fatalf("result_count = %#v, want %d", decoded["result_count"], resultCount)
	}
	requireSearchResponseMetaWithoutResultCount(t, decoded)
}

func requireSearchResponseMetaWithoutResultCount(t *testing.T, decoded map[string]any) {
	t.Helper()

	if decoded["sort_by"] != searchSortByDisplayName {
		t.Fatalf("sort_by = %#v, want %s", decoded["sort_by"], searchSortByDisplayName)
	}
	if decoded["sort_ascending"] != true {
		t.Fatalf("sort_ascending = %#v, want true", decoded["sort_ascending"])
	}
	if _, ok := decoded["results"].([]any); !ok {
		t.Fatalf("results = %#v, want array", decoded["results"])
	}
	if cursor, ok := decoded["cursor"]; ok {
		if _, isString := cursor.(string); !isString {
			t.Fatalf("cursor = %#v, want string when present", cursor)
		}
	}
}

func requireDocumentedSearchResponseLinks(t *testing.T, decoded map[string]any, wantHref string) {
	t.Helper()

	if decoded["_schema"] != "SearchResponse" {
		t.Fatalf("_schema = %#v, want SearchResponse", decoded["_schema"])
	}
	self, ok := decoded["_self"].(map[string]any)
	if !ok {
		t.Fatalf("_self = %#v, want object", decoded["_self"])
	}
	if self["href"] != wantHref {
		t.Fatalf("_self.href = %#v, want %s", self["href"], wantHref)
	}
	if self["rel"] != "self" {
		t.Fatalf("_self.rel = %#v, want self", self["rel"])
	}
	links, ok := decoded["_links"].([]any)
	if !ok {
		t.Fatalf("_links = %#v, want array", decoded["_links"])
	}
	if len(links) != 1 {
		t.Fatalf("_links count = %d, want 1", len(links))
	}
	link, ok := links[0].(map[string]any)
	if !ok {
		t.Fatalf("_links[0] = %#v, want object", links[0])
	}
	if link["href"] != wantHref {
		t.Fatalf("_links[0].href = %#v, want %s", link["href"], wantHref)
	}
	if link["rel"] != "self" {
		t.Fatalf("_links[0].rel = %#v, want self", link["rel"])
	}
}

func requireSearchResponseCursor(t *testing.T, decoded map[string]any, want string) string {
	t.Helper()

	cursor, ok := decoded["cursor"].(string)
	if !ok {
		t.Fatalf("cursor = %#v, want string %q", decoded["cursor"], want)
	}
	if cursor != want {
		t.Fatalf("cursor = %#v, want %q", cursor, want)
	}
	return cursor
}

func requireSearchResponseNoCursor(t *testing.T, decoded map[string]any) {
	t.Helper()

	if cursor, ok := decoded["cursor"]; ok {
		t.Fatalf("cursor = %#v, want absent on last page", cursor)
	}
}

func requireSearchResponseNoResultCount(t *testing.T, decoded map[string]any) {
	t.Helper()

	if resultCount, ok := decoded["result_count"]; ok {
		t.Fatalf("result_count = %#v, want absent on cursor page", resultCount)
	}
}

func requireSearchResultsCount(t *testing.T, decoded map[string]any, want int) []any {
	t.Helper()

	results, ok := decoded["results"].([]any)
	if !ok {
		t.Fatalf("results = %#v, want array", decoded["results"])
	}
	if len(results) != want {
		t.Fatalf("results count = %d, want %d", len(results), want)
	}
	return results
}

func requireSearchResultDisplayNamesAt(t *testing.T, decoded map[string]any, want map[int]string) {
	t.Helper()

	results, ok := decoded["results"].([]any)
	if !ok {
		t.Fatalf("results = %#v, want array", decoded["results"])
	}
	for index, expected := range want {
		if index < 0 || index >= len(results) {
			t.Fatalf("display name assertion index %d outside results count %d", index, len(results))
		}
		result, isObject := results[index].(map[string]any)
		if !isObject {
			t.Fatalf("results[%d] = %#v, want object", index, results[index])
		}
		if result["display_name"] != expected {
			t.Fatalf("results[%d].display_name = %#v, want %q", index, result["display_name"], expected)
		}
	}
}

func requireSingleSearchResult(t *testing.T, decoded map[string]any) map[string]any {
	t.Helper()

	results, ok := decoded["results"].([]any)
	if !ok {
		t.Fatalf("results = %#v, want array", decoded["results"])
	}
	if len(results) != 1 {
		t.Fatalf("results count = %d, want 1", len(results))
	}
	result, ok := results[0].(map[string]any)
	if !ok {
		t.Fatalf("results[0] = %#v, want object", results[0])
	}
	return result
}

func requirePolicySearchStatus(t *testing.T, result map[string]any, intentPath string, consolidatedStatus string) {
	t.Helper()

	status, ok := result["status"].(map[string]any)
	if !ok {
		t.Fatalf("status = %#v, want object", result["status"])
	}
	if status["intent_path"] != intentPath {
		t.Fatalf("status.intent_path = %#v, want %s", status["intent_path"], intentPath)
	}
	consolidated, ok := status["consolidated_status"].(map[string]any)
	if !ok {
		t.Fatalf("status.consolidated_status = %#v, want object", status["consolidated_status"])
	}
	if consolidated["consolidated_status"] != consolidatedStatus {
		t.Fatalf("consolidated_status = %#v, want %s", consolidated["consolidated_status"], consolidatedStatus)
	}
	perEP, ok := status["consolidated_status_per_enforcement_point"].([]any)
	if !ok {
		t.Fatalf("per enforcement point status = %#v, want array", status["consolidated_status_per_enforcement_point"])
	}
	if len(perEP) != 1 {
		t.Fatalf("per enforcement point status count = %d, want 1", len(perEP))
	}
}

func requireSearchResultDisplayNames(t *testing.T, decoded map[string]any, want ...string) {
	t.Helper()

	results, ok := decoded["results"].([]any)
	if !ok {
		t.Fatalf("results = %#v, want array", decoded["results"])
	}
	if len(results) != len(want) {
		t.Fatalf("results count = %d, want %d", len(results), len(want))
	}
	for index, expected := range want {
		result, isObject := results[index].(map[string]any)
		if !isObject {
			t.Fatalf("results[%d] = %#v, want object", index, results[index])
		}
		if result["display_name"] != expected {
			t.Fatalf("results[%d].display_name = %#v, want %q", index, result["display_name"], expected)
		}
	}
}

func createPolicySearchGroup(t *testing.T, server *httptest.Server, groupID string, payload map[string]any) {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	req := newHTTPAPITestRequest(
		t,
		http.MethodPatch,
		server.URL+"/policy/api/v1/infra/domains/default/groups/"+groupID,
		bytes.NewReader(body),
	)
	resp := doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create group %q StatusCode = %d, want 200", groupID, resp.StatusCode)
	}
}

func deletePolicySearchGroup(t *testing.T, server *httptest.Server, groupID string) {
	t.Helper()

	req := newHTTPAPITestRequest(
		t,
		http.MethodDelete,
		server.URL+"/policy/api/v1/infra/domains/default/groups/"+groupID,
		nil,
	)
	resp := doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete group %q StatusCode = %d, want 200", groupID, resp.StatusCode)
	}
}

func createManagerSearchIPSet(t *testing.T, server *httptest.Server, payload map[string]any) {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	req := newHTTPAPITestRequest(t, http.MethodPost, server.URL+"/api/v1/ip-sets", bytes.NewReader(body))
	resp := doHTTPAPITestRequest(t, server, req)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create ip set StatusCode = %d, want 201", resp.StatusCode)
	}
}
