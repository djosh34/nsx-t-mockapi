package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestManagerFirewallSectionsListRequiresAuthAndReturnsEmptyList(t *testing.T) {
	t.Parallel()

	server := newHTTPAPITestServer(t, nil)
	sectionsURL := server.URL + "/api/v1/firewall/sections"

	resp := doHTTPAPITestRequest(t, server, newHTTPAPITestRequestWithoutAuth(t, http.MethodGet, sectionsURL, nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated StatusCode = %d, want 401", resp.StatusCode)
	}

	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, sectionsURL, nil))
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

func TestManagerFirewallSectionCreateReadAndListThroughHTTP(t *testing.T) {
	t.Parallel()

	server := newHTTPAPITestServer(t, nil)
	sectionsURL := server.URL + "/api/v1/firewall/sections"
	sectionURL := sectionsURL + "/web-section"

	resp := doJSONHTTPAPITestRequest(t, server, http.MethodPost, sectionsURL, `{
		"id":"web-section",
		"display_name":"Web Section",
		"resource_type":"FirewallSection",
		"section_type":"LAYER3",
		"stateful":true
	}`)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST section StatusCode = %d, want 201", resp.StatusCode)
	}
	created := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestString(t, created, "id", "web-section")
	requireHTTPAPITestString(t, created, "display_name", "Web Section")
	requireHTTPAPITestString(t, created, "path", "/api/v1/firewall/sections/web-section")
	requireHTTPAPITestString(t, created, "parent_path", "/api/v1/firewall/sections")
	requireHTTPAPITestString(t, created, "relative_path", "web-section")
	requireHTTPAPITestString(t, created, "resource_type", "FirewallSection")
	requireHTTPAPITestNumber(t, created, "rule_count", 0)
	requireHTTPAPITestRevision(t, created, 0)

	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, sectionURL, nil))
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET section StatusCode = %d, want 200", resp.StatusCode)
	}
	read := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestString(t, read, "display_name", "Web Section")
	requireHTTPAPITestNumber(t, read, "rule_count", 0)

	list := getHTTPAPITestList(t, server, sectionsURL)
	if list.ResultCount != 1 {
		t.Fatalf("section list ResultCount = %d, want 1", list.ResultCount)
	}
	requireHTTPAPITestResultNames(t, list.Results, "Web Section")
}

func TestManagerFirewallSectionInvalidPayloadsReturnBadRequest(t *testing.T) {
	t.Parallel()

	server := newHTTPAPITestServer(t, nil)
	sectionsURL := server.URL + "/api/v1/firewall/sections"

	for name, body := range map[string]string{
		"malformed":        `{"display_name":`,
		"missing section":  `{"display_name":"Missing Section","stateful":true}`,
		"missing stateful": `{"display_name":"Missing Stateful","section_type":"LAYER3"}`,
		"stateful string":  `{"display_name":"Bad Stateful","section_type":"LAYER3","stateful":"true"}`,
		"bad section":      `{"display_name":"Bad Section","section_type":"BAD","stateful":true}`,
		"wrong resource":   `{"resource_type":"FirewallRule","section_type":"LAYER3","stateful":true}`,
		"long display":     `{"display_name":"` + strings.Repeat("x", 256) + `","section_type":"LAYER3","stateful":true}`,
		"long description": `{"description":"` + strings.Repeat("x", 1025) + `","section_type":"LAYER3","stateful":true}`,
		"too many tags":    `{"tags":[` + strings.Repeat(`{},`, 30) + `{}],"section_type":"LAYER3","stateful":true}`,
		"too many applied": `{"applied_tos":[` +
			strings.Repeat(`{},`, 128) + `{}],"section_type":"LAYER3","stateful":true}`,
		"non object payload": `[]`,
	} {
		resp := doJSONHTTPAPITestRequest(t, server, http.MethodPost, sectionsURL, body)
		closeHTTPAPITestBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s StatusCode = %d, want 400", name, resp.StatusCode)
		}
	}
}

func TestManagerFirewallSectionUpdateReviseAndDeleteThroughHTTP(t *testing.T) {
	t.Parallel()

	server := newHTTPAPITestServer(t, nil)
	sectionsURL := server.URL + "/api/v1/firewall/sections"
	sectionURL := sectionsURL + "/update-section"

	resp := doJSONHTTPAPITestRequest(t, server, http.MethodPost, sectionsURL, `{
		"id":"update-section",
		"display_name":"Update Section",
		"section_type":"LAYER3",
		"stateful":true
	}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST section StatusCode = %d, want 201", resp.StatusCode)
	}

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPut, sectionURL, `{
		"display_name":"Missing Revision",
		"section_type":"LAYER3",
		"stateful":true
	}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("PUT missing revision StatusCode = %d, want 409", resp.StatusCode)
	}

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPut, sectionURL, `{
		"display_name":"Updated Section",
		"section_type":"LAYER3",
		"stateful":false,
		"_revision":0
	}`)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT section StatusCode = %d, want 200", resp.StatusCode)
	}
	updated := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestString(t, updated, "display_name", "Updated Section")
	requireHTTPAPITestRevision(t, updated, 1)

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPut, sectionURL, `{
		"display_name":"Stale",
		"section_type":"LAYER3",
		"stateful":false,
		"_revision":0
	}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("PUT stale revision StatusCode = %d, want 409", resp.StatusCode)
	}

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, sectionURL+"?action=revise", `{
		"display_name":"Revised Section",
		"section_type":"LAYER3",
		"stateful":false,
		"_revision":1,
		"priority":10
	}`)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revise section StatusCode = %d, want 200", resp.StatusCode)
	}
	revised := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestString(t, revised, "display_name", "Revised Section")
	requireHTTPAPITestNumber(t, revised, "priority", 10)
	requireHTTPAPITestRevision(t, revised, 2)

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, sectionURL+"?action=bad", `{}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bad action StatusCode = %d, want 404", resp.StatusCode)
	}

	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodDelete, sectionURL, nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE section StatusCode = %d, want 200", resp.StatusCode)
	}

	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, sectionURL, nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET deleted section StatusCode = %d, want 404", resp.StatusCode)
	}

	list := getHTTPAPITestList(t, server, sectionsURL)
	if list.ResultCount != 0 {
		t.Fatalf("section list ResultCount after delete = %d, want 0", list.ResultCount)
	}
}

//nolint:cyclop // One Manager rules scenario keeps parent consistency, revision, validation, and rule_count connected.
func TestManagerFirewallRulesCRUDValidationAndCascadeThroughHTTP(t *testing.T) {
	t.Parallel()

	server := newHTTPAPITestServer(t, nil)
	sectionsURL := server.URL + "/api/v1/firewall/sections"
	sectionURL := sectionsURL + "/rules-section"
	rulesURL := sectionURL + "/rules"
	ruleURL := rulesURL + "/allow-web"

	resp := doJSONHTTPAPITestRequest(t, server, http.MethodPost, sectionsURL, `{
		"id":"rules-section",
		"display_name":"Rules Section",
		"section_type":"LAYER3",
		"stateful":true
	}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST section StatusCode = %d, want 201", resp.StatusCode)
	}

	list := getHTTPAPITestList(t, server, rulesURL)
	if list.ResultCount != 0 {
		t.Fatalf("initial rules ResultCount = %d, want 0", list.ResultCount)
	}

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, server.URL+"/api/v1/firewall/sections/missing/rules", `{
		"display_name":"Missing Parent",
		"action":"ALLOW"
	}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("POST rule missing parent StatusCode = %d, want 404", resp.StatusCode)
	}

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, rulesURL, `{"display_name":"Missing Action"}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST rule missing action StatusCode = %d, want 400", resp.StatusCode)
	}

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, rulesURL, `{
		"id":"allow-web",
		"display_name":"Allow Web",
		"resource_type":"FirewallRule",
		"action":"ALLOW",
		"direction":"IN_OUT",
		"ip_protocol":"IPV4_IPV6",
		"sequence_number":5
	}`)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST rule StatusCode = %d, want 200", resp.StatusCode)
	}
	created := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestString(t, created, "id", "allow-web")
	requireHTTPAPITestString(t, created, "path", "/api/v1/firewall/sections/rules-section/rules/allow-web")
	requireHTTPAPITestString(t, created, "parent_path", "/api/v1/firewall/sections/rules-section")
	requireHTTPAPITestString(t, created, "resource_type", "FirewallRule")
	requireHTTPAPITestRevision(t, created, 0)

	section := getHTTPAPITestObject(t, server, sectionURL)
	requireHTTPAPITestNumber(t, section, "rule_count", 1)

	list = getHTTPAPITestList(t, server, rulesURL)
	if list.ResultCount != 1 {
		t.Fatalf("rule list ResultCount = %d, want 1", list.ResultCount)
	}
	requireHTTPAPITestResultNames(t, list.Results, "Allow Web")

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPut, ruleURL, `{
		"display_name":"Stale",
		"action":"DROP",
		"_revision":7
	}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("PUT stale rule StatusCode = %d, want 409", resp.StatusCode)
	}

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPut, ruleURL, `{
		"display_name":"Drop Web",
		"action":"DROP",
		"_revision":0
	}`)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT rule StatusCode = %d, want 200", resp.StatusCode)
	}
	updated := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestString(t, updated, "action", "DROP")
	requireHTTPAPITestRevision(t, updated, 1)

	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodDelete, sectionURL, nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("DELETE section with child rules StatusCode = %d, want 409", resp.StatusCode)
	}

	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodDelete, ruleURL, nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE rule StatusCode = %d, want 200", resp.StatusCode)
	}
	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, ruleURL, nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET deleted rule StatusCode = %d, want 404", resp.StatusCode)
	}
	section = getHTTPAPITestObject(t, server, sectionURL)
	requireHTTPAPITestNumber(t, section, "rule_count", 0)
}

func TestManagerFirewallActionRoutesPreserveSectionRuleConsistency(t *testing.T) {
	t.Parallel()

	server := newHTTPAPITestServer(t, nil)
	sectionsURL := server.URL + "/api/v1/firewall/sections"
	sectionURL := sectionsURL + "/bundle-section"
	rulesURL := sectionURL + "/rules"

	resp := doJSONHTTPAPITestRequest(t, server, http.MethodPost, sectionsURL+"?action=create_with_rules", `{
		"id":"bundle-section",
		"display_name":"Bundle Section",
		"section_type":"LAYER3",
		"stateful":true,
		"rules":[
			{"id":"allow-a","display_name":"Allow A","action":"ALLOW","sequence_number":20},
			{"id":"drop-b","display_name":"Drop B","action":"DROP","sequence_number":10}
		]
	}`)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create_with_rules StatusCode = %d, want 201", resp.StatusCode)
	}
	created := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestEmbeddedRuleNames(t, created, "Drop B", "Allow A")
	requireHTTPAPITestNumber(t, created, "rule_count", 2)

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, sectionURL+"?action=list_with_rules", `{}`)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list_with_rules StatusCode = %d, want 200", resp.StatusCode)
	}
	withRules := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestEmbeddedRuleNames(t, withRules, "Drop B", "Allow A")

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, rulesURL+"?action=create_multiple", `{
		"rules":[
			{"id":"detect-c","display_name":"Detect C","action":"DETECT"}
		]
	}`)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create_multiple StatusCode = %d, want 200", resp.StatusCode)
	}
	createdRules := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestEmbeddedRuleNames(t, createdRules, "Detect C")

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, sectionURL+"?action=update_with_rules", `{
		"display_name":"Bundle Updated",
		"section_type":"LAYER3",
		"stateful":false,
		"_revision":0,
		"rules":[
			{"id":"allow-final","display_name":"Allow Final","action":"ALLOW"}
		]
	}`)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update_with_rules StatusCode = %d, want 200", resp.StatusCode)
	}
	updated := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestString(t, updated, "display_name", "Bundle Updated")
	requireHTTPAPITestEmbeddedRuleNames(t, updated, "Allow Final")
	requireHTTPAPITestNumber(t, updated, "rule_count", 1)

	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, rulesURL+"/drop-b", nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET replaced rule StatusCode = %d, want 404", resp.StatusCode)
	}

	stats := getHTTPAPITestList(t, server, rulesURL+"/stats")
	if stats.ResultCount != 1 {
		t.Fatalf("stats ResultCount = %d, want 1", stats.ResultCount)
	}

	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodDelete, sectionURL+"?cascade=true", nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE cascade section StatusCode = %d, want 200", resp.StatusCode)
	}
	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, rulesURL+"/allow-final", nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET cascade deleted rule StatusCode = %d, want 404", resp.StatusCode)
	}
}

//nolint:cyclop // One IPSet scenario keeps CRUD, revision, member actions, validation, and hard delete connected.
func TestManagerIPSetCRUDMembersValidationAndDeleteThroughHTTP(t *testing.T) {
	t.Parallel()

	server := newHTTPAPITestServer(t, nil)
	ipSetsURL := server.URL + "/api/v1/ip-sets"
	ipSetURL := ipSetsURL + "/web-ips"

	list := getHTTPAPITestList(t, server, ipSetsURL)
	if list.ResultCount != 0 {
		t.Fatalf("initial IPSet ResultCount = %d, want 0", list.ResultCount)
	}

	resp := doJSONHTTPAPITestRequest(t, server, http.MethodPost, ipSetsURL, `{
		"id":"web-ips",
		"display_name":"Web IPs",
		"resource_type":"IPSet",
		"ip_addresses":["10.0.0.1","10.0.0.10-10.0.0.20","10.0.1.0/24"]
	}`)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST IPSet StatusCode = %d, want 201", resp.StatusCode)
	}
	created := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestString(t, created, "id", "web-ips")
	requireHTTPAPITestString(t, created, "path", "/api/v1/ip-sets/web-ips")
	requireHTTPAPITestString(t, created, "resource_type", "IPSet")
	requireHTTPAPITestRevision(t, created, 0)

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, ipSetsURL, `{
		"id":"bad-ips",
		"display_name":"Bad IPs",
		"ip_addresses":["10.0.0.1","fd00::1"]
	}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST mixed-family IPSet StatusCode = %d, want 400", resp.StatusCode)
	}

	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, ipSetURL, nil))
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET IPSet StatusCode = %d, want 200", resp.StatusCode)
	}
	read := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestString(t, read, "display_name", "Web IPs")

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPut, ipSetURL, `{
		"display_name":"Stale IPs",
		"ip_addresses":["10.0.0.2"],
		"_revision":7
	}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("PUT stale IPSet StatusCode = %d, want 409", resp.StatusCode)
	}

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPut, ipSetURL, `{
		"display_name":"Updated IPs",
		"ip_addresses":["10.0.0.2"],
		"_revision":0
	}`)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT IPSet StatusCode = %d, want 200", resp.StatusCode)
	}
	updated := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestString(t, updated, "display_name", "Updated IPs")
	requireHTTPAPITestRevision(t, updated, 1)

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, ipSetURL+"?action=add_ip", `{"ip_address":"10.0.0.3"}`)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add_ip StatusCode = %d, want 201", resp.StatusCode)
	}
	added := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestString(t, added, "ip_address", "10.0.0.3")

	members := getHTTPAPITestList(t, server, ipSetURL+"/members")
	requireHTTPAPITestIPElementResults(t, members.Results, "10.0.0.2", "10.0.0.3")

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, ipSetURL+"?action=remove_ip", `{"ip_address":"10.0.0.2"}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("remove_ip StatusCode = %d, want 201", resp.StatusCode)
	}
	members = getHTTPAPITestList(t, server, ipSetURL+"/members")
	requireHTTPAPITestIPElementResults(t, members.Results, "10.0.0.3")

	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodDelete, ipSetURL, nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE IPSet StatusCode = %d, want 200", resp.StatusCode)
	}
	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, ipSetURL, nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET deleted IPSet StatusCode = %d, want 404", resp.StatusCode)
	}
}

//nolint:cyclop // One edge scenario shares generated ids, revision actions, and validation state.
func TestManagerAdditionalEdgeBehaviorsThroughHTTP(t *testing.T) {
	t.Parallel()

	server := newHTTPAPITestServer(t, nil)
	sectionsURL := server.URL + "/api/v1/firewall/sections"

	resp := doJSONHTTPAPITestRequest(t, server, http.MethodPost, sectionsURL, `{
		"display_name":"Generated Section",
		"section_type":"LAYER3",
		"stateful":true
	}`)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST generated section StatusCode = %d, want 201", resp.StatusCode)
	}
	generatedSection := decodeHTTPAPITestObject(t, resp)
	generatedSectionID, ok := generatedSection["id"].(string)
	if !ok || generatedSectionID == "" {
		t.Fatalf("generated section id = %#v, want non-empty string", generatedSection["id"])
	}

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, sectionsURL, `{
		"id":7,
		"display_name":"Bad ID",
		"section_type":"LAYER3",
		"stateful":true
	}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST numeric section id StatusCode = %d, want 400", resp.StatusCode)
	}

	withRulesURL := sectionsURL + "?action=create_with_rules"
	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, withRulesURL, `{
		"display_name":"Generated Bundle",
		"section_type":"LAYER3",
		"stateful":true,
		"rules":[{"display_name":"Generated Rule","action":"ALLOW"}]
	}`)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create_with_rules generated StatusCode = %d, want 201", resp.StatusCode)
	}
	generatedBundle := decodeHTTPAPITestObject(t, resp)
	generatedBundleID, ok := generatedBundle["id"].(string)
	if !ok || generatedBundleID == "" {
		t.Fatalf("generated bundle id = %#v, want non-empty string", generatedBundle["id"])
	}
	requireHTTPAPITestEmbeddedRuleNames(t, generatedBundle, "Generated Rule")

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, withRulesURL, `{
		"display_name":"Bad Bundle",
		"section_type":"LAYER3",
		"stateful":true
	}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("create_with_rules missing rules StatusCode = %d, want 400", resp.StatusCode)
	}

	reviseSectionURL := sectionsURL + "/" + generatedBundleID + "?action=revise_with_rules"
	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, reviseSectionURL, `{
		"display_name":"Revised Bundle",
		"section_type":"LAYER3",
		"stateful":true,
		"_revision":0,
		"rules":[{"id":"revised-rule","display_name":"Revised Rule","action":"DROP"}]
	}`)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revise_with_rules StatusCode = %d, want 200", resp.StatusCode)
	}
	revisedBundle := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestEmbeddedRuleNames(t, revisedBundle, "Revised Rule")

	ruleURL := sectionsURL + "/" + generatedBundleID + "/rules/revised-rule"
	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, ruleURL+"?action=revise", `{
		"display_name":"Revised Rule Again",
		"action":"ALLOW",
		"_revision":0,
		"priority":3
	}`)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rule revise StatusCode = %d, want 200", resp.StatusCode)
	}
	revisedRule := decodeHTTPAPITestObject(t, resp)
	requireHTTPAPITestNumber(t, revisedRule, "priority", 3)

	for name, body := range map[string]string{
		"wrong resource":   `{"resource_type":"FirewallSection","action":"ALLOW"}`,
		"bad action":       `{"action":"PASS"}`,
		"bad direction":    `{"action":"ALLOW","direction":"SIDEWAYS"}`,
		"bad protocol":     `{"action":"ALLOW","ip_protocol":"IPV10"}`,
		"long notes":       `{"action":"ALLOW","notes":"` + strings.Repeat("x", 2049) + `"}`,
		"long rule tag":    `{"action":"ALLOW","rule_tag":"` + strings.Repeat("x", 33) + `"}`,
		"too many sources": `{"action":"ALLOW","sources":[` + quotedCSV("s", 129) + `]}`,
		"non-array rules":  `{"rules":"bad"}`,
	} {
		targetURL := sectionsURL + "/" + generatedBundleID + "/rules"
		if name == "non-array rules" {
			targetURL += "?action=create_multiple"
		}
		resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, targetURL, body)
		closeHTTPAPITestBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s StatusCode = %d, want 400", name, resp.StatusCode)
		}
	}

	ipSetsURL := server.URL + "/api/v1/ip-sets"
	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, ipSetsURL, `{"display_name":"Generated IPSet"}`)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST generated IPSet StatusCode = %d, want 201", resp.StatusCode)
	}
	generatedIPSet := decodeHTTPAPITestObject(t, resp)
	generatedIPSetID, ok := generatedIPSet["id"].(string)
	if !ok || generatedIPSetID == "" {
		t.Fatalf("generated IPSet id = %#v, want non-empty string", generatedIPSet["id"])
	}
	generatedIPSetURL := ipSetsURL + "/" + generatedIPSetID

	members := getHTTPAPITestList(t, server, generatedIPSetURL+"/members")
	if members.ResultCount != 0 {
		t.Fatalf("empty generated IPSet members ResultCount = %d, want 0", members.ResultCount)
	}

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, generatedIPSetURL+"?action=add_ip", `{}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("add_ip missing ip_address StatusCode = %d, want 400", resp.StatusCode)
	}
	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, generatedIPSetURL+"?action=add_ip", `{"ip_address":7}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("add_ip numeric ip_address StatusCode = %d, want 400", resp.StatusCode)
	}
	resp = doJSONHTTPAPITestRequest(
		t,
		server,
		http.MethodPost,
		generatedIPSetURL+"?action=add_ip",
		`{"ip_address":"not-an-ip"}`,
	)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("add_ip bad ip_address StatusCode = %d, want 400", resp.StatusCode)
	}
	resp = doJSONHTTPAPITestRequest(
		t,
		server,
		http.MethodPost,
		generatedIPSetURL+"?action=add_ip",
		`{"ip_address":"10.0.0.1"}`,
	)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add_ip IPv4 StatusCode = %d, want 201", resp.StatusCode)
	}
	resp = doJSONHTTPAPITestRequest(
		t,
		server,
		http.MethodPost,
		generatedIPSetURL+"?action=add_ip",
		`{"ip_address":"fd00::1"}`,
	)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("add_ip mixed family StatusCode = %d, want 400", resp.StatusCode)
	}
}

//nolint:cyclop // Representative missing-resource checks are clearer as one HTTP routing scenario.
func TestManagerMissingResourcesAndRouteFailuresThroughHTTP(t *testing.T) {
	t.Parallel()

	server := newHTTPAPITestServer(t, nil)
	sectionsURL := server.URL + "/api/v1/firewall/sections"
	missingSectionURL := sectionsURL + "/missing"

	resp := doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, missingSectionURL, nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET missing section StatusCode = %d, want 404", resp.StatusCode)
	}
	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPut, missingSectionURL, `{
		"display_name":"Missing",
		"section_type":"LAYER3",
		"stateful":true,
		"_revision":0
	}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("PUT missing section StatusCode = %d, want 404", resp.StatusCode)
	}
	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodDelete, missingSectionURL, nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("DELETE missing section StatusCode = %d, want 404", resp.StatusCode)
	}
	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, missingSectionURL+"/rules", nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET missing section rules StatusCode = %d, want 404", resp.StatusCode)
	}
	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, missingSectionURL+"/rules/stats", nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET missing section stats StatusCode = %d, want 404", resp.StatusCode)
	}

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, sectionsURL, `{
		"id":"route-section",
		"display_name":"Route Section",
		"section_type":"LAYER3",
		"stateful":true
	}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST route section StatusCode = %d, want 201", resp.StatusCode)
	}
	routeRulesURL := sectionsURL + "/route-section/rules"

	resp = doJSONHTTPAPITestRequest(
		t,
		server,
		http.MethodPost,
		routeRulesURL,
		`{"display_name":"Generated Rule","action":"ALLOW"}`,
	)
	defer closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST generated rule StatusCode = %d, want 200", resp.StatusCode)
	}
	generatedRule := decodeHTTPAPITestObject(t, resp)
	if id, ok := generatedRule["id"].(string); !ok || id == "" {
		t.Fatalf("generated rule id = %#v, want non-empty string", generatedRule["id"])
	}

	missingRuleURL := routeRulesURL + "/missing-rule"
	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, missingRuleURL, nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET missing rule StatusCode = %d, want 404", resp.StatusCode)
	}
	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPut, missingRuleURL, `{
		"display_name":"Missing",
		"action":"ALLOW",
		"_revision":0
	}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("PUT missing rule StatusCode = %d, want 404", resp.StatusCode)
	}
	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodDelete, missingRuleURL, nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("DELETE missing rule StatusCode = %d, want 404", resp.StatusCode)
	}
	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, routeRulesURL+"?action=create_multiple", `{"rules":[7]}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("create_multiple non-object rule StatusCode = %d, want 400", resp.StatusCode)
	}
	resp = doJSONHTTPAPITestRequest(
		t,
		server,
		http.MethodPost,
		server.URL+"/api/v1/firewall/sections/nope/rules?action=create_multiple",
		`{"rules":[]}`,
	)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("create_multiple missing section StatusCode = %d, want 404", resp.StatusCode)
	}

	ipSetsURL := server.URL + "/api/v1/ip-sets"
	missingIPSetURL := ipSetsURL + "/missing"
	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, missingIPSetURL, nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET missing IPSet StatusCode = %d, want 404", resp.StatusCode)
	}
	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPut, missingIPSetURL, `{"display_name":"Missing","_revision":0}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("PUT missing IPSet StatusCode = %d, want 404", resp.StatusCode)
	}
	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodDelete, missingIPSetURL, nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("DELETE missing IPSet StatusCode = %d, want 404", resp.StatusCode)
	}
	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, missingIPSetURL+"/members", nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET missing IPSet members StatusCode = %d, want 404", resp.StatusCode)
	}
	resp = doJSONHTTPAPITestRequest(
		t,
		server,
		http.MethodPost,
		missingIPSetURL+"?action=add_ip",
		`{"ip_address":"10.0.0.1"}`,
	)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("add_ip missing IPSet StatusCode = %d, want 404", resp.StatusCode)
	}
	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, missingIPSetURL+"?action=bad", `{}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bad IPSet action StatusCode = %d, want 404", resp.StatusCode)
	}
}

//nolint:cyclop // Bad request route coverage is clearer as one table-like HTTP scenario.
func TestManagerBadRequestCoverageThroughHTTP(t *testing.T) {
	t.Parallel()

	server := newHTTPAPITestServer(t, nil)
	sectionsURL := server.URL + "/api/v1/firewall/sections"
	sectionURL := sectionsURL + "/bad-request-section"

	resp := doJSONHTTPAPITestRequest(t, server, http.MethodPost, sectionsURL, "")
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST empty section body StatusCode = %d, want 400", resp.StatusCode)
	}

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, sectionsURL, `{
		"id":"bad-request-section",
		"display_name":"Bad Request Section",
		"section_type":"LAYER3",
		"stateful":true
	}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST bad-request section StatusCode = %d, want 201", resp.StatusCode)
	}

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPut, sectionURL, `{"display_name":`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("PUT malformed section StatusCode = %d, want 400", resp.StatusCode)
	}

	rulesURL := sectionURL + "/rules"
	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, rulesURL, `{"id":7,"action":"ALLOW"}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST numeric rule id StatusCode = %d, want 400", resp.StatusCode)
	}

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, rulesURL, `{
		"id":"bad-request-rule",
		"display_name":"Bad Request Rule",
		"action":"ALLOW"
	}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST bad-request rule StatusCode = %d, want 200", resp.StatusCode)
	}
	ruleURL := rulesURL + "/bad-request-rule"
	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPut, ruleURL, `{"display_name":`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("PUT malformed rule StatusCode = %d, want 400", resp.StatusCode)
	}

	tooManyRules := `{"rules":[` + strings.Repeat(`{"action":"ALLOW"},`, 1000) + `{"action":"ALLOW"}]}`
	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, rulesURL+"?action=create_multiple", tooManyRules)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("create_multiple too many rules StatusCode = %d, want 400", resp.StatusCode)
	}

	ipSetsURL := server.URL + "/api/v1/ip-sets"
	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequestWithoutAuth(t, http.MethodGet, ipSetsURL, nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated IPSet list StatusCode = %d, want 401", resp.StatusCode)
	}

	for name, body := range map[string]string{
		"numeric id":      `{"id":7}`,
		"wrong resource":  `{"resource_type":"FirewallSection"}`,
		"too many tags":   `{"tags":[` + strings.Repeat(`{},`, 30) + `{}]}`,
		"invalid element": `{"ip_addresses":["not-an-ip"]}`,
	} {
		resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, ipSetsURL, body)
		closeHTTPAPITestBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s IPSet StatusCode = %d, want 400", name, resp.StatusCode)
		}
	}
}

func TestManagerConflictAndIdempotentMemberBehaviorThroughHTTP(t *testing.T) {
	t.Parallel()

	server := newHTTPAPITestServer(t, nil)
	sectionsURL := server.URL + "/api/v1/firewall/sections"
	sectionURL := sectionsURL + "/conflict-section"

	resp := doJSONHTTPAPITestRequest(t, server, http.MethodPost, sectionsURL+"?action=create_with_rules", `{
		"id":"conflict-section",
		"display_name":"Conflict Section",
		"section_type":"LAYER3",
		"stateful":true,
		"rules":[{"id":"keep-rule","display_name":"Keep Rule","action":"ALLOW"}]
	}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create_with_rules conflict section StatusCode = %d, want 201", resp.StatusCode)
	}

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, sectionURL+"?action=update_with_rules", `{
		"display_name":"Stale Bundle",
		"section_type":"LAYER3",
		"stateful":true,
		"_revision":7,
		"rules":[{"id":"bad-rule","display_name":"Bad Rule","action":"DROP"}]
	}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("stale update_with_rules StatusCode = %d, want 409", resp.StatusCode)
	}

	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, sectionURL+"/rules/keep-rule", nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET kept rule StatusCode = %d, want 200", resp.StatusCode)
	}
	resp = doHTTPAPITestRequest(t, server, newHTTPAPITestRequest(t, http.MethodGet, sectionURL+"/rules/bad-rule", nil))
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET stale-created rule StatusCode = %d, want 404", resp.StatusCode)
	}

	ipSetsURL := server.URL + "/api/v1/ip-sets"
	ipSetURL := ipSetsURL + "/idempotent-ips"
	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, ipSetsURL, `{
		"id":"idempotent-ips",
		"display_name":"Idempotent IPs"
	}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST idempotent IPSet StatusCode = %d, want 201", resp.StatusCode)
	}

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPut, ipSetURL, `{
		"resource_type":"FirewallSection",
		"_revision":0
	}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("PUT wrong IPSet resource_type StatusCode = %d, want 400", resp.StatusCode)
	}

	for index := range 2 {
		resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, ipSetURL+"?action=add_ip", `{"ip_address":"10.20.0.1"}`)
		closeHTTPAPITestBody(t, resp)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("add_ip duplicate %d StatusCode = %d, want 201", index, resp.StatusCode)
		}
	}
	members := getHTTPAPITestList(t, server, ipSetURL+"/members")
	requireHTTPAPITestIPElementResults(t, members.Results, "10.20.0.1")

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, ipSetURL+"?action=remove_ip", `{"ip_address":"10.20.0.2"}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("remove_ip absent StatusCode = %d, want 201", resp.StatusCode)
	}
	members = getHTTPAPITestList(t, server, ipSetURL+"/members")
	requireHTTPAPITestIPElementResults(t, members.Results, "10.20.0.1")
}

func requireHTTPAPITestIPElementResults(t *testing.T, results []json.RawMessage, want ...string) {
	t.Helper()

	if len(results) != len(want) {
		t.Fatalf("ip element results count = %d, want %d", len(results), len(want))
	}
	for index, wantValue := range want {
		var got map[string]any
		if err := json.Unmarshal(results[index], &got); err != nil {
			t.Fatalf("Unmarshal() IP element result %d error = %v", index, err)
		}
		requireHTTPAPITestString(t, got, "ip_address", wantValue)
	}
}
