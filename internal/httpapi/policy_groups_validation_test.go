package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nsx-t-mockapi/internal/config"

	"go.uber.org/zap"
)

var errPolicyGroupValidationTestBoom = errors.New("policy group validation test boom")

func TestPolicyGroupExpressionValidationBranchesThroughHTTP(t *testing.T) {
	t.Parallel()

	server := newHTTPAPITestServer(t, nil)
	groupURL := server.URL + "/policy/api/v1/infra/domains/default/groups/web"
	resp := doJSONHTTPAPITestRequest(t, server, http.MethodPut, groupURL, `{"display_name":"Web"}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT group StatusCode = %d, want 200", resp.StatusCode)
	}

	ipExpressionURL := groupURL + "/ip-address-expressions/ip1"
	for name, body := range map[string]string{
		"missing ip_addresses": `{"resource_type":"IPAddressExpression"}`,
		"empty ip_addresses":   `{"resource_type":"IPAddressExpression","ip_addresses":[]}`,
		"bad ip type":          `{"resource_type":"IPAddressExpression","ip_addresses":[7]}`,
		"bad ip element":       `{"resource_type":"IPAddressExpression","ip_addresses":["not-an-ip"]}`,
	} {
		resp = doJSONHTTPAPITestRequest(t, server, http.MethodPatch, ipExpressionURL, body)
		closeHTTPAPITestBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s StatusCode = %d, want 400", name, resp.StatusCode)
		}
	}

	resp = doJSONHTTPAPITestRequest(
		t,
		server,
		http.MethodPatch,
		ipExpressionURL,
		`{"resource_type":"IPAddressExpression","ip_addresses":["10.0.0.1-10.0.0.10","10.0.0.0/24"]}`,
	)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH IP range/CIDR StatusCode = %d, want 200", resp.StatusCode)
	}

	pathExpressionURL := groupURL + "/path-expressions/paths1"
	for name, body := range map[string]string{
		"missing paths": `{"resource_type":"PathExpression"}`,
		"empty paths":   `{"resource_type":"PathExpression","paths":[]}`,
		"bad path type": `{"resource_type":"PathExpression","paths":[7]}`,
		"bad path":      `{"resource_type":"PathExpression","paths":["/unsupported/path"]}`,
	} {
		resp = doJSONHTTPAPITestRequest(t, server, http.MethodPatch, pathExpressionURL, body)
		closeHTTPAPITestBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s StatusCode = %d, want 400", name, resp.StatusCode)
		}
	}

	resp = doJSONHTTPAPITestRequest(t, server, http.MethodPost, ipExpressionURL, `{"ip_addresses":["10.0.0.1"]}`)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST missing action StatusCode = %d, want 405", resp.StatusCode)
	}

	resp = doHTTPAPITestRequest(
		t,
		server,
		newHTTPAPITestRequest(t, http.MethodDelete, groupURL+"/ip-address-expressions/missing", nil),
	)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("DELETE missing expression StatusCode = %d, want 404", resp.StatusCode)
	}

	resp = doHTTPAPITestRequest(
		t,
		server,
		newHTTPAPITestRequest(
			t,
			http.MethodGet,
			server.URL+"/policy/api/v1/infra/domains/default/groups/missing/members/segments",
			nil,
		),
	)
	closeHTTPAPITestBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("members missing group StatusCode = %d, want 404", resp.StatusCode)
	}
}

//nolint:cyclop // Compact branch coverage for small validator helpers.
func TestPolicyGroupHelperErrorBranches(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	if validateOptionalStringLength(recorder, map[string]any{"display_name": 7}, "display_name", maxNSXDisplayNameLength) {
		t.Fatal("validateOptionalStringLength() = true, want false for non-string")
	}
	recorder = httptest.NewRecorder()
	if validateOptionalArrayMax(recorder, map[string]any{"group_type": "bad"}, "group_type", 1) {
		t.Fatal("validateOptionalArrayMax() = true, want false for non-array")
	}
	recorder = httptest.NewRecorder()
	if validateGroupExpressionEntry(recorder, map[string]any{"resource_type": "Condition"}, 1) {
		t.Fatal("validateGroupExpressionEntry() = true, want false for misplaced condition")
	}

	if _, ok := ipElementFamily("10.0.0.10-192.168.0.1-fd00::1"); ok {
		t.Fatal("ipElementFamily() ok = true, want false for malformed range")
	}
	if _, ok := ipElementFamily("10.0.0.0/not-prefix"); ok {
		t.Fatal("ipElementFamily() ok = true, want false for malformed prefix")
	}
	if _, ok := ipElementFamily("not-an-ip"); ok {
		t.Fatal("ipElementFamily() ok = true, want false for malformed address")
	}

	_, err := applyIPExpressionAction(
		json.RawMessage(`{"ip_addresses":["10.0.0.1"]}`),
		[]string{"10.0.0.2"},
		"bad",
	)
	if !errors.Is(err, errUnsupportedIPAddressAction) {
		t.Fatalf("applyIPExpressionAction() error = %v, want errUnsupportedIPAddressAction", err)
	}
	if _, missingErr := stringArrayField(
		map[string]any{},
		"ip_addresses",
	); !errors.Is(missingErr, errPayloadFieldMissing) {
		t.Fatalf("stringArrayField() missing error = %v, want errPayloadFieldMissing", missingErr)
	}
	if _, wrongTypeErr := stringArrayField(
		map[string]any{"ip_addresses": "bad"},
		"ip_addresses",
	); !errors.Is(wrongTypeErr, errPayloadFieldWrongType) {
		t.Fatalf("stringArrayField() wrong-type error = %v, want errPayloadFieldWrongType", wrongTypeErr)
	}
	if _, nonStringErr := stringArrayField(
		map[string]any{"ip_addresses": []any{7}},
		"ip_addresses",
	); !errors.Is(nonStringErr, errPayloadFieldWrongType) {
		t.Fatalf("stringArrayField() non-string error = %v, want errPayloadFieldWrongType", nonStringErr)
	}

	if _, mergeErr := mergeJSONObject(json.RawMessage(`{`), map[string]any{}); mergeErr == nil {
		t.Fatal("mergeJSONObject() error = nil, want invalid JSON error")
	}
	if _, stateErr := withState(json.RawMessage(`{`), "SUCCESS"); stateErr == nil {
		t.Fatal("withState() error = nil, want invalid JSON error")
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	if username := requestUsername(req); username != "nsx_admin" {
		t.Fatalf("requestUsername() = %q, want nsx_admin", username)
	}

	writeMutationError(
		httptest.NewRecorder(),
		zap.NewNop(),
		errPolicyGroupValidationTestBoom,
		"test action",
		"/infra/test",
	)
}

//nolint:cyclop // Compact branch coverage for router and request-body helpers.
func TestHTTPBoundaryHelperBranches(t *testing.T) {
	t.Parallel()

	var nilCtx context.Context
	if handler, err := NewHandler(nilCtx, AppOptions{}); err == nil || handler != nil {
		t.Fatalf("NewHandler(nil) handler = %v error = %v, want nil handler and error", handler, err)
	}

	staticRoute := Route{Path: "/fixed"}
	if _, matched := staticRoute.match("/other", ""); matched {
		t.Fatal("static route matched different path")
	}
	templateRoute := Route{Template: "/policy/api/v1/infra/domains/{domain-id}/groups"}
	if _, matched := templateRoute.match("/policy/api/v1/infra/domains/default/groups/web", ""); matched {
		t.Fatal("template route matched path with extra segment")
	}
	params, matched := templateRoute.match("/policy/api/v1/infra/domains/default/groups", "")
	if !matched {
		t.Fatal("template route did not match expected path")
	}
	if params["domain-id"] != "default" {
		t.Fatalf("domain-id param = %q, want default", params["domain-id"])
	}
	if routeParam(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil), "missing") != "" {
		t.Fatal("routeParam() without route context returned non-empty value")
	}

	for name, body := range map[string]string{
		"empty": "",
		"null":  "null",
	} {
		req := httptest.NewRequestWithContext(
			context.Background(),
			http.MethodPut,
			"/policy/api/v1/infra/domains/default/groups/web",
			strings.NewReader(body),
		)
		recorder := httptest.NewRecorder()
		if _, _, ok := readAndValidateJSONObject(recorder, req, zap.NewNop()); ok {
			t.Fatalf("%s readAndValidateJSONObject() ok = true, want false", name)
		}
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s StatusCode = %d, want 400", name, recorder.Code)
		}
	}

	db := openHTTPAPITestDB(t)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close() error = %v", err)
		}
	})
	if handler, err := NewHandler(
		context.Background(),
		AppOptions{Config: config.Config{}, DB: db},
	); err != nil || handler == nil {
		t.Fatalf("NewHandler() with nil logger/clock handler = %v error = %v, want handler and nil error", handler, err)
	}
}
