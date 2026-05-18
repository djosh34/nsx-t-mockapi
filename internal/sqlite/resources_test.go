package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"nsx-t-mockapi/internal/clock"
	"nsx-t-mockapi/internal/config"
)

func TestResourceStoreBootstrapCreatesResourceTablesAndKeepsUserBootstrapCompatible(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close() error = %v", err)
		}
	}()

	if _, err := NewUserStore(db).EnsureBootstrap(ctx); err != nil {
		t.Fatalf("EnsureBootstrap() users error = %v", err)
	}

	store := NewResourceStore(db, ResourceStoreOptions{})
	if err := store.EnsureBootstrap(ctx); err != nil {
		t.Fatalf("EnsureBootstrap() resources error = %v", err)
	}

	for _, table := range []string{
		"resources",
		"resource_edges",
		"resource_realization",
		"operation_log",
		"resource_fts",
		"search_fields",
	} {
		if !tableExists(t, db, table) {
			t.Fatalf("tableExists(%q) = false, want true", table)
		}
	}
	if !virtualTableUsesFTS5(t, db, "resource_fts") {
		t.Fatal("resource_fts is not an FTS5 virtual table")
	}

	user, found, err := NewUserStore(db).FindUser(ctx, DefaultAdminUsername)
	if err != nil {
		t.Fatalf("FindUser() error = %v", err)
	}
	if !found {
		t.Fatalf("FindUser(%q) found = false, want true", DefaultAdminUsername)
	}
	if user.Role != RoleAdmin {
		t.Fatalf("FindUser() role = %q, want %q", user.Role, RoleAdmin)
	}
}

func TestResourceStoreCreatePolicyResourceStoresCanonicalJSONBPayload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	fakeClock := clock.NewFakeClock(time.Date(2026, 5, 17, 10, 30, 0, 0, time.UTC))
	store := NewResourceStore(db, ResourceStoreOptions{Clock: fakeClock})
	spec := groupResourceSpec("web")

	resource, err := store.Mutate(ctx, Mutation{
		Spec:          spec,
		Body:          json.RawMessage(`{"description":"web group"}`),
		Username:      DefaultAdminUsername,
		Operation:     ResourceOperationCreate,
		RequestPath:   "/policy/api/v1/infra/domains/default/groups/web",
		RouteTemplate: "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}",
		StatusCode:    200,
	})
	if err != nil {
		t.Fatalf("Mutate() error = %v", err)
	}

	payload := decodeResourcePayload(t, resource.Payload)
	requirePayloadString(t, payload, "id", "web")
	requirePayloadString(t, payload, "display_name", "web")
	requirePayloadString(t, payload, "resource_type", "Group")
	requirePayloadString(t, payload, "path", "/infra/domains/default/groups/web")
	requirePayloadString(t, payload, "parent_path", "/infra/domains/default")
	requirePayloadString(t, payload, "relative_path", "web")
	requirePayloadString(t, payload, "_create_user", DefaultAdminUsername)
	requirePayloadString(t, payload, "_last_modified_user", DefaultAdminUsername)
	requirePayloadString(t, payload, "_protection", "NOT_PROTECTED")
	requirePayloadNumber(t, payload, "_create_time", 1779013800000)
	requirePayloadNumber(t, payload, "_last_modified_time", 1779013800000)
	requirePayloadNumber(t, payload, "_revision", 0)
	requirePayloadBool(t, payload, "_system_owned", false)
	requirePayloadBool(t, payload, "marked_for_delete", false)

	var storageType string
	var valid int
	err = db.QueryRowContext(
		ctx,
		"SELECT typeof(payload), json_valid(payload, 8) FROM resources WHERE path = ?",
		spec.Path,
	).Scan(&storageType, &valid)
	if err != nil {
		t.Fatalf("query stored payload type error = %v", err)
	}
	if storageType != sqliteStorageBlob {
		t.Fatalf("typeof(payload) = %q, want blob", storageType)
	}
	if valid != 1 {
		t.Fatalf("json_valid(payload, 8) = %d, want 1", valid)
	}
}

func TestResourceStoreCreateIgnoresClientSuppliedReadOnlyMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := NewResourceStore(db, ResourceStoreOptions{
		Clock: clock.NewFakeClock(time.Date(2026, 5, 17, 10, 30, 0, 0, time.UTC)),
	})
	spec := groupResourceSpec("web")

	resource, err := store.Mutate(ctx, Mutation{
		Spec: spec,
		Body: json.RawMessage(`{
			"id":"wrong",
			"display_name":"Client Name",
			"resource_type":"WrongType",
			"path":"/wrong",
			"parent_path":"/wrong-parent",
			"relative_path":"wrong-relative",
			"_create_user":"mallory",
			"_create_time":1,
			"_last_modified_user":"mallory",
			"_last_modified_time":2,
			"_system_owned":true,
			"_protection":"PROTECTED",
			"_revision":99,
			"marked_for_delete":true
		}`),
		Username:      DefaultAdminUsername,
		Operation:     ResourceOperationCreate,
		RequestPath:   "/policy/api/v1/infra/domains/default/groups/web",
		RouteTemplate: "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}",
		StatusCode:    200,
	})
	if err != nil {
		t.Fatalf("Mutate() error = %v", err)
	}

	payload := decodeResourcePayload(t, resource.Payload)
	requirePayloadString(t, payload, "id", "web")
	requirePayloadString(t, payload, "display_name", "Client Name")
	requirePayloadString(t, payload, "resource_type", "Group")
	requirePayloadString(t, payload, "path", "/infra/domains/default/groups/web")
	requirePayloadString(t, payload, "parent_path", "/infra/domains/default")
	requirePayloadString(t, payload, "relative_path", "web")
	requirePayloadString(t, payload, "_create_user", DefaultAdminUsername)
	requirePayloadNumber(t, payload, "_create_time", 1779013800000)
	requirePayloadString(t, payload, "_last_modified_user", DefaultAdminUsername)
	requirePayloadNumber(t, payload, "_last_modified_time", 1779013800000)
	requirePayloadBool(t, payload, "_system_owned", false)
	requirePayloadString(t, payload, "_protection", "NOT_PROTECTED")
	requirePayloadNumber(t, payload, "_revision", 0)
	requirePayloadBool(t, payload, "marked_for_delete", false)
}

func TestResourceStoreCreatePolicyResourcePopulatesFullTextSearch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := NewResourceStore(db, ResourceStoreOptions{
		Clock: clock.NewFakeClock(time.Date(2026, 5, 17, 10, 30, 0, 0, time.UTC)),
	})
	spec := groupResourceSpec("web-alpha")

	createTestResource(ctx, t, store, spec, json.RawMessage(`{
		"display_name":"Web Alpha",
		"description":"Handles payment traffic",
		"tags":[{"scope":"env","tag":"prod"},{"scope":"tier","tag":"frontend"}]
	}`))

	for _, term := range []string{"Alpha", "payment", "groups", "Group", "frontend"} {
		if !resourceFTSMatches(ctx, t, db, spec.Path, term) {
			t.Fatalf("resource_fts MATCH %q found no row for %q", term, spec.Path)
		}
	}
}

func TestResourceStoreCreatePolicyResourcePopulatesStructuredSearchFields(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := NewResourceStore(db, ResourceStoreOptions{
		Clock: clock.NewFakeClock(time.Date(2026, 5, 17, 10, 30, 0, 0, time.UTC)),
	})
	spec := groupResourceSpec("web-alpha")

	createTestResource(ctx, t, store, spec, json.RawMessage(`{
		"display_name":"Web Alpha",
		"description":"Handles payment traffic",
		"tags":[{"scope":"env","tag":"prod"},{"scope":"tier","tag":"frontend"}]
	}`))

	for fieldName, textValue := range map[string]string{
		"resource_type": "group",
		"display_name":  "web alpha",
		"tags.scope":    "env",
		"tags.tag":      "frontend",
	} {
		if !searchFieldTextExists(ctx, t, db, spec.Path, fieldName, textValue) {
			t.Fatalf("search_fields missing text field %s=%q for %q", fieldName, textValue, spec.Path)
		}
	}
	if !searchFieldBoolExists(ctx, t, db, spec.Path, "marked_for_delete", false) {
		t.Fatalf("search_fields missing marked_for_delete=false for %q", spec.Path)
	}
	if !searchFieldNumberExists(ctx, t, db, spec.Path, "_revision", 0) {
		t.Fatalf("search_fields missing numeric _revision=0 for %q", spec.Path)
	}
}

func TestResourceStoreUpdateWithMatchingRevisionPreservesCreateMetadataAndIncrementsRevision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	start := time.Date(2026, 5, 17, 10, 30, 0, 0, time.UTC)
	fakeClock := clock.NewFakeClock(start)
	store := NewResourceStore(db, ResourceStoreOptions{Clock: fakeClock})
	spec := groupResourceSpec("web")

	created, err := store.Mutate(ctx, Mutation{
		Spec:          spec,
		Body:          json.RawMessage(`{"display_name":"Web"}`),
		Username:      DefaultAdminUsername,
		Operation:     ResourceOperationCreate,
		RequestPath:   "/policy/api/v1/infra/domains/default/groups/web",
		RouteTemplate: "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}",
		StatusCode:    200,
	})
	if err != nil {
		t.Fatalf("Mutate() create error = %v", err)
	}
	createdPayload := decodeResourcePayload(t, created.Payload)
	requirePayloadNumber(t, createdPayload, "_revision", 0)

	fakeClock.Advance(2 * time.Second)
	updated, err := store.Mutate(ctx, Mutation{
		Spec:            spec,
		Body:            json.RawMessage(`{"display_name":"Web Updated","description":"new","_revision":0}`),
		Username:        DefaultAdminUsername,
		Operation:       ResourceOperationUpdate,
		EnforceRevision: true,
		RequestPath:     "/policy/api/v1/infra/domains/default/groups/web",
		RouteTemplate:   "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}",
		StatusCode:      200,
	})
	if err != nil {
		t.Fatalf("Mutate() update error = %v", err)
	}

	payload := decodeResourcePayload(t, updated.Payload)
	requirePayloadString(t, payload, "display_name", "Web Updated")
	requirePayloadString(t, payload, "description", "new")
	requirePayloadString(t, payload, "_create_user", DefaultAdminUsername)
	requirePayloadNumber(t, payload, "_create_time", 1779013800000)
	requirePayloadString(t, payload, "_last_modified_user", DefaultAdminUsername)
	requirePayloadNumber(t, payload, "_last_modified_time", 1779013802000)
	requirePayloadNumber(t, payload, "_revision", 1)
	if updated.Revision != 1 {
		t.Fatalf("updated Revision = %d, want 1", updated.Revision)
	}

	var revision int
	err = db.QueryRowContext(ctx, "SELECT revision FROM resources WHERE path = ?", spec.Path).Scan(&revision)
	if err != nil {
		t.Fatalf("query relational revision error = %v", err)
	}
	if revision != 1 {
		t.Fatalf("relational revision = %d, want 1", revision)
	}
}

func TestResourceStoreUpdateReplacesSearchIndexRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := NewResourceStore(db, ResourceStoreOptions{
		Clock: clock.NewFakeClock(time.Date(2026, 5, 17, 10, 30, 0, 0, time.UTC)),
	})
	spec := groupResourceSpec("web")

	createTestResource(ctx, t, store, spec, json.RawMessage(`{
		"display_name":"Legacy Web",
		"description":"old payment traffic",
		"tags":[{"scope":"env","tag":"legacy"}]
	}`))

	_, err := store.Mutate(ctx, Mutation{
		Spec: spec,
		Body: json.RawMessage(`{
			"display_name":"Modern Web",
			"description":"new edge traffic",
			"tags":[{"scope":"env","tag":"modern"}],
			"_revision":0
		}`),
		Username:        DefaultAdminUsername,
		Operation:       ResourceOperationUpdate,
		EnforceRevision: true,
		RequestPath:     "/policy/api/v1/infra/domains/default/groups/web",
		RouteTemplate:   "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}",
		StatusCode:      200,
	})
	if err != nil {
		t.Fatalf("Mutate() update error = %v", err)
	}

	if resourceFTSMatches(ctx, t, db, spec.Path, "Legacy") {
		t.Fatalf("resource_fts still matches old display name for %q", spec.Path)
	}
	if !resourceFTSMatches(ctx, t, db, spec.Path, "Modern") {
		t.Fatalf("resource_fts missing new display name for %q", spec.Path)
	}
	if searchFieldTextExists(ctx, t, db, spec.Path, "tags.tag", "legacy") {
		t.Fatalf("search_fields still has old tag for %q", spec.Path)
	}
	if !searchFieldTextExists(ctx, t, db, spec.Path, "tags.tag", "modern") {
		t.Fatalf("search_fields missing new tag for %q", spec.Path)
	}
}

func TestResourceStoreStaleEnforcedUpdateReturnsConflictAndLeavesPayloadUnchanged(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := NewResourceStore(db, ResourceStoreOptions{
		Clock: clock.NewFakeClock(time.Date(2026, 5, 17, 10, 30, 0, 0, time.UTC)),
	})
	spec := groupResourceSpec("web")

	created, err := store.Mutate(ctx, Mutation{
		Spec:          spec,
		Body:          json.RawMessage(`{"display_name":"Web"}`),
		Username:      DefaultAdminUsername,
		Operation:     ResourceOperationCreate,
		RequestPath:   "/policy/api/v1/infra/domains/default/groups/web",
		RouteTemplate: "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}",
		StatusCode:    200,
	})
	if err != nil {
		t.Fatalf("Mutate() create error = %v", err)
	}

	_, err = store.Mutate(ctx, Mutation{
		Spec:            spec,
		Body:            json.RawMessage(`{"display_name":"Changed","_revision":7}`),
		Username:        DefaultAdminUsername,
		Operation:       ResourceOperationUpdate,
		EnforceRevision: true,
		RequestPath:     "/policy/api/v1/infra/domains/default/groups/web",
		RouteTemplate:   "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}",
		StatusCode:      200,
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("Mutate() stale update error = %v, want ErrRevisionConflict", err)
	}

	read, found, err := store.Read(ctx, ReadOptions{Path: spec.Path})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !found {
		t.Fatal("Read() found = false, want true")
	}
	if string(read.Payload) != string(created.Payload) {
		t.Fatalf("stored payload changed after conflict:\ngot  %s\nwant %s", read.Payload, created.Payload)
	}
}

func TestResourceStoreStaleEnforcedUpdateLeavesSearchIndexUnchanged(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := NewResourceStore(db, ResourceStoreOptions{
		Clock: clock.NewFakeClock(time.Date(2026, 5, 17, 10, 30, 0, 0, time.UTC)),
	})
	spec := groupResourceSpec("web")

	createTestResource(ctx, t, store, spec, json.RawMessage(`{
		"display_name":"Stable Web",
		"description":"known good traffic",
		"tags":[{"scope":"env","tag":"stable"}]
	}`))

	_, err := store.Mutate(ctx, Mutation{
		Spec: spec,
		Body: json.RawMessage(`{
			"display_name":"Broken Web",
			"description":"bad traffic",
			"tags":[{"scope":"env","tag":"broken"}],
			"_revision":7
		}`),
		Username:        DefaultAdminUsername,
		Operation:       ResourceOperationUpdate,
		EnforceRevision: true,
		RequestPath:     "/policy/api/v1/infra/domains/default/groups/web",
		RouteTemplate:   "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}",
		StatusCode:      200,
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("Mutate() stale update error = %v, want ErrRevisionConflict", err)
	}

	if !resourceFTSMatches(ctx, t, db, spec.Path, "Stable") {
		t.Fatalf("resource_fts missing previous display name for %q", spec.Path)
	}
	if resourceFTSMatches(ctx, t, db, spec.Path, "Broken") {
		t.Fatalf("resource_fts matches failed mutation display name for %q", spec.Path)
	}
	if !searchFieldTextExists(ctx, t, db, spec.Path, "tags.tag", "stable") {
		t.Fatalf("search_fields missing previous tag for %q", spec.Path)
	}
	if searchFieldTextExists(ctx, t, db, spec.Path, "tags.tag", "broken") {
		t.Fatalf("search_fields has failed mutation tag for %q", spec.Path)
	}
}

func TestResourceStorePolicyDeletePersistsTombstoneAndHidesNormalReadAndList(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	fakeClock := clock.NewFakeClock(time.Date(2026, 5, 17, 10, 30, 0, 0, time.UTC))
	store := NewResourceStore(db, ResourceStoreOptions{Clock: fakeClock})
	spec := groupResourceSpec("web")

	createTestResource(ctx, t, store, spec, json.RawMessage(`{"display_name":"Web"}`))

	fakeClock.Advance(3 * time.Second)
	deleted := deleteTestResource(ctx, t, store, spec)
	if deleted.Revision != 1 {
		t.Fatalf("deleted Revision = %d, want 1", deleted.Revision)
	}

	requireNormalReadAndListHidden(ctx, t, store, spec)

	inspected, found, err := store.Read(ctx, ReadOptions{Path: spec.Path, IncludeTombstones: true})
	if err != nil {
		t.Fatalf("Read() include tombstone error = %v", err)
	}
	if !found {
		t.Fatal("Read() include tombstone found = false, want true")
	}
	payload := decodeResourcePayload(t, inspected.Payload)
	requirePayloadBool(t, payload, "marked_for_delete", true)
	requirePayloadNumber(t, payload, "_revision", 1)
	requirePayloadNumber(t, payload, "_last_modified_time", 1779013803000)

	requireTombstoneColumns(ctx, t, db, spec.Path, 1779013803000)
}

func TestResourceStorePolicyDeleteIndexesTombstone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := NewResourceStore(db, ResourceStoreOptions{
		Clock: clock.NewFakeClock(time.Date(2026, 5, 17, 10, 30, 0, 0, time.UTC)),
	})
	spec := groupResourceSpec("web")

	createTestResource(ctx, t, store, spec, json.RawMessage(`{
		"display_name":"Deleted Web",
		"description":"retired traffic"
	}`))
	deleteTestResource(ctx, t, store, spec)

	requireNormalReadAndListHidden(ctx, t, store, spec)
	if !resourceFTSMatches(ctx, t, db, spec.Path, "Deleted") {
		t.Fatalf("resource_fts missing tombstoned resource for %q", spec.Path)
	}
	if !searchFieldBoolExists(ctx, t, db, spec.Path, "marked_for_delete", true) {
		t.Fatalf("search_fields missing marked_for_delete=true for %q", spec.Path)
	}
	if searchFieldBoolExists(ctx, t, db, spec.Path, "marked_for_delete", false) {
		t.Fatalf("search_fields still has marked_for_delete=false for tombstoned %q", spec.Path)
	}
}

func TestResourceStoreManagerHardDeleteRemovesSearchIndexRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := NewResourceStore(db, ResourceStoreOptions{
		Clock: clock.NewFakeClock(time.Date(2026, 5, 17, 10, 30, 0, 0, time.UTC)),
	})
	spec := managerIPSetResourceSpec("ipset-a")

	createTestResource(ctx, t, store, spec, json.RawMessage(`{
		"display_name":"Blocked IPs",
		"description":"temporary block list"
	}`))
	deleteTestResource(ctx, t, store, spec)

	if resourceFTSMatches(ctx, t, db, spec.Path, "Blocked") {
		t.Fatalf("resource_fts still has hard-deleted manager resource %q", spec.Path)
	}
	if searchFieldTextExists(ctx, t, db, spec.Path, "display_name", "blocked ips") {
		t.Fatalf("search_fields still has hard-deleted manager resource %q", spec.Path)
	}
}

func TestResourceStoreRealizationStatusMovesFromPendingToSuccessWithFakeClock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	fakeClock := clock.NewFakeClock(time.Date(2026, 5, 17, 10, 30, 0, 0, time.UTC))
	store := NewResourceStore(db, ResourceStoreOptions{
		Clock: fakeClock,
		Realization: config.RealizationConfig{
			CreateDelayMS: 5000,
			KindDelayMS:   map[string]int{},
		},
	})
	spec := groupResourceSpec("web")

	created, err := store.Mutate(ctx, Mutation{
		Spec:          spec,
		Body:          json.RawMessage(`{"display_name":"Web"}`),
		Username:      DefaultAdminUsername,
		Operation:     ResourceOperationCreate,
		RequestPath:   "/policy/api/v1/infra/domains/default/groups/web",
		RouteTemplate: "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}",
		StatusCode:    200,
	})
	if err != nil {
		t.Fatalf("Mutate() create error = %v", err)
	}
	if created.RealizationStatus != realizationPending {
		t.Fatalf("created RealizationStatus = %q, want IN_PROGRESS", created.RealizationStatus)
	}

	read, found, err := store.Read(ctx, ReadOptions{Path: spec.Path})
	if err != nil {
		t.Fatalf("Read() before delay error = %v", err)
	}
	if !found {
		t.Fatal("Read() before delay found = false, want true")
	}
	if read.RealizationStatus != realizationPending {
		t.Fatalf("read before delay RealizationStatus = %q, want IN_PROGRESS", read.RealizationStatus)
	}

	fakeClock.Advance(5 * time.Second)
	read, found, err = store.Read(ctx, ReadOptions{Path: spec.Path})
	if err != nil {
		t.Fatalf("Read() after delay error = %v", err)
	}
	if !found {
		t.Fatal("Read() after delay found = false, want true")
	}
	if read.RealizationStatus != realizationSuccess {
		t.Fatalf("read after delay RealizationStatus = %q, want SUCCESS", read.RealizationStatus)
	}
}

func TestResourceStoreListReturnsMultipleResourcesWithRealizationStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	fakeClock := clock.NewFakeClock(time.Date(2026, 5, 17, 10, 30, 0, 0, time.UTC))
	store := NewResourceStore(db, ResourceStoreOptions{
		Clock: fakeClock,
		Realization: config.RealizationConfig{
			DefaultDelayMS: 1000,
			KindDelayMS:    map[string]int{},
		},
	})

	createNamedTestGroups(ctx, t, store, "web-b", "web-a")

	listed, err := store.List(ctx, ListOptions{CollectionKey: "policy.groups", ParentPath: "/infra/domains/default"})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	requireResourcePaths(t, listed, "/infra/domains/default/groups/web-a", "/infra/domains/default/groups/web-b")
	requireResourceStatuses(t, listed, realizationPending)

	fakeClock.Advance(time.Second)
	listed, err = store.List(ctx, ListOptions{CollectionKey: "policy.groups", ParentPath: "/infra/domains/default"})
	if err != nil {
		t.Fatalf("List() after delay error = %v", err)
	}
	requireResourceStatuses(t, listed, realizationSuccess)
}

func TestResourceStoreOperationLogRecordsMutationMetadataAndJSONBBodies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := NewResourceStore(db, ResourceStoreOptions{
		Clock: clock.NewFakeClock(time.Date(2026, 5, 17, 10, 30, 0, 0, time.UTC)),
	})
	spec := groupResourceSpec("web")

	if _, err := store.Mutate(ctx, Mutation{
		Spec:          spec,
		Body:          json.RawMessage(`{"display_name":"Web"}`),
		Username:      DefaultAdminUsername,
		Operation:     ResourceOperationCreate,
		RequestPath:   "/policy/api/v1/infra/domains/default/groups/web",
		RouteTemplate: "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}",
		StatusCode:    201,
	}); err != nil {
		t.Fatalf("Mutate() create error = %v", err)
	}

	requireOperationLogRow(t, readOperationLogRow(ctx, t, db, spec.Path), operationLogRow{
		username:      DefaultAdminUsername,
		method:        resourceMethodPost,
		requestPath:   "/policy/api/v1/infra/domains/default/groups/web",
		routeTemplate: "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}",
		statusCode:    201,
		resourcePath:  spec.Path,
		requestType:   sqliteStorageBlob,
		responseType:  sqliteStorageBlob,
		requestValid:  1,
		responseValid: 1,
	})
}

func TestResourceStoreMutationErrorBranchesRollbackCleanly(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := NewResourceStore(db, ResourceStoreOptions{
		Clock: clock.NewFakeClock(time.Date(2026, 5, 17, 10, 30, 0, 0, time.UTC)),
	})
	spec := groupResourceSpec("web")

	_, err := store.Mutate(ctx, Mutation{
		Spec:      spec,
		Username:  DefaultAdminUsername,
		Operation: "unsupported",
	})
	if !errors.Is(err, errUnsupportedResourceOperation) {
		t.Fatalf("unsupported mutation error = %v, want errUnsupportedResourceOperation", err)
	}

	_, err = store.Mutate(ctx, Mutation{
		Spec:            spec,
		Body:            json.RawMessage(`{"display_name":"Missing","_revision":0}`),
		Username:        DefaultAdminUsername,
		Operation:       ResourceOperationUpdate,
		EnforceRevision: true,
	})
	if !errors.Is(err, errResourceNotFound) {
		t.Fatalf("missing update error = %v, want errResourceNotFound", err)
	}

	createTestResource(ctx, t, store, spec, json.RawMessage(`{"display_name":"Web"}`))
	_, err = store.Mutate(ctx, Mutation{
		Spec:            spec,
		Body:            json.RawMessage(`{"display_name":"Missing Revision"}`),
		Username:        DefaultAdminUsername,
		Operation:       ResourceOperationUpdate,
		EnforceRevision: true,
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("missing revision update error = %v, want ErrRevisionConflict", err)
	}

	_, err = store.Mutate(ctx, Mutation{
		Spec:      groupResourceSpec("bad-json"),
		Body:      json.RawMessage(`{"display_name":`),
		Username:  DefaultAdminUsername,
		Operation: ResourceOperationCreate,
	})
	if err == nil {
		t.Fatal("invalid JSON create error = nil, want error")
	}

	managerSpec := managerIPSetResourceSpec("ipset-a")
	createTestResource(ctx, t, store, managerSpec, json.RawMessage(`{"display_name":"IPSet"}`))
	_, err = store.Mutate(ctx, Mutation{
		Spec:      managerSpec,
		Username:  DefaultAdminUsername,
		Operation: ResourceOperationDelete,
	})
	if err != nil {
		t.Fatalf("manager delete error = %v, want nil", err)
	}
	if _, found, readErr := store.Read(ctx, ReadOptions{Path: managerSpec.Path}); readErr != nil || found {
		t.Fatalf("Read() hard deleted manager resource found = %v error = %v, want found false and nil error", found, readErr)
	}

	if _, found, readErr := store.Read(ctx, ReadOptions{Path: spec.Path}); readErr != nil || !found {
		t.Fatalf("Read() after rollback found = %v error = %v, want found true and nil error", found, readErr)
	}
}

func TestResourceStoreKindDelayOverridesOperationDelay(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	fakeClock := clock.NewFakeClock(time.Date(2026, 5, 17, 10, 30, 0, 0, time.UTC))
	store := NewResourceStore(db, ResourceStoreOptions{
		Clock: fakeClock,
		Realization: config.RealizationConfig{
			CreateDelayMS: 1000,
			KindDelayMS: map[string]int{
				"Group": 3000,
			},
		},
	})
	spec := groupResourceSpec("web")

	created := createTestResource(ctx, t, store, spec, json.RawMessage(`{"display_name":"Web"}`))
	if created.RealizationStatus != realizationPending {
		t.Fatalf("created RealizationStatus = %q, want %q", created.RealizationStatus, realizationPending)
	}

	fakeClock.Advance(time.Second)
	read, found, err := store.Read(ctx, ReadOptions{Path: spec.Path})
	if err != nil {
		t.Fatalf("Read() after operation delay error = %v", err)
	}
	if !found {
		t.Fatal("Read() after operation delay found = false, want true")
	}
	if read.RealizationStatus != realizationPending {
		t.Fatalf("status after operation delay = %q, want %q", read.RealizationStatus, realizationPending)
	}

	fakeClock.Advance(2 * time.Second)
	read, found, err = store.Read(ctx, ReadOptions{Path: spec.Path})
	if err != nil {
		t.Fatalf("Read() after kind delay error = %v", err)
	}
	if !found {
		t.Fatal("Read() after kind delay found = false, want true")
	}
	if read.RealizationStatus != realizationSuccess {
		t.Fatalf("status after kind delay = %q, want %q", read.RealizationStatus, realizationSuccess)
	}
}

func TestResourceStoreEmptyBodyDefaultsAndExplicitTombstoneList(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := NewResourceStore(db, ResourceStoreOptions{
		Clock: clock.NewFakeClock(time.Date(2026, 5, 17, 10, 30, 0, 0, time.UTC)),
	})
	spec := groupResourceSpec("web")

	created := createTestResource(ctx, t, store, spec, nil)
	payload := decodeResourcePayload(t, created.Payload)
	requirePayloadString(t, payload, "display_name", "web")

	deleteTestResource(ctx, t, store, spec)
	listed, err := store.List(ctx, ListOptions{
		CollectionKey:     spec.CollectionKey,
		ParentPath:        spec.ParentPath,
		IncludeTombstones: true,
	})
	if err != nil {
		t.Fatalf("List() include tombstones error = %v", err)
	}
	requireResourcePaths(t, listed, spec.Path)
}

func tableExists(t *testing.T, db *sql.DB, table string) bool {
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

func virtualTableUsesFTS5(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()

	var createSQL string
	err := db.QueryRowContext(
		context.Background(),
		"SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?",
		table,
	).Scan(&createSQL)
	if err != nil {
		t.Fatalf("query sqlite_master SQL for %q error = %v", table, err)
	}
	return strings.Contains(strings.ToLower(createSQL), "using fts5")
}

func resourceFTSMatches(ctx context.Context, t *testing.T, db *sql.DB, path string, query string) bool {
	t.Helper()

	var count int
	err := db.QueryRowContext(
		ctx,
		`SELECT count(*)
		   FROM resource_fts
		  WHERE resource_path = ?
		    AND resource_fts MATCH ?`,
		path,
		query,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query resource_fts MATCH %q error = %v", query, err)
	}
	return count == 1
}

func searchFieldTextExists(
	ctx context.Context,
	t *testing.T,
	db *sql.DB,
	path string,
	fieldName string,
	textValue string,
) bool {
	t.Helper()

	var count int
	err := db.QueryRowContext(
		ctx,
		`SELECT count(*)
		   FROM search_fields
		  WHERE resource_path = ?
		    AND field_name = ?
		    AND text_value = ?`,
		path,
		fieldName,
		textValue,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query search_fields text %s=%q error = %v", fieldName, textValue, err)
	}
	return count > 0
}

func searchFieldBoolExists(
	ctx context.Context,
	t *testing.T,
	db *sql.DB,
	path string,
	fieldName string,
	boolValue bool,
) bool {
	t.Helper()

	want := 0
	if boolValue {
		want = 1
	}
	var count int
	err := db.QueryRowContext(
		ctx,
		`SELECT count(*)
		   FROM search_fields
		  WHERE resource_path = ?
		    AND field_name = ?
		    AND bool_value = ?`,
		path,
		fieldName,
		want,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query search_fields bool %s=%v error = %v", fieldName, boolValue, err)
	}
	return count > 0
}

func searchFieldNumberExists(
	ctx context.Context,
	t *testing.T,
	db *sql.DB,
	path string,
	fieldName string,
	numberValue float64,
) bool {
	t.Helper()

	var count int
	err := db.QueryRowContext(
		ctx,
		`SELECT count(*)
		   FROM search_fields
		  WHERE resource_path = ?
		    AND field_name = ?
		    AND number_value = ?`,
		path,
		fieldName,
		numberValue,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query search_fields number %s=%v error = %v", fieldName, numberValue, err)
	}
	return count > 0
}

func createTestResource(
	ctx context.Context,
	t *testing.T,
	store ResourceStore,
	spec ResourceSpec,
	body json.RawMessage,
) StoredResource {
	t.Helper()

	resource, err := store.Mutate(ctx, Mutation{
		Spec:          spec,
		Body:          body,
		Username:      DefaultAdminUsername,
		Operation:     ResourceOperationCreate,
		RequestPath:   "/policy/api/v1/infra/domains/default/groups/" + spec.RelativePath,
		RouteTemplate: "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}",
		StatusCode:    200,
	})
	if err != nil {
		t.Fatalf("Mutate() create %q error = %v", spec.Path, err)
	}
	return resource
}

func deleteTestResource(ctx context.Context, t *testing.T, store ResourceStore, spec ResourceSpec) StoredResource {
	t.Helper()

	resource, err := store.Mutate(ctx, Mutation{
		Spec:          spec,
		Username:      DefaultAdminUsername,
		Operation:     ResourceOperationDelete,
		RequestPath:   "/policy/api/v1/infra/domains/default/groups/" + spec.RelativePath,
		RouteTemplate: "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}",
		StatusCode:    200,
	})
	if err != nil {
		t.Fatalf("Mutate() delete %q error = %v", spec.Path, err)
	}
	return resource
}

func requireNormalReadAndListHidden(ctx context.Context, t *testing.T, store ResourceStore, spec ResourceSpec) {
	t.Helper()

	_, found, err := store.Read(ctx, ReadOptions{Path: spec.Path})
	if err != nil {
		t.Fatalf("Read() normal error = %v", err)
	}
	if found {
		t.Fatal("Read() normal found = true, want false")
	}
	listed, err := store.List(ctx, ListOptions{CollectionKey: spec.CollectionKey, ParentPath: spec.ParentPath})
	if err != nil {
		t.Fatalf("List() normal error = %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("List() normal count = %d, want 0", len(listed))
	}
}

func requireTombstoneColumns(ctx context.Context, t *testing.T, db *sql.DB, path string, wantDeletedAtMS int64) {
	t.Helper()

	var markedForDelete int
	var deletedAtMS int64
	var deletedBy string
	err := db.QueryRowContext(
		ctx,
		"SELECT marked_for_delete, deleted_at_ms, deleted_by FROM resources WHERE path = ?",
		path,
	).Scan(&markedForDelete, &deletedAtMS, &deletedBy)
	if err != nil {
		t.Fatalf("query tombstone columns error = %v", err)
	}
	if markedForDelete != 1 {
		t.Fatalf("marked_for_delete = %d, want 1", markedForDelete)
	}
	if deletedAtMS != wantDeletedAtMS {
		t.Fatalf("deleted_at_ms = %d, want %d", deletedAtMS, wantDeletedAtMS)
	}
	if deletedBy != DefaultAdminUsername {
		t.Fatalf("deleted_by = %q, want %q", deletedBy, DefaultAdminUsername)
	}
}

func createNamedTestGroups(ctx context.Context, t *testing.T, store ResourceStore, ids ...string) {
	t.Helper()

	for _, id := range ids {
		createTestResource(ctx, t, store, groupResourceSpec(id), json.RawMessage(`{"display_name":"`+id+`"}`))
	}
}

func requireResourcePaths(t *testing.T, resources []StoredResource, want ...string) {
	t.Helper()

	if len(resources) != len(want) {
		t.Fatalf("resource count = %d, want %d", len(resources), len(want))
	}
	for index := range want {
		if resources[index].Path != want[index] {
			t.Fatalf("resource[%d].Path = %q, want %q", index, resources[index].Path, want[index])
		}
	}
}

func requireResourceStatuses(t *testing.T, resources []StoredResource, want string) {
	t.Helper()

	for index, resource := range resources {
		if resource.RealizationStatus != want {
			t.Fatalf("resource[%d].RealizationStatus = %q, want %q", index, resource.RealizationStatus, want)
		}
	}
}

type operationLogRow struct {
	username      string
	method        string
	requestPath   string
	routeTemplate string
	statusCode    int
	resourcePath  string
	requestType   string
	responseType  string
	requestValid  int
	responseValid int
}

func readOperationLogRow(ctx context.Context, t *testing.T, db *sql.DB, path string) operationLogRow {
	t.Helper()

	var row operationLogRow
	err := db.QueryRowContext(
		ctx,
		`SELECT username,
		        method,
		        request_path,
		        route_template,
		        status_code,
		        resource_path,
		        typeof(request_body),
		        typeof(response_body),
		        json_valid(request_body, 8),
		        json_valid(response_body, 8)
		   FROM operation_log
		  WHERE resource_path = ?`,
		path,
	).Scan(
		&row.username,
		&row.method,
		&row.requestPath,
		&row.routeTemplate,
		&row.statusCode,
		&row.resourcePath,
		&row.requestType,
		&row.responseType,
		&row.requestValid,
		&row.responseValid,
	)
	if err != nil {
		t.Fatalf("query operation_log error = %v", err)
	}
	return row
}

func requireOperationLogRow(t *testing.T, got operationLogRow, want operationLogRow) {
	t.Helper()

	if got != want {
		t.Fatalf("operation log row = %#v, want %#v", got, want)
	}
}

func TestResourceStoreUtilityBranches(t *testing.T) {
	t.Parallel()

	for operation, want := range map[string]string{
		ResourceOperationCreate: resourceMethodPost,
		ResourceOperationUpdate: resourceMethodPut,
		ResourceOperationPatch:  resourceMethodPatch,
		ResourceOperationDelete: resourceMethodDelete,
		ResourceOperationRevise: resourceMethodPost,
		"custom":                "custom",
	} {
		if got := methodForOperation(operation); got != want {
			t.Fatalf("methodForOperation(%q) = %q, want %q", operation, got, want)
		}
	}

	var retErr error
	appendCloseError(&retErr, "close first", errResourceNotFound)
	if !errors.Is(retErr, errResourceNotFound) {
		t.Fatalf("first close retErr = %v, want errResourceNotFound", retErr)
	}
	appendCloseError(&retErr, "close second", ErrRevisionConflict)
	if !errors.Is(retErr, errResourceNotFound) || !errors.Is(retErr, ErrRevisionConflict) {
		t.Fatalf("combined close retErr = %v, want both errors", retErr)
	}
	appendCloseError(&retErr, "close nil", nil)
	if !errors.Is(retErr, errResourceNotFound) || !errors.Is(retErr, ErrRevisionConflict) {
		t.Fatalf("nil close retErr = %v, want previous errors", retErr)
	}
}

func openResourceStoreTestDB(t *testing.T) *sql.DB {
	t.Helper()

	ctx := context.Background()
	db := openTestDB(t)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close() error = %v", err)
		}
	})
	if _, err := NewUserStore(db).EnsureBootstrap(ctx); err != nil {
		t.Fatalf("EnsureBootstrap() users error = %v", err)
	}
	if err := NewResourceStore(db, ResourceStoreOptions{}).EnsureBootstrap(ctx); err != nil {
		t.Fatalf("EnsureBootstrap() resources error = %v", err)
	}
	return db
}

func groupResourceSpec(id string) ResourceSpec {
	return ResourceSpec{
		APIFamily:     ResourceAPIFamilyPolicy,
		CollectionKey: "policy.groups",
		Kind:          "Group",
		ResourceType:  "Group",
		Path:          "/infra/domains/default/groups/" + id,
		ParentPath:    "/infra/domains/default",
		RelativePath:  id,
	}
}

func managerIPSetResourceSpec(id string) ResourceSpec {
	return ResourceSpec{
		APIFamily:     ResourceAPIFamilyManager,
		CollectionKey: "manager.ip-sets",
		Kind:          "IPSet",
		ResourceType:  "IPSet",
		Path:          "/api/v1/ip-sets/" + id,
		ParentPath:    "/api/v1/ip-sets",
		RelativePath:  id,
	}
}

func decodeResourcePayload(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	return payload
}

func requirePayloadString(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()

	got, ok := payload[key].(string)
	if !ok {
		t.Fatalf("payload[%q] = %#v, want string %q", key, payload[key], want)
	}
	if got != want {
		t.Fatalf("payload[%q] = %q, want %q", key, got, want)
	}
}

func requirePayloadNumber(t *testing.T, payload map[string]any, key string, want float64) {
	t.Helper()

	got, ok := payload[key].(float64)
	if !ok {
		t.Fatalf("payload[%q] = %#v, want number %.0f", key, payload[key], want)
	}
	if got != want {
		t.Fatalf("payload[%q] = %.0f, want %.0f", key, got, want)
	}
}

func requirePayloadBool(t *testing.T, payload map[string]any, key string, want bool) {
	t.Helper()

	got, ok := payload[key].(bool)
	if !ok {
		t.Fatalf("payload[%q] = %#v, want bool %v", key, payload[key], want)
	}
	if got != want {
		t.Fatalf("payload[%q] = %v, want %v", key, got, want)
	}
}
