package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"nsx-t-mockapi/internal/clock"
	"nsx-t-mockapi/internal/config"

	"go.uber.org/zap"
)

const resourceBootstrapMigrationVersion = "003_create_resource_search_index"

var (
	// ErrRevisionConflict reports a missing or stale NSX resource revision.
	ErrRevisionConflict = errors.New("resource revision conflict")

	errUnsupportedResourceOperation = errors.New("unsupported resource operation")
	errResourceNotFound             = errors.New("resource not found")
	errUnexpectedRowsAffected       = errors.New("unexpected rows affected")
	errCanonicalPayloadInvalid      = errors.New("canonical payload is invalid")
)

const (
	// ResourceAPIFamilyPolicy identifies NSX Policy resources.
	ResourceAPIFamilyPolicy = "policy"
	// ResourceAPIFamilyManager identifies NSX Manager resources.
	ResourceAPIFamilyManager = "manager"
)

const (
	// ResourceOperationCreate records a resource creation.
	ResourceOperationCreate = "create"
	// ResourceOperationUpdate records a resource update.
	ResourceOperationUpdate = "update"
	// ResourceOperationPatch records a resource patch.
	ResourceOperationPatch = "patch"
	// ResourceOperationDelete records a resource deletion.
	ResourceOperationDelete = "delete"
	// ResourceOperationRevise records an ordered revision operation.
	ResourceOperationRevise = "revise"
)

const (
	protectionNotProtected = "NOT_PROTECTED"
	realizationDefaultEP   = "default"
	realizationPending     = "IN_PROGRESS"
	realizationSuccess     = "SUCCESS"
	resourceMethodPost     = "POST"
	resourceMethodPut      = "PUT"
	resourceMethodPatch    = "PATCH"
	resourceMethodDelete   = "DELETE"
)

const (
	createResourcesSQL = `
CREATE TABLE IF NOT EXISTS resources (
	path TEXT PRIMARY KEY,
	api_family TEXT NOT NULL CHECK (api_family IN ('manager', 'policy')),
	collection_key TEXT NOT NULL,
	kind TEXT NOT NULL,
	id TEXT NOT NULL,
	parent_path TEXT,
	relative_path TEXT NOT NULL,
	display_name TEXT NOT NULL,
	resource_type TEXT,
	revision INTEGER NOT NULL DEFAULT 0,
	payload BLOB NOT NULL CHECK (json_valid(payload, 8)),
	accepted_at_ms INTEGER NOT NULL,
	config_valid_from_ms INTEGER NOT NULL,
	create_user TEXT NOT NULL,
	create_time_ms INTEGER NOT NULL,
	last_modified_user TEXT NOT NULL,
	last_modified_time_ms INTEGER NOT NULL,
	system_owned INTEGER NOT NULL DEFAULT 0 CHECK (system_owned IN (0, 1)),
	protection TEXT NOT NULL DEFAULT 'NOT_PROTECTED',
	marked_for_delete INTEGER NOT NULL DEFAULT 0 CHECK (marked_for_delete IN (0, 1)),
	deleted_at_ms INTEGER,
	deleted_by TEXT,
	FOREIGN KEY (create_user) REFERENCES users(username),
	FOREIGN KEY (last_modified_user) REFERENCES users(username),
	FOREIGN KEY (deleted_by) REFERENCES users(username)
)`

	createResourceRealizationSQL = `
CREATE TABLE IF NOT EXISTS resource_realization (
	resource_path TEXT NOT NULL,
	enforcement_point_id TEXT NOT NULL DEFAULT 'default',
	operation TEXT NOT NULL CHECK (operation IN ('create', 'update', 'patch', 'delete', 'revise')),
	accepted_at_ms INTEGER NOT NULL,
	valid_from_ms INTEGER NOT NULL,
	desired_status TEXT NOT NULL DEFAULT 'SUCCESS',
	pending_status TEXT NOT NULL DEFAULT 'IN_PROGRESS',
	error_message TEXT,
	last_updated_ms INTEGER NOT NULL,
	PRIMARY KEY (resource_path, enforcement_point_id),
	FOREIGN KEY (resource_path) REFERENCES resources(path) ON DELETE CASCADE
)`

	createResourceEdgesSQL = `
CREATE TABLE IF NOT EXISTS resource_edges (
	parent_path TEXT NOT NULL,
	child_path TEXT NOT NULL,
	relationship TEXT NOT NULL DEFAULT 'contains',
	created_at_ms INTEGER NOT NULL,
	PRIMARY KEY (parent_path, child_path, relationship),
	FOREIGN KEY (parent_path) REFERENCES resources(path) ON DELETE CASCADE,
	FOREIGN KEY (child_path) REFERENCES resources(path) ON DELETE CASCADE
)`

	createOperationLogSQL = `
CREATE TABLE IF NOT EXISTS operation_log (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	at_ms INTEGER NOT NULL,
	username TEXT NOT NULL,
	method TEXT NOT NULL,
	request_path TEXT NOT NULL,
	route_template TEXT,
	action TEXT,
	status_code INTEGER NOT NULL,
	resource_path TEXT,
	request_body BLOB CHECK (request_body IS NULL OR json_valid(request_body, 8)),
	response_body BLOB CHECK (response_body IS NULL OR json_valid(response_body, 8)),
	error TEXT,
	FOREIGN KEY (username) REFERENCES users(username)
)`

	createResourcesLiveCollectionParentIndexSQL = `
CREATE INDEX IF NOT EXISTS resources_live_collection_parent_idx
ON resources(collection_key, parent_path, marked_for_delete, display_name, id)`

	createResourcesLiveKindParentIndexSQL = `
CREATE INDEX IF NOT EXISTS resources_live_kind_parent_idx
ON resources(kind, parent_path, marked_for_delete, display_name, id)`

	createResourcesIDIndexSQL = `
CREATE INDEX IF NOT EXISTS resources_id_idx
ON resources(collection_key, id, marked_for_delete)`

	createResourcesParentIndexSQL = `
CREATE INDEX IF NOT EXISTS resources_parent_idx
ON resources(parent_path, marked_for_delete)`

	createResourceRealizationStatusIndexSQL = `
CREATE INDEX IF NOT EXISTS resource_realization_status_idx
ON resource_realization(resource_path, valid_from_ms, desired_status, pending_status)`

	createOperationLogAtIndexSQL = `
CREATE INDEX IF NOT EXISTS operation_log_at_idx
ON operation_log(at_ms)`

	insertResourceSQL = `
INSERT INTO resources (
	path,
	api_family,
	collection_key,
	kind,
	id,
	parent_path,
	relative_path,
	display_name,
	resource_type,
	revision,
	payload,
	accepted_at_ms,
	config_valid_from_ms,
	create_user,
	create_time_ms,
	last_modified_user,
	last_modified_time_ms,
	system_owned,
	protection,
	marked_for_delete
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, jsonb(?), ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	selectLiveResourceForMutationSQL = `
SELECT json(payload), revision, create_user, create_time_ms
FROM resources
WHERE path = ?
  AND marked_for_delete = 0`

	readResourceSQL = `
SELECT r.api_family,
       json(r.payload),
       r.revision,
       rr.valid_from_ms,
       rr.desired_status,
       rr.pending_status
FROM resources AS r
LEFT JOIN resource_realization AS rr
  ON rr.resource_path = r.path
 AND rr.enforcement_point_id = 'default'
WHERE r.path = ?
  AND (? = 1 OR r.marked_for_delete = 0)`

	listResourcesSQL = `
SELECT r.path,
       r.api_family,
       json(r.payload),
       r.revision,
       rr.valid_from_ms,
       rr.desired_status,
       rr.pending_status
FROM resources AS r
LEFT JOIN resource_realization AS rr
  ON rr.resource_path = r.path
 AND rr.enforcement_point_id = 'default'
WHERE r.collection_key = ?
  AND ((? IS NULL AND r.parent_path IS NULL) OR r.parent_path = ?)
  AND (? = 1 OR r.marked_for_delete = 0)
ORDER BY r.display_name COLLATE NOCASE ASC, r.id COLLATE NOCASE ASC`

	updateResourceSQL = `
UPDATE resources
SET display_name = ?,
	resource_type = ?,
	revision = ?,
	payload = jsonb(?),
	accepted_at_ms = ?,
	config_valid_from_ms = ?,
	last_modified_user = ?,
	last_modified_time_ms = ?,
	system_owned = ?,
	protection = ?,
	marked_for_delete = ?,
	deleted_at_ms = NULL,
	deleted_by = NULL
WHERE path = ?`

	tombstonePolicyResourceSQL = `
UPDATE resources
SET revision = ?,
	payload = jsonb(?),
	accepted_at_ms = ?,
	config_valid_from_ms = ?,
	last_modified_user = ?,
	last_modified_time_ms = ?,
	marked_for_delete = ?,
	deleted_at_ms = ?,
	deleted_by = ?
WHERE path = ?`

	hardDeleteManagerResourceSQL = `
DELETE FROM resources
WHERE path = ?
  AND api_family = 'manager'
  AND marked_for_delete = 0`

	upsertResourceRealizationSQL = `
INSERT INTO resource_realization (
	resource_path,
	enforcement_point_id,
	operation,
	accepted_at_ms,
	valid_from_ms,
	desired_status,
	pending_status,
	last_updated_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(resource_path, enforcement_point_id) DO UPDATE SET
	operation = excluded.operation,
	accepted_at_ms = excluded.accepted_at_ms,
	valid_from_ms = excluded.valid_from_ms,
	desired_status = excluded.desired_status,
	pending_status = excluded.pending_status,
	error_message = NULL,
	last_updated_ms = excluded.last_updated_ms`

	insertOperationLogWithBodiesSQL = `
INSERT INTO operation_log (
	at_ms,
	username,
	method,
	request_path,
	route_template,
	action,
	status_code,
	resource_path,
	request_body,
	response_body
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, jsonb(?), jsonb(?))`
)

// ResourceStoreOptions configures resource persistence behavior.
type ResourceStoreOptions struct {
	Clock       clock.Clock
	Realization config.RealizationConfig
	Logger      *zap.Logger
}

// ResourceStore owns canonical NSX resource persistence in SQLite.
type ResourceStore struct {
	db          *sql.DB
	clock       clock.Clock
	realization config.RealizationConfig
	logger      *zap.Logger
}

// ResourceSpec describes route-derived resource identity.
type ResourceSpec struct {
	APIFamily     string
	CollectionKey string
	Kind          string
	ResourceType  string
	Path          string
	ParentPath    string
	RelativePath  string
}

// Mutation describes one accepted resource write.
type Mutation struct {
	Spec            ResourceSpec
	Body            json.RawMessage
	Username        string
	Operation       string
	EnforceRevision bool
	RequestPath     string
	RouteTemplate   string
	Action          string
	StatusCode      int
}

// StoredResource is a canonical resource read from the store boundary.
type StoredResource struct {
	Path              string
	APIFamily         string
	Revision          int
	Payload           json.RawMessage
	RealizationStatus string
}

// ReadOptions configures a single-resource read.
type ReadOptions struct {
	Path              string
	IncludeTombstones bool
}

// ListOptions configures collection reads.
type ListOptions struct {
	CollectionKey     string
	ParentPath        string
	IncludeTombstones bool
}

type existingResource struct {
	payload      json.RawMessage
	revision     int
	createUser   string
	createTimeMS int64
}

// NewResourceStore creates a resource persistence boundary over db.
func NewResourceStore(db *sql.DB, opts ResourceStoreOptions) ResourceStore {
	storeClock := opts.Clock
	if storeClock == nil {
		storeClock = clock.SystemClock{}
	}
	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}

	return ResourceStore{
		db:          db,
		clock:       storeClock,
		realization: opts.Realization,
		logger:      logger,
	}
}

// EnsureBootstrap creates the resource-store schema and indexes.
func (s ResourceStore) EnsureBootstrap(ctx context.Context) (retErr error) {
	s.logger.Info("starting resource store bootstrap")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin resource bootstrap transaction: %w", err)
	}
	defer func() {
		if retErr == nil {
			return
		}
		rollbackErr := tx.Rollback()
		if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			retErr = fmt.Errorf("%w; rollback resource bootstrap transaction: %w", retErr, rollbackErr)
		}
	}()

	if err = ensureResourceSchema(ctx, tx); err != nil {
		return err
	}
	if err = ensureSearchIndexSchema(ctx, tx); err != nil {
		return err
	}

	result, err := execPrepared(ctx, tx, insertSchemaMigrationSQL, resourceBootstrapMigrationVersion)
	if err != nil {
		return fmt.Errorf("record resource schema migration: %w", err)
	}
	if _, err = result.RowsAffected(); err != nil {
		return fmt.Errorf("read resource schema migration rows affected: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit resource bootstrap transaction: %w", err)
	}

	s.logger.Debug("resource store bootstrap completed")
	return nil
}

// Mutate creates, updates, patches, revises, or deletes one resource transactionally.
func (s ResourceStore) Mutate(ctx context.Context, mutation Mutation) (resource StoredResource, retErr error) {
	s.logger.Info(
		"starting resource mutation",
		zap.String("operation", mutation.Operation),
		zap.String("path", mutation.Spec.Path),
		zap.String("username", mutation.Username),
	)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StoredResource{}, fmt.Errorf("begin resource mutation transaction: %w", err)
	}
	defer rollbackTxOnError(tx, &retErr, "resource mutation")

	now := s.clock.Now()
	nowMS := unixMillis(now)
	delayMS := s.realizationDelayMS(mutation.Operation, mutation.Spec.Kind)
	validFromMS := nowMS + int64(delayMS)

	resource, err = s.applyMutation(ctx, tx, mutation, now, nowMS, validFromMS)
	if err != nil {
		return StoredResource{}, err
	}
	if err = s.maintainSearchIndex(ctx, tx, mutation, resource); err != nil {
		return StoredResource{}, err
	}

	if mutation.Spec.APIFamily != ResourceAPIFamilyManager || mutation.Operation != ResourceOperationDelete {
		if err = s.writeRealization(ctx, tx, mutation, nowMS, validFromMS); err != nil {
			return StoredResource{}, err
		}
	}
	if err = s.writeOperationLog(ctx, tx, mutation, nowMS, resource.Payload); err != nil {
		return StoredResource{}, err
	}

	if err = tx.Commit(); err != nil {
		return StoredResource{}, fmt.Errorf("commit resource mutation transaction: %w", err)
	}

	s.logger.Debug(
		"resource mutation completed",
		zap.String("operation", mutation.Operation),
		zap.String("path", resource.Path),
		zap.Int("revision", resource.Revision),
		zap.Int64("valid_from_ms", validFromMS),
	)
	return resource, nil
}

// Read returns one canonical resource payload and joined realization status.
func (s ResourceStore) Read(ctx context.Context, opts ReadOptions) (resource StoredResource, found bool, retErr error) {
	s.logger.Debug(
		"reading resource",
		zap.String("path", opts.Path),
		zap.Bool("include_tombstones", opts.IncludeTombstones),
	)
	stmt, err := s.db.PrepareContext(ctx, readResourceSQL)
	if err != nil {
		return StoredResource{}, false, fmt.Errorf("prepare read resource: %w", err)
	}
	defer closeStatement(&retErr, stmt, "read resource")

	includeTombstones := 0
	if opts.IncludeTombstones {
		includeTombstones = 1
	}
	var payloadText string
	var validFrom sql.NullInt64
	var desiredStatus sql.NullString
	var pendingStatus sql.NullString
	err = stmt.QueryRowContext(ctx, opts.Path, includeTombstones).Scan(
		&resource.APIFamily,
		&payloadText,
		&resource.Revision,
		&validFrom,
		&desiredStatus,
		&pendingStatus,
	)
	if errors.Is(err, sql.ErrNoRows) {
		s.logger.Debug("resource not found", zap.String("path", opts.Path))
		return StoredResource{}, false, nil
	}
	if err != nil {
		return StoredResource{}, false, fmt.Errorf("query resource %q: %w", opts.Path, err)
	}

	status := realizationSuccess
	if validFrom.Valid {
		status = realizationJoinedStatus(s.clock.Now(), validFrom.Int64, desiredStatus, pendingStatus)
	}
	resource.Path = opts.Path
	resource.Payload = json.RawMessage(payloadText)
	resource.RealizationStatus = status
	s.logger.Debug(
		"read resource completed",
		zap.String("path", opts.Path),
		zap.Int("revision", resource.Revision),
		zap.String("realization_status", resource.RealizationStatus),
	)
	return resource, true, nil
}

// List returns canonical collection payloads and joined realization status.
func (s ResourceStore) List(ctx context.Context, opts ListOptions) (resources []StoredResource, retErr error) {
	s.logger.Debug(
		"listing resources",
		zap.String("collection_key", opts.CollectionKey),
		zap.String("parent_path", opts.ParentPath),
		zap.Bool("include_tombstones", opts.IncludeTombstones),
	)
	stmt, err := s.db.PrepareContext(ctx, listResourcesSQL)
	if err != nil {
		return nil, fmt.Errorf("prepare list resources: %w", err)
	}
	defer closeStatement(&retErr, stmt, "list resources")

	includeTombstones := 0
	if opts.IncludeTombstones {
		includeTombstones = 1
	}
	parentPath := nullableString(opts.ParentPath)
	rows, err := stmt.QueryContext(ctx, opts.CollectionKey, parentPath, parentPath, includeTombstones)
	if err != nil {
		return nil, fmt.Errorf("query resources collection %q: %w", opts.CollectionKey, err)
	}
	defer closeRows(&retErr, rows, "resource list")

	resources, err = s.scanListedResources(rows)
	if err != nil {
		return nil, err
	}

	s.logger.Debug(
		"list resources completed",
		zap.String("collection_key", opts.CollectionKey),
		zap.String("parent_path", opts.ParentPath),
		zap.Int("resource_count", len(resources)),
	)
	return resources, nil
}

func (s ResourceStore) applyMutation(
	ctx context.Context,
	tx *sql.Tx,
	mutation Mutation,
	now time.Time,
	nowMS int64,
	validFromMS int64,
) (StoredResource, error) {
	switch mutation.Operation {
	case ResourceOperationCreate:
		return s.createResource(ctx, tx, mutation, now, nowMS, validFromMS)
	case ResourceOperationUpdate:
		return s.updateResource(ctx, tx, mutation, now, nowMS, validFromMS)
	case ResourceOperationPatch:
		return s.updateResource(ctx, tx, mutation, now, nowMS, validFromMS)
	case ResourceOperationRevise:
		return s.updateResource(ctx, tx, mutation, now, nowMS, validFromMS)
	case ResourceOperationDelete:
		return s.deleteResource(ctx, tx, mutation, now, nowMS, validFromMS)
	default:
		return StoredResource{}, fmt.Errorf("%w: %q", errUnsupportedResourceOperation, mutation.Operation)
	}
}

func ensureResourceSchema(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []struct {
		name string
		sql  string
	}{
		{name: "resources", sql: createResourcesSQL},
		{name: "resource_realization", sql: createResourceRealizationSQL},
		{name: "resource_edges", sql: createResourceEdgesSQL},
		{name: "operation_log", sql: createOperationLogSQL},
		{name: "resources_live_collection_parent_idx", sql: createResourcesLiveCollectionParentIndexSQL},
		{name: "resources_live_kind_parent_idx", sql: createResourcesLiveKindParentIndexSQL},
		{name: "resources_id_idx", sql: createResourcesIDIndexSQL},
		{name: "resources_parent_idx", sql: createResourcesParentIndexSQL},
		{name: "resource_realization_status_idx", sql: createResourceRealizationStatusIndexSQL},
		{name: "operation_log_at_idx", sql: createOperationLogAtIndexSQL},
	} {
		if _, err := tx.ExecContext(ctx, statement.sql); err != nil {
			return fmt.Errorf("create %s resource schema object: %w", statement.name, err)
		}
	}
	return nil
}

func (s ResourceStore) scanListedResources(rows *sql.Rows) ([]StoredResource, error) {
	resources := []StoredResource{}
	for rows.Next() {
		resource, err := s.scanListedResource(rows)
		if err != nil {
			return nil, err
		}
		resources = append(resources, resource)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate resource list rows: %w", err)
	}

	return resources, nil
}

func (s ResourceStore) scanListedResource(rows *sql.Rows) (StoredResource, error) {
	var resource StoredResource
	var payloadText string
	var validFrom sql.NullInt64
	var desiredStatus sql.NullString
	var pendingStatus sql.NullString
	if err := rows.Scan(
		&resource.Path,
		&resource.APIFamily,
		&payloadText,
		&resource.Revision,
		&validFrom,
		&desiredStatus,
		&pendingStatus,
	); err != nil {
		return StoredResource{}, fmt.Errorf("scan resource list row: %w", err)
	}
	resource.Payload = json.RawMessage(payloadText)
	resource.RealizationStatus = realizationSuccess
	if validFrom.Valid {
		resource.RealizationStatus = realizationJoinedStatus(s.clock.Now(), validFrom.Int64, desiredStatus, pendingStatus)
	}
	return resource, nil
}

func (s ResourceStore) createResource(
	ctx context.Context,
	tx *sql.Tx,
	mutation Mutation,
	now time.Time,
	nowMS int64,
	validFromMS int64,
) (StoredResource, error) {
	payload, err := canonicalCreatePayload(mutation.Spec, mutation.Body, mutation.Username, nowMS)
	if err != nil {
		return StoredResource{}, err
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return StoredResource{}, fmt.Errorf("marshal canonical create payload: %w", err)
	}
	displayName, err := payloadString(payload, "display_name")
	if err != nil {
		return StoredResource{}, err
	}

	result, err := execPrepared(
		ctx,
		tx,
		insertResourceSQL,
		mutation.Spec.Path,
		mutation.Spec.APIFamily,
		mutation.Spec.CollectionKey,
		mutation.Spec.Kind,
		mutation.Spec.RelativePath,
		nullableString(mutation.Spec.ParentPath),
		mutation.Spec.RelativePath,
		displayName,
		mutation.Spec.ResourceType,
		0,
		string(payloadJSON),
		nowMS,
		validFromMS,
		mutation.Username,
		nowMS,
		mutation.Username,
		nowMS,
		0,
		protectionNotProtected,
		0,
	)
	if err != nil {
		return StoredResource{}, fmt.Errorf("insert resource %q: %w", mutation.Spec.Path, err)
	}
	if _, err = result.RowsAffected(); err != nil {
		return StoredResource{}, fmt.Errorf("read inserted resource rows affected: %w", err)
	}

	return StoredResource{
		Path:              mutation.Spec.Path,
		APIFamily:         mutation.Spec.APIFamily,
		Revision:          0,
		Payload:           json.RawMessage(payloadJSON),
		RealizationStatus: realizationStatus(now, validFromMS),
	}, nil
}

func (s ResourceStore) updateResource(
	ctx context.Context,
	tx *sql.Tx,
	mutation Mutation,
	now time.Time,
	nowMS int64,
	validFromMS int64,
) (StoredResource, error) {
	existing, found, err := findLiveResourceForMutation(ctx, tx, mutation.Spec.Path)
	if err != nil {
		return StoredResource{}, err
	}
	if !found {
		return StoredResource{}, fmt.Errorf("%w: %q", errResourceNotFound, mutation.Spec.Path)
	}

	requestPayload, err := decodeJSONObject(mutation.Body)
	if err != nil {
		return StoredResource{}, err
	}
	if err = enforceRequestedRevision(mutation, requestPayload, existing.revision); err != nil {
		return StoredResource{}, err
	}

	payload := canonicalUpdatePayload(mutation.Spec, requestPayload, mutation.Username, nowMS, existing)
	nextRevision := existing.revision + 1
	payload["_revision"] = nextRevision
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return StoredResource{}, fmt.Errorf("marshal canonical update payload: %w", err)
	}
	displayName, err := payloadString(payload, "display_name")
	if err != nil {
		return StoredResource{}, err
	}

	result, err := execPrepared(
		ctx,
		tx,
		updateResourceSQL,
		displayName,
		mutation.Spec.ResourceType,
		nextRevision,
		string(payloadJSON),
		nowMS,
		validFromMS,
		mutation.Username,
		nowMS,
		0,
		protectionNotProtected,
		0,
		mutation.Spec.Path,
	)
	if err != nil {
		return StoredResource{}, fmt.Errorf("update resource %q: %w", mutation.Spec.Path, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return StoredResource{}, fmt.Errorf("read updated resource rows affected: %w", err)
	}
	if err = requireOneRowAffected(rowsAffected, "update resource", mutation.Spec.Path); err != nil {
		return StoredResource{}, err
	}

	return StoredResource{
		Path:              mutation.Spec.Path,
		APIFamily:         mutation.Spec.APIFamily,
		Revision:          nextRevision,
		Payload:           json.RawMessage(payloadJSON),
		RealizationStatus: realizationStatus(now, validFromMS),
	}, nil
}

func (s ResourceStore) deleteResource(
	ctx context.Context,
	tx *sql.Tx,
	mutation Mutation,
	now time.Time,
	nowMS int64,
	validFromMS int64,
) (StoredResource, error) {
	existing, found, err := findLiveResourceForMutation(ctx, tx, mutation.Spec.Path)
	if err != nil {
		return StoredResource{}, err
	}
	if !found {
		return StoredResource{}, fmt.Errorf("%w: %q", errResourceNotFound, mutation.Spec.Path)
	}
	if mutation.Spec.APIFamily != ResourceAPIFamilyPolicy {
		return s.hardDeleteManagerResource(ctx, tx, mutation, existing, now, validFromMS)
	}

	nextRevision := existing.revision + 1
	payload, err := canonicalDeletePayload(existing.payload, mutation.Username, nowMS, nextRevision)
	if err != nil {
		return StoredResource{}, err
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return StoredResource{}, fmt.Errorf("marshal canonical delete payload: %w", err)
	}

	result, err := execPrepared(
		ctx,
		tx,
		tombstonePolicyResourceSQL,
		nextRevision,
		string(payloadJSON),
		nowMS,
		validFromMS,
		mutation.Username,
		nowMS,
		1,
		nowMS,
		mutation.Username,
		mutation.Spec.Path,
	)
	if err != nil {
		return StoredResource{}, fmt.Errorf("tombstone resource %q: %w", mutation.Spec.Path, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return StoredResource{}, fmt.Errorf("read tombstoned resource rows affected: %w", err)
	}
	if err = requireOneRowAffected(rowsAffected, "tombstone resource", mutation.Spec.Path); err != nil {
		return StoredResource{}, err
	}

	return StoredResource{
		Path:              mutation.Spec.Path,
		APIFamily:         mutation.Spec.APIFamily,
		Revision:          nextRevision,
		Payload:           json.RawMessage(payloadJSON),
		RealizationStatus: realizationStatus(now, validFromMS),
	}, nil
}

func (s ResourceStore) hardDeleteManagerResource(
	ctx context.Context,
	tx *sql.Tx,
	mutation Mutation,
	existing existingResource,
	now time.Time,
	validFromMS int64,
) (StoredResource, error) {
	result, err := execPrepared(ctx, tx, hardDeleteManagerResourceSQL, mutation.Spec.Path)
	if err != nil {
		return StoredResource{}, fmt.Errorf("hard delete resource %q: %w", mutation.Spec.Path, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return StoredResource{}, fmt.Errorf("read hard deleted resource rows affected: %w", err)
	}
	if err = requireOneRowAffected(rowsAffected, "hard delete resource", mutation.Spec.Path); err != nil {
		return StoredResource{}, err
	}
	return StoredResource{
		Path:              mutation.Spec.Path,
		APIFamily:         mutation.Spec.APIFamily,
		Revision:          existing.revision,
		Payload:           existing.payload,
		RealizationStatus: realizationStatus(now, validFromMS),
	}, nil
}

func canonicalDeletePayload(
	raw json.RawMessage,
	username string,
	nowMS int64,
	revision int,
) (map[string]any, error) {
	payload, err := decodeJSONObject(raw)
	if err != nil {
		return nil, err
	}
	payload["_last_modified_user"] = username
	payload["_last_modified_time"] = nowMS
	payload["_revision"] = revision
	payload["marked_for_delete"] = true
	return payload, nil
}

func (s ResourceStore) writeRealization(
	ctx context.Context,
	tx *sql.Tx,
	mutation Mutation,
	nowMS int64,
	validFromMS int64,
) error {
	result, err := execPrepared(
		ctx,
		tx,
		upsertResourceRealizationSQL,
		mutation.Spec.Path,
		realizationDefaultEP,
		mutation.Operation,
		nowMS,
		validFromMS,
		realizationSuccess,
		realizationPending,
		nowMS,
	)
	if err != nil {
		return fmt.Errorf("write resource realization for %q: %w", mutation.Spec.Path, err)
	}
	if _, err = result.RowsAffected(); err != nil {
		return fmt.Errorf("read resource realization rows affected: %w", err)
	}
	return nil
}

func (s ResourceStore) writeOperationLog(
	ctx context.Context,
	tx *sql.Tx,
	mutation Mutation,
	nowMS int64,
	response json.RawMessage,
) error {
	statusCode := mutation.StatusCode
	if statusCode == 0 {
		statusCode = 200
	}
	requestBody := mutation.Body
	if len(requestBody) == 0 {
		requestBody = json.RawMessage(`{}`)
	}

	result, err := execPrepared(
		ctx,
		tx,
		insertOperationLogWithBodiesSQL,
		nowMS,
		mutation.Username,
		methodForOperation(mutation.Operation),
		mutation.RequestPath,
		nullableString(mutation.RouteTemplate),
		nullableString(mutation.Action),
		statusCode,
		mutation.Spec.Path,
		string(requestBody),
		string(response),
	)
	if err != nil {
		return fmt.Errorf("write operation log for %q: %w", mutation.Spec.Path, err)
	}
	if _, err = result.RowsAffected(); err != nil {
		return fmt.Errorf("read operation log rows affected: %w", err)
	}
	return nil
}

func canonicalCreatePayload(
	spec ResourceSpec,
	body json.RawMessage,
	username string,
	nowMS int64,
) (map[string]any, error) {
	payload, err := decodeJSONObject(body)
	if err != nil {
		return nil, err
	}
	removeReadOnlyMetadata(payload)

	payload["id"] = spec.RelativePath
	displayName, ok := payload["display_name"].(string)
	if !ok || displayName == "" {
		payload["display_name"] = spec.RelativePath
	}
	payload["resource_type"] = spec.ResourceType
	payload["path"] = spec.Path
	payload["parent_path"] = spec.ParentPath
	payload["relative_path"] = spec.RelativePath
	payload["_create_user"] = username
	payload["_create_time"] = nowMS
	payload["_last_modified_user"] = username
	payload["_last_modified_time"] = nowMS
	payload["_system_owned"] = false
	payload["_protection"] = protectionNotProtected
	payload["_revision"] = 0
	if spec.APIFamily == ResourceAPIFamilyPolicy {
		payload["marked_for_delete"] = false
	}

	return payload, nil
}

func canonicalUpdatePayload(
	spec ResourceSpec,
	payload map[string]any,
	username string,
	nowMS int64,
	existing existingResource,
) map[string]any {
	removeReadOnlyMetadata(payload)

	payload["id"] = spec.RelativePath
	displayName, ok := payload["display_name"].(string)
	if !ok || displayName == "" {
		payload["display_name"] = spec.RelativePath
	}
	payload["resource_type"] = spec.ResourceType
	payload["path"] = spec.Path
	payload["parent_path"] = spec.ParentPath
	payload["relative_path"] = spec.RelativePath
	payload["_create_user"] = existing.createUser
	payload["_create_time"] = existing.createTimeMS
	payload["_last_modified_user"] = username
	payload["_last_modified_time"] = nowMS
	payload["_system_owned"] = false
	payload["_protection"] = protectionNotProtected
	if spec.APIFamily == ResourceAPIFamilyPolicy {
		payload["marked_for_delete"] = false
	}

	return payload
}

func enforceRequestedRevision(mutation Mutation, requestPayload map[string]any, currentRevision int) error {
	if !mutation.EnforceRevision {
		return nil
	}
	requestRevision, ok := requestedRevision(requestPayload)
	if ok && requestRevision == currentRevision {
		return nil
	}
	return fmt.Errorf(
		"%w: path %q got %v want %d",
		ErrRevisionConflict,
		mutation.Spec.Path,
		requestPayload["_revision"],
		currentRevision,
	)
}

func requireOneRowAffected(rowsAffected int64, action string, path string) error {
	if rowsAffected == 1 {
		return nil
	}
	return fmt.Errorf("%w: %s %q affected %d rows, want 1", errUnexpectedRowsAffected, action, path, rowsAffected)
}

func requestedRevision(payload map[string]any) (int, bool) {
	raw, ok := payload["_revision"]
	if !ok {
		return 0, false
	}
	value, ok := raw.(float64)
	if !ok {
		return 0, false
	}
	revision := int(value)
	if float64(revision) != value {
		return 0, false
	}
	return revision, true
}

func findLiveResourceForMutation(
	ctx context.Context,
	tx *sql.Tx,
	path string,
) (found existingResource, exists bool, retErr error) {
	stmt, err := tx.PrepareContext(ctx, selectLiveResourceForMutationSQL)
	if err != nil {
		return existingResource{}, false, fmt.Errorf("prepare find live resource: %w", err)
	}
	defer closeStatement(&retErr, stmt, "find live resource")

	var payloadText string
	err = stmt.QueryRowContext(ctx, path).Scan(
		&payloadText,
		&found.revision,
		&found.createUser,
		&found.createTimeMS,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return existingResource{}, false, nil
	}
	if err != nil {
		return existingResource{}, false, fmt.Errorf("query resource %q for mutation: %w", path, err)
	}
	found.payload = json.RawMessage(payloadText)
	return found, true, nil
}

func decodeJSONObject(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode resource body: %w", err)
	}
	if payload == nil {
		return map[string]any{}, nil
	}
	return payload, nil
}

func removeReadOnlyMetadata(payload map[string]any) {
	for _, key := range []string{
		"_create_user",
		"_create_time",
		"_last_modified_user",
		"_last_modified_time",
		"_system_owned",
		"_protection",
		"_revision",
		"path",
		"parent_path",
		"relative_path",
		"marked_for_delete",
	} {
		delete(payload, key)
	}
}

func payloadString(payload map[string]any, key string) (string, error) {
	value, ok := payload[key].(string)
	if !ok || value == "" {
		return "", fmt.Errorf("%w: field %q is not a non-empty string", errCanonicalPayloadInvalid, key)
	}
	return value, nil
}

func (s ResourceStore) realizationDelayMS(operation string, kind string) int {
	if delay, ok := s.realization.KindDelayMS[kind]; ok {
		return delay
	}
	switch operation {
	case ResourceOperationCreate:
		if s.realization.CreateDelayMS != 0 {
			return s.realization.CreateDelayMS
		}
	case ResourceOperationUpdate, ResourceOperationPatch, ResourceOperationRevise:
		if s.realization.UpdateDelayMS != 0 {
			return s.realization.UpdateDelayMS
		}
	case ResourceOperationDelete:
		if s.realization.DeleteDelayMS != 0 {
			return s.realization.DeleteDelayMS
		}
	}
	return s.realization.DefaultDelayMS
}

func methodForOperation(operation string) string {
	switch operation {
	case ResourceOperationCreate:
		return resourceMethodPost
	case ResourceOperationUpdate:
		return resourceMethodPut
	case ResourceOperationPatch:
		return resourceMethodPatch
	case ResourceOperationDelete:
		return resourceMethodDelete
	case ResourceOperationRevise:
		return resourceMethodPost
	default:
		return operation
	}
}

func realizationStatus(now time.Time, validFromMS int64) string {
	if unixMillis(now) < validFromMS {
		return realizationPending
	}
	return realizationSuccess
}

func realizationJoinedStatus(
	now time.Time,
	validFromMS int64,
	desiredStatus sql.NullString,
	pendingStatus sql.NullString,
) string {
	if unixMillis(now) < validFromMS {
		if pendingStatus.Valid && pendingStatus.String != "" {
			return pendingStatus.String
		}
		return realizationPending
	}
	if desiredStatus.Valid && desiredStatus.String != "" {
		return desiredStatus.String
	}
	return realizationSuccess
}

func unixMillis(t time.Time) int64 {
	return t.UnixNano() / int64(time.Millisecond)
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func rollbackTxOnError(tx *sql.Tx, retErr *error, action string) {
	if *retErr == nil {
		return
	}
	rollbackErr := tx.Rollback()
	if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
		*retErr = fmt.Errorf("%w; rollback %s transaction: %w", *retErr, action, rollbackErr)
	}
}

func closeStatement(retErr *error, stmt *sql.Stmt, name string) {
	appendCloseError(retErr, "close "+name+" statement", stmt.Close())
}

func closeRows(retErr *error, rows *sql.Rows, name string) {
	appendCloseError(retErr, "close "+name+" rows", rows.Close())
}

func appendCloseError(retErr *error, action string, closeErr error) {
	if closeErr == nil {
		return
	}
	if *retErr == nil {
		*retErr = fmt.Errorf("%s: %w", action, closeErr)
		return
	}
	*retErr = fmt.Errorf("%w; %s: %w", *retErr, action, closeErr)
}
