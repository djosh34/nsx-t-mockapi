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

const (
	appConcurrentCreatesPerHost = 4
	appConcurrentOpsPerHost     = 16
)

var (
	errAppConcurrentMalformedSearchResult = errors.New("malformed concurrent search result")
	errAppConcurrentUnexpectedObjectField = errors.New("unexpected concurrent object field")
	errAppConcurrentUnexpectedStatus      = errors.New("unexpected concurrent HTTP status")
)

type appConcurrentHostSpec struct {
	Host        string
	DisplayBase string
}

//nolint:cyclop // One public HTTP scenario keeps concurrent create/read/update/list/delete/search isolation connected.
func TestBuiltHandlerHandlesConcurrentMultiHostTrafficWithoutLeakage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := filepath.Join(t.TempDir(), "data")
	dbPath := filepath.Join(dataDir, "nsx-t-mockapi.db")
	configPath := writeAppConcurrentConfig(t, dbPath)
	fakeClock := clock.NewFakeClock(time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC))
	built, err := Build(ctx, Options{ConfigPath: configPath, Logger: zap.NewNop(), Clock: fakeClock})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	defer closeAppTestApplication(t, built)

	server := httptest.NewServer(built.Server.Handler)
	defer server.Close()

	hosts := []appConcurrentHostSpec{
		{Host: appTestManagerOne, DisplayBase: "MHOne"},
		{Host: appTestManagerTwo, DisplayBase: "MHTwo"},
	}
	for _, spec := range hosts {
		appConcurrentSeedHost(t, server, spec)
	}

	err = appConcurrentRun(
		appConcurrentPolicyOperations(server, hosts),
		appConcurrentManagerOperations(server, hosts),
		appConcurrentSearchOperations(server, hosts),
	)
	if err != nil {
		t.Fatalf("concurrent multi-host operations error = %v", err)
	}

	for _, spec := range hosts {
		appRequireConcurrentPolicyCreateStates(t, server, spec, "in_progress")
	}
	fakeClock.Advance(5 * time.Second)
	for _, spec := range hosts {
		appRequireConcurrentPolicyCreateStates(t, server, spec, "success")
	}

	for _, spec := range hosts {
		other := hosts[0]
		if other.Host == spec.Host {
			other = hosts[1]
		}
		segments, listErr := appConcurrentList(
			t,
			server,
			spec.Host,
			"/policy/api/v1/infra/segments",
			appsqlite.DefaultAdminPassword,
		)
		if listErr != nil {
			t.Fatalf("list policy segments for %s: %v", spec.Host, listErr)
		}
		appRequireConcurrentNames(t, spec.Host, segments, spec.DisplayBase+"Policy", other.DisplayBase+"Policy")
		appRequireConcurrentNameAbsent(t, spec.Host, segments, spec.DisplayBase+"PolicyDelete")
		appRequireConcurrentNamePresent(t, spec.Host, segments, spec.DisplayBase+"PolicyRead")
		for index := range appConcurrentCreatesPerHost {
			appRequireConcurrentNamePresent(
				t,
				spec.Host,
				segments,
				fmt.Sprintf("%sPolicyCreate%d", spec.DisplayBase, index),
			)
		}

		ipSets, ipSetListErr := appConcurrentList(
			t,
			server,
			spec.Host,
			"/api/v1/ip-sets",
			appsqlite.DefaultAdminPassword,
		)
		if ipSetListErr != nil {
			t.Fatalf("list manager IP sets for %s: %v", spec.Host, ipSetListErr)
		}
		appRequireConcurrentNames(t, spec.Host, ipSets, spec.DisplayBase+"IP", other.DisplayBase+"IP")
		appRequireConcurrentNameAbsent(t, spec.Host, ipSets, spec.DisplayBase+"IPDelete")
		appRequireConcurrentNamePresent(t, spec.Host, ipSets, spec.DisplayBase+"IPRead")
		appRequireConcurrentNamePresent(t, spec.Host, ipSets, spec.DisplayBase+"IPUpdated")
		for index := range appConcurrentCreatesPerHost {
			appRequireConcurrentNamePresent(t, spec.Host, ipSets, fmt.Sprintf("%sIPCreate%d", spec.DisplayBase, index))
		}

		policyNames, searchErr := appConcurrentSearchDisplayNames(
			t,
			server,
			spec.Host,
			"/policy/api/v1/search/query",
			"display_name:"+spec.DisplayBase+"Policy*",
			appsqlite.DefaultAdminPassword,
		)
		if searchErr != nil {
			t.Fatalf("policy search for %s: %v", spec.Host, searchErr)
		}
		appRequireConcurrentSearchNames(t, spec.Host, policyNames, spec.DisplayBase+"Policy", other.DisplayBase+"Policy")
		appRequireConcurrentSearchNamePresent(t, spec.Host, policyNames, spec.DisplayBase+"PolicyRead")
		appRequireConcurrentSearchNameAbsent(t, spec.Host, policyNames, spec.DisplayBase+"PolicyDelete")

		managerNames, managerSearchErr := appConcurrentSearchDisplayNames(
			t,
			server,
			spec.Host,
			"/api/v1/search/query",
			"display_name:"+spec.DisplayBase+"IP*",
			appsqlite.DefaultAdminPassword,
		)
		if managerSearchErr != nil {
			t.Fatalf("manager search for %s: %v", spec.Host, managerSearchErr)
		}
		appRequireConcurrentSearchNames(t, spec.Host, managerNames, spec.DisplayBase+"IP", other.DisplayBase+"IP")
		appRequireConcurrentSearchNamePresent(t, spec.Host, managerNames, spec.DisplayBase+"IPUpdated")
		appRequireConcurrentSearchNameAbsent(t, spec.Host, managerNames, spec.DisplayBase+"IPDelete")
		appRequireConcurrentUpdatedIPSet(t, spec.Host, ipSets, spec.DisplayBase+"IPUpdated")
	}

	nsxOneDB := appConcurrentResolveManagerDB(t, built, appTestManagerOne)
	nsxTwoDB := appConcurrentResolveManagerDB(t, built, appTestManagerTwo)
	t.Logf("manager database evidence: %s => %s", appTestManagerOne, nsxOneDB.Path)
	t.Logf("manager database evidence: %s => %s", appTestManagerTwo, nsxTwoDB.Path)
	appRequireConcurrentDBFile(t, nsxOneDB.Path)
	appRequireConcurrentDBFile(t, nsxTwoDB.Path)
	appRequireConcurrentDBCount(t, nsxOneDB.DB, "operation_log", appConcurrentOpsPerHost)
	appRequireConcurrentDBCount(t, nsxTwoDB.DB, "operation_log", appConcurrentOpsPerHost)
	appRequireConcurrentDBCount(t, nsxOneDB.DB, "policy_tombstones", 1)
	appRequireConcurrentDBCount(t, nsxTwoDB.DB, "policy_tombstones", 1)

	appConcurrentChangeAdminPassword(t, nsxOneDB.DB, "nsx_one_concurrent_password")
	err = appConcurrentRun([]func() error{
		func() error {
			return appConcurrentExpectStatus(
				server,
				appTestManagerOne,
				http.MethodGet,
				"/policy/api/v1/infra/segments",
				nil,
				appsqlite.DefaultAdminPassword,
				http.StatusUnauthorized,
			)
		},
		func() error {
			return appConcurrentExpectStatus(
				server,
				appTestManagerOne,
				http.MethodGet,
				"/policy/api/v1/infra/segments",
				nil,
				"nsx_one_concurrent_password",
				http.StatusOK,
			)
		},
		func() error {
			return appConcurrentExpectStatus(
				server,
				appTestManagerTwo,
				http.MethodGet,
				"/policy/api/v1/infra/segments",
				nil,
				appsqlite.DefaultAdminPassword,
				http.StatusOK,
			)
		},
	})
	if err != nil {
		t.Fatalf("concurrent auth isolation checks error = %v", err)
	}
}

func writeAppConcurrentConfig(t *testing.T, dbPath string) string {
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

func appConcurrentSeedHost(t *testing.T, server *httptest.Server, spec appConcurrentHostSpec) {
	t.Helper()

	seed := []struct {
		method string
		path   string
		body   map[string]any
	}{
		{
			method: http.MethodPatch,
			path:   "/policy/api/v1/infra/segments/" + strings.ToLower(spec.DisplayBase) + "-policy-read",
			body:   map[string]any{"display_name": spec.DisplayBase + "PolicyRead", "resource_type": "Segment"},
		},
		{
			method: http.MethodPatch,
			path:   "/policy/api/v1/infra/segments/" + strings.ToLower(spec.DisplayBase) + "-policy-delete",
			body:   map[string]any{"display_name": spec.DisplayBase + "PolicyDelete", "resource_type": "Segment"},
		},
		{
			method: http.MethodPost,
			path:   "/api/v1/ip-sets",
			body: appConcurrentIPSetPayload(
				strings.ToLower(spec.DisplayBase)+"-ip-read",
				spec.DisplayBase+"IPRead",
				"192.0.2.10",
			),
		},
		{
			method: http.MethodPost,
			path:   "/api/v1/ip-sets",
			body: appConcurrentIPSetPayload(
				strings.ToLower(spec.DisplayBase)+"-ip-update",
				spec.DisplayBase+"IPUpdate",
				"192.0.2.20",
			),
		},
		{
			method: http.MethodPost,
			path:   "/api/v1/ip-sets",
			body: appConcurrentIPSetPayload(
				strings.ToLower(spec.DisplayBase)+"-ip-delete",
				spec.DisplayBase+"IPDelete",
				"192.0.2.30",
			),
		},
	}
	for _, request := range seed {
		body, marshalErr := json.Marshal(request.body)
		if marshalErr != nil {
			t.Fatalf("Marshal() error = %v", marshalErr)
		}
		wantStatus := http.StatusOK
		if request.method == http.MethodPost {
			wantStatus = http.StatusCreated
		}
		if err := appConcurrentExpectStatus(
			server,
			spec.Host,
			request.method,
			request.path,
			body,
			appsqlite.DefaultAdminPassword,
			wantStatus,
		); err != nil {
			t.Fatalf("seed %s %s for %s: %v", request.method, request.path, spec.Host, err)
		}
	}
}

func appConcurrentPolicyOperations(server *httptest.Server, hosts []appConcurrentHostSpec) []func() error {
	ops := make([]func() error, 0, len(hosts)*(appConcurrentCreatesPerHost+3))
	for _, spec := range hosts {
		hostSpec := spec
		for index := range appConcurrentCreatesPerHost {
			createIndex := index
			ops = append(ops, func() error {
				body, err := json.Marshal(map[string]any{
					"display_name":  fmt.Sprintf("%sPolicyCreate%d", hostSpec.DisplayBase, createIndex),
					"resource_type": "Segment",
				})
				if err != nil {
					return fmt.Errorf("marshal policy segment: %w", err)
				}
				return appConcurrentExpectStatus(
					server,
					hostSpec.Host,
					http.MethodPatch,
					fmt.Sprintf(
						"/policy/api/v1/infra/segments/%s-policy-create-%d",
						strings.ToLower(hostSpec.DisplayBase),
						createIndex,
					),
					body,
					appsqlite.DefaultAdminPassword,
					http.StatusOK,
				)
			})
		}
		ops = append(ops,
			func() error {
				return appConcurrentExpectStatus(
					server,
					hostSpec.Host,
					http.MethodGet,
					"/policy/api/v1/infra/segments/"+strings.ToLower(hostSpec.DisplayBase)+"-policy-read",
					nil,
					appsqlite.DefaultAdminPassword,
					http.StatusOK,
				)
			},
			func() error {
				return appConcurrentExpectStatus(
					server,
					hostSpec.Host,
					http.MethodGet,
					"/policy/api/v1/infra/segments",
					nil,
					appsqlite.DefaultAdminPassword,
					http.StatusOK,
				)
			},
			func() error {
				return appConcurrentExpectStatus(
					server,
					hostSpec.Host,
					http.MethodDelete,
					"/policy/api/v1/infra/segments/"+strings.ToLower(hostSpec.DisplayBase)+"-policy-delete",
					nil,
					appsqlite.DefaultAdminPassword,
					http.StatusOK,
				)
			},
		)
	}
	return ops
}

func appConcurrentManagerOperations(server *httptest.Server, hosts []appConcurrentHostSpec) []func() error {
	ops := make([]func() error, 0, len(hosts)*(appConcurrentCreatesPerHost+4))
	for _, spec := range hosts {
		hostSpec := spec
		for index := range appConcurrentCreatesPerHost {
			createIndex := index
			ops = append(ops, func() error {
				body, err := json.Marshal(appConcurrentIPSetPayload(
					fmt.Sprintf("%s-ip-create-%d", strings.ToLower(hostSpec.DisplayBase), createIndex),
					fmt.Sprintf("%sIPCreate%d", hostSpec.DisplayBase, createIndex),
					fmt.Sprintf("198.51.100.%d", 10+createIndex),
				))
				if err != nil {
					return fmt.Errorf("marshal manager IP set: %w", err)
				}
				return appConcurrentExpectStatus(
					server,
					hostSpec.Host,
					http.MethodPost,
					"/api/v1/ip-sets",
					body,
					appsqlite.DefaultAdminPassword,
					http.StatusCreated,
				)
			})
		}
		updateBody, marshalErr := json.Marshal(map[string]any{
			"display_name":  hostSpec.DisplayBase + "IPUpdated",
			"resource_type": "IPSet",
			"ip_addresses":  []string{"203.0.113.20"},
			"_revision":     0,
		})
		if marshalErr != nil {
			ops = append(ops, func() error {
				return fmt.Errorf("marshal manager IP set update: %w", marshalErr)
			})
			continue
		}
		ops = append(ops,
			func() error {
				return appConcurrentExpectStatus(
					server,
					hostSpec.Host,
					http.MethodPut,
					"/api/v1/ip-sets/"+strings.ToLower(hostSpec.DisplayBase)+"-ip-update",
					updateBody,
					appsqlite.DefaultAdminPassword,
					http.StatusOK,
				)
			},
			func() error {
				return appConcurrentExpectStatus(
					server,
					hostSpec.Host,
					http.MethodGet,
					"/api/v1/ip-sets/"+strings.ToLower(hostSpec.DisplayBase)+"-ip-read",
					nil,
					appsqlite.DefaultAdminPassword,
					http.StatusOK,
				)
			},
			func() error {
				return appConcurrentExpectStatus(
					server,
					hostSpec.Host,
					http.MethodGet,
					"/api/v1/ip-sets",
					nil,
					appsqlite.DefaultAdminPassword,
					http.StatusOK,
				)
			},
			func() error {
				return appConcurrentExpectStatus(
					server,
					hostSpec.Host,
					http.MethodDelete,
					"/api/v1/ip-sets/"+strings.ToLower(hostSpec.DisplayBase)+"-ip-delete",
					nil,
					appsqlite.DefaultAdminPassword,
					http.StatusOK,
				)
			},
		)
	}
	return ops
}

func appConcurrentSearchOperations(server *httptest.Server, hosts []appConcurrentHostSpec) []func() error {
	ops := make([]func() error, 0, len(hosts)*2)
	for _, spec := range hosts {
		hostSpec := spec
		ops = append(ops,
			func() error {
				return appConcurrentExpectStatus(
					server,
					hostSpec.Host,
					http.MethodGet,
					"/policy/api/v1/search/query?query="+url.QueryEscape("display_name:"+hostSpec.DisplayBase+"Policy*"),
					nil,
					appsqlite.DefaultAdminPassword,
					http.StatusOK,
				)
			},
			func() error {
				return appConcurrentExpectStatus(
					server,
					hostSpec.Host,
					http.MethodGet,
					"/api/v1/search/query?query="+url.QueryEscape("display_name:"+hostSpec.DisplayBase+"IP*"),
					nil,
					appsqlite.DefaultAdminPassword,
					http.StatusOK,
				)
			},
		)
	}
	return ops
}

func appConcurrentIPSetPayload(id string, displayName string, ipAddress string) map[string]any {
	return map[string]any{
		"id":            id,
		"display_name":  displayName,
		"resource_type": "IPSet",
		"ip_addresses":  []string{ipAddress},
	}
}

func appConcurrentRun(operationGroups ...[]func() error) error {
	var operations []func() error
	for _, group := range operationGroups {
		operations = append(operations, group...)
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(operations))
	for _, operation := range operations {
		op := operation
		wg.Go(func() {
			if err := op(); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)

	var joined []error
	for err := range errs {
		joined = append(joined, err)
	}
	if len(joined) == 0 {
		return nil
	}
	return fmt.Errorf("%d concurrent operation(s) failed: %w", len(joined), joined[0])
}

func appConcurrentExpectStatus(
	server *httptest.Server,
	host string,
	method string,
	path string,
	body []byte,
	password string,
	wantStatus int,
) error {
	req, err := http.NewRequestWithContext(context.Background(), method, server.URL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("new request %s %s: %w", method, path, err)
	}
	req.Host = host
	req.SetBasicAuth(appsqlite.DefaultAdminUsername, password)
	resp, err := server.Client().Do(req)
	if err != nil {
		return fmt.Errorf("do request %s %s host %s: %w", method, path, host, err)
	}
	if resp.StatusCode == wantStatus {
		if closeErr := resp.Body.Close(); closeErr != nil {
			return fmt.Errorf("close response body for %s %s host %s: %w", method, path, host, closeErr)
		}
		return nil
	}
	responseBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			return fmt.Errorf(
				"read response body for %s %s host %s: %w; close response body: %w",
				method,
				path,
				host,
				readErr,
				closeErr,
			)
		}
		return fmt.Errorf("read response body for %s %s host %s: %w", method, path, host, readErr)
	}
	if closeErr := resp.Body.Close(); closeErr != nil {
		return fmt.Errorf("close response body for %s %s host %s: %w", method, path, host, closeErr)
	}
	return fmt.Errorf(
		"%w: "+
			"%s %s host %s StatusCode = %d, want %d, body %q",
		errAppConcurrentUnexpectedStatus,
		method,
		path,
		host,
		resp.StatusCode,
		wantStatus,
		string(responseBody),
	)
}

func appConcurrentList(
	t *testing.T,
	server *httptest.Server,
	host string,
	path string,
	password string,
) ([]map[string]any, error) {
	t.Helper()

	var decoded struct {
		Results []map[string]any `json:"results"`
	}
	if err := appConcurrentGetDecoded(t, server, host, path, password, &decoded); err != nil {
		return nil, fmt.Errorf("get decoded list %s host %s: %w", path, host, err)
	}
	return decoded.Results, nil
}

func appConcurrentSearchDisplayNames(
	t *testing.T,
	server *httptest.Server,
	host string,
	path string,
	query string,
	password string,
) ([]string, error) {
	t.Helper()

	searchPath := path + "?query=" + url.QueryEscape(query)
	results, err := appConcurrentList(t, server, host, searchPath, password)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(results))
	for _, result := range results {
		name, ok := result["display_name"].(string)
		if !ok {
			return nil, fmt.Errorf(
				"%w: display_name = %#v, want string",
				errAppConcurrentMalformedSearchResult,
				result["display_name"],
			)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func appRequireConcurrentPolicyCreateStates(
	t *testing.T,
	server *httptest.Server,
	spec appConcurrentHostSpec,
	wantState string,
) {
	t.Helper()

	for index := range appConcurrentCreatesPerHost {
		segmentID := fmt.Sprintf("%s-policy-create-%d", strings.ToLower(spec.DisplayBase), index)
		state, err := appConcurrentReadStringField(
			t,
			server,
			spec.Host,
			"/policy/api/v1/infra/segments/"+segmentID+"/state",
			"state",
			appsqlite.DefaultAdminPassword,
		)
		if err != nil {
			t.Fatalf("read policy segment state for %s host %s: %v", segmentID, spec.Host, err)
		}
		if state != wantState {
			t.Fatalf("policy segment %s host %s state = %q, want %q", segmentID, spec.Host, state, wantState)
		}
	}
}

func appConcurrentReadStringField(
	t *testing.T,
	server *httptest.Server,
	host string,
	path string,
	field string,
	password string,
) (string, error) {
	t.Helper()

	var decoded map[string]any
	if err := appConcurrentGetDecoded(t, server, host, path, password, &decoded); err != nil {
		return "", fmt.Errorf("get decoded object %s host %s: %w", path, host, err)
	}
	value, ok := decoded[field].(string)
	if !ok {
		return "", fmt.Errorf("%w: field %q = %#v, want string", errAppConcurrentUnexpectedObjectField, field, decoded[field])
	}
	return value, nil
}

func appConcurrentGetDecoded(
	t *testing.T,
	server *httptest.Server,
	host string,
	path string,
	password string,
	target any,
) error {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+path, nil)
	if err != nil {
		return fmt.Errorf("new GET request %s: %w", path, err)
	}
	req.Host = host
	req.SetBasicAuth(appsqlite.DefaultAdminUsername, password)
	resp, err := server.Client().Do(req)
	if err != nil {
		return fmt.Errorf("do GET request %s host %s: %w", path, host, err)
	}
	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			closeErr := resp.Body.Close()
			if closeErr != nil {
				return fmt.Errorf("read GET response body: %w; close GET response body: %w", readErr, closeErr)
			}
			return fmt.Errorf("read GET response body: %w", readErr)
		}
		if closeErr := resp.Body.Close(); closeErr != nil {
			return fmt.Errorf("close GET response body: %w", closeErr)
		}
		return fmt.Errorf(
			"%w: GET %s host %s StatusCode = %d, want 200, body %q",
			errAppConcurrentUnexpectedStatus,
			path,
			host,
			resp.StatusCode,
			string(body),
		)
	}
	if err = json.NewDecoder(resp.Body).Decode(target); err != nil {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			return fmt.Errorf("decode GET response: %w; close GET response body: %w", err, closeErr)
		}
		return fmt.Errorf("decode GET response: %w", err)
	}
	if closeErr := resp.Body.Close(); closeErr != nil {
		return fmt.Errorf("close GET response body: %w", closeErr)
	}
	return nil
}

func appRequireConcurrentNames(
	t *testing.T,
	host string,
	results []map[string]any,
	wantPrefix string,
	forbiddenPrefix string,
) {
	t.Helper()

	for _, result := range results {
		displayName, ok := result["display_name"].(string)
		if !ok {
			t.Fatalf("host %s display_name = %#v, want string", host, result["display_name"])
		}
		if strings.HasPrefix(displayName, forbiddenPrefix) {
			t.Fatalf("host %s leaked forbidden display_name %q", host, displayName)
		}
		if !strings.HasPrefix(displayName, wantPrefix) {
			t.Fatalf("host %s display_name %q does not have expected prefix %q", host, displayName, wantPrefix)
		}
	}
}

func appRequireConcurrentSearchNames(
	t *testing.T,
	host string,
	names []string,
	wantPrefix string,
	forbiddenPrefix string,
) {
	t.Helper()

	if len(names) == 0 {
		t.Fatalf("host %s search names empty, want names with prefix %q", host, wantPrefix)
	}
	for _, name := range names {
		if strings.HasPrefix(name, forbiddenPrefix) {
			t.Fatalf("host %s search leaked forbidden display_name %q", host, name)
		}
		if !strings.HasPrefix(name, wantPrefix) {
			t.Fatalf("host %s search display_name %q does not have expected prefix %q", host, name, wantPrefix)
		}
	}
}

func appRequireConcurrentSearchNamePresent(t *testing.T, host string, names []string, want string) {
	t.Helper()

	if slices.Contains(names, want) {
		return
	}
	t.Fatalf("host %s search display_name %q absent from %#v", host, want, names)
}

func appRequireConcurrentSearchNameAbsent(t *testing.T, host string, names []string, forbidden string) {
	t.Helper()

	for _, name := range names {
		if name == forbidden {
			t.Fatalf("host %s search display_name %q present in %#v", host, forbidden, names)
		}
	}
}

func appRequireConcurrentUpdatedIPSet(t *testing.T, host string, results []map[string]any, displayName string) {
	t.Helper()

	for _, result := range results {
		if result["display_name"] != displayName {
			continue
		}
		revision, ok := result["_revision"].(float64)
		if !ok {
			t.Fatalf("host %s updated IPSet _revision = %#v, want number", host, result["_revision"])
		}
		if revision != 1 {
			t.Fatalf("host %s updated IPSet _revision = %.0f, want 1", host, revision)
		}
		addresses, ok := result["ip_addresses"].([]any)
		if !ok {
			t.Fatalf("host %s updated IPSet ip_addresses = %#v, want array", host, result["ip_addresses"])
		}
		if len(addresses) != 1 || addresses[0] != "203.0.113.20" {
			t.Fatalf("host %s updated IPSet ip_addresses = %#v, want [203.0.113.20]", host, addresses)
		}
		return
	}
	t.Fatalf("host %s updated IPSet %q absent from %#v", host, displayName, appConcurrentDisplayNames(results))
}

func appRequireConcurrentNamePresent(t *testing.T, host string, results []map[string]any, want string) {
	t.Helper()

	for _, result := range results {
		if result["display_name"] == want {
			return
		}
	}
	t.Fatalf("host %s display_name %q absent from %#v", host, want, appConcurrentDisplayNames(results))
}

func appRequireConcurrentNameAbsent(t *testing.T, host string, results []map[string]any, forbidden string) {
	t.Helper()

	for _, result := range results {
		if result["display_name"] == forbidden {
			t.Fatalf("host %s display_name %q present in %#v", host, forbidden, appConcurrentDisplayNames(results))
		}
	}
}

func appConcurrentDisplayNames(results []map[string]any) []string {
	names := make([]string, 0, len(results))
	for _, result := range results {
		displayName, ok := result["display_name"].(string)
		if !ok {
			continue
		}
		names = append(names, displayName)
	}
	sort.Strings(names)
	return names
}

func appConcurrentResolveManagerDB(t *testing.T, built *Application, host string) appsqlite.ManagerDatabase {
	t.Helper()

	managerDB, err := built.ManagerDatabases.ResolveManagerDatabase(context.Background(), host)
	if err != nil {
		t.Fatalf("ResolveManagerDatabase(%s) error = %v", host, err)
	}
	return managerDB
}

func appRequireConcurrentDBFile(t *testing.T, path string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("manager database %q Stat() error = %v", path, err)
	}
	if info.IsDir() {
		t.Fatalf("manager database %q is directory, want file", path)
	}
}

func appRequireConcurrentDBCount(t *testing.T, db *sql.DB, subject string, want int) {
	t.Helper()

	var query string
	switch subject {
	case "operation_log":
		query = "SELECT count(*) FROM operation_log"
	case "policy_tombstones":
		query = "SELECT count(*) FROM resources WHERE marked_for_delete = 1"
	default:
		t.Fatalf("unsupported DB count subject %q", subject)
	}

	var got int
	if err := db.QueryRowContext(context.Background(), query).Scan(&got); err != nil {
		t.Fatalf("count %s error = %v", subject, err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", subject, got, want)
	}
}

func appConcurrentChangeAdminPassword(t *testing.T, db *sql.DB, password string) {
	t.Helper()

	result, err := db.ExecContext(
		context.Background(),
		"UPDATE users SET password = ? WHERE username = ?",
		password,
		appsqlite.DefaultAdminUsername,
	)
	if err != nil {
		t.Fatalf("update admin password error = %v", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("RowsAffected() error = %v", err)
	}
	if rows != 1 {
		t.Fatalf("RowsAffected() = %d, want 1", rows)
	}
}
