package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"go.uber.org/zap"
)

const (
	createResourceFTSSQL = `
CREATE VIRTUAL TABLE IF NOT EXISTS resource_fts
USING fts5(
	resource_path UNINDEXED,
	all_text,
	display_name,
	description,
	path_text,
	resource_type,
	tag_text
)`

	createSearchFieldsSQL = `
CREATE TABLE IF NOT EXISTS search_fields (
	resource_path TEXT NOT NULL,
	field_name TEXT NOT NULL,
	value_ordinal INTEGER NOT NULL,
	text_value TEXT,
	number_value REAL,
	bool_value INTEGER CHECK (bool_value IN (0, 1) OR bool_value IS NULL),
	PRIMARY KEY (resource_path, field_name, value_ordinal),
	FOREIGN KEY (resource_path) REFERENCES resources(path) ON DELETE CASCADE
)`

	createSearchFieldsTextIndexSQL = `
CREATE INDEX IF NOT EXISTS search_fields_text_idx
ON search_fields(field_name, text_value)`

	createSearchFieldsNumberIndexSQL = `
CREATE INDEX IF NOT EXISTS search_fields_number_idx
ON search_fields(field_name, number_value)`

	createSearchFieldsBoolIndexSQL = `
CREATE INDEX IF NOT EXISTS search_fields_bool_idx
ON search_fields(field_name, bool_value)`

	createSearchFieldsResourceIndexSQL = `
CREATE INDEX IF NOT EXISTS search_fields_resource_idx
ON search_fields(resource_path)`

	deleteResourceFTSSQL = `
DELETE FROM resource_fts
WHERE resource_path = ?`

	deleteSearchFieldsSQL = `
DELETE FROM search_fields
WHERE resource_path = ?`

	insertResourceFTSSQL = `
INSERT INTO resource_fts (
	resource_path,
	all_text,
	display_name,
	description,
	path_text,
	resource_type,
	tag_text
) VALUES (?, ?, ?, ?, ?, ?, ?)`

	insertSearchFieldSQL = `
INSERT INTO search_fields (
	resource_path,
	field_name,
	value_ordinal,
	text_value,
	number_value,
	bool_value
) VALUES (?, ?, ?, ?, ?, ?)`
)

type resourceFTSDocument struct {
	path         string
	allText      string
	displayName  string
	description  string
	pathText     string
	resourceType string
	tagText      string
}

type searchFieldValue struct {
	name      string
	ordinal   int
	textValue sql.NullString
	numValue  sql.NullFloat64
	boolValue sql.NullInt64
}

func ensureSearchIndexSchema(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []struct {
		name string
		sql  string
	}{
		{name: "resource_fts", sql: createResourceFTSSQL},
		{name: "search_fields", sql: createSearchFieldsSQL},
		{name: "search_fields_text_idx", sql: createSearchFieldsTextIndexSQL},
		{name: "search_fields_number_idx", sql: createSearchFieldsNumberIndexSQL},
		{name: "search_fields_bool_idx", sql: createSearchFieldsBoolIndexSQL},
		{name: "search_fields_resource_idx", sql: createSearchFieldsResourceIndexSQL},
	} {
		if _, err := tx.ExecContext(ctx, statement.sql); err != nil {
			return fmt.Errorf("create %s search schema object: %w", statement.name, err)
		}
	}
	return nil
}

func (s ResourceStore) maintainSearchIndex(
	ctx context.Context,
	tx *sql.Tx,
	mutation Mutation,
	resource StoredResource,
) error {
	if mutation.Spec.APIFamily == ResourceAPIFamilyManager && mutation.Operation == ResourceOperationDelete {
		if err := deleteResourceSearchIndex(ctx, tx, mutation.Spec.Path); err != nil {
			return err
		}
		s.logger.Debug("deleted resource search index", zap.String("path", mutation.Spec.Path))
		return nil
	}

	payload, err := decodeJSONObject(resource.Payload)
	if err != nil {
		return fmt.Errorf("decode search index payload for %q: %w", resource.Path, err)
	}
	document := buildResourceFTSDocument(resource.Path, payload)
	fields := buildSearchFields(payload)
	if err = replaceResourceSearchIndex(ctx, tx, document, fields); err != nil {
		return err
	}
	s.logger.Debug(
		"upserted resource search index",
		zap.String("path", resource.Path),
		zap.Int("all_text_length", len(document.allText)),
		zap.Int("field_count", len(fields)),
	)
	return nil
}

func deleteResourceSearchIndex(ctx context.Context, tx *sql.Tx, path string) error {
	result, err := execPrepared(ctx, tx, deleteSearchFieldsSQL, path)
	if err != nil {
		return fmt.Errorf("delete search field rows for %q: %w", path, err)
	}
	if _, err = result.RowsAffected(); err != nil {
		return fmt.Errorf("read deleted search field rows affected: %w", err)
	}

	result, err = execPrepared(ctx, tx, deleteResourceFTSSQL, path)
	if err != nil {
		return fmt.Errorf("delete resource fts row for %q: %w", path, err)
	}
	if _, err = result.RowsAffected(); err != nil {
		return fmt.Errorf("read deleted resource fts rows affected: %w", err)
	}
	return nil
}

func replaceResourceSearchIndex(
	ctx context.Context,
	tx *sql.Tx,
	document resourceFTSDocument,
	fields []searchFieldValue,
) error {
	if err := deleteResourceSearchIndex(ctx, tx, document.path); err != nil {
		return err
	}

	result, err := execPrepared(
		ctx,
		tx,
		insertResourceFTSSQL,
		document.path,
		document.allText,
		document.displayName,
		document.description,
		document.pathText,
		document.resourceType,
		document.tagText,
	)
	if err != nil {
		return fmt.Errorf("insert resource fts row for %q: %w", document.path, err)
	}
	if _, err = result.RowsAffected(); err != nil {
		return fmt.Errorf("read inserted resource fts rows affected: %w", err)
	}

	for _, field := range fields {
		result, err = execPrepared(
			ctx,
			tx,
			insertSearchFieldSQL,
			document.path,
			field.name,
			field.ordinal,
			nullableStringValue(field.textValue),
			nullableFloatValue(field.numValue),
			nullableIntValue(field.boolValue),
		)
		if err != nil {
			return fmt.Errorf("insert search field %q for %q: %w", field.name, document.path, err)
		}
		if _, err = result.RowsAffected(); err != nil {
			return fmt.Errorf("read inserted search field rows affected: %w", err)
		}
	}
	return nil
}

func buildResourceFTSDocument(path string, payload map[string]any) resourceFTSDocument {
	displayName := stringValue(payload["display_name"])
	description := stringValue(payload["description"])
	resourceType := stringValue(payload["resource_type"])
	pathText := stringValue(payload["path"])
	tagText := strings.Join(collectTagText(payload["tags"]), " ")
	allText := strings.Join(collectSearchText(payload), " ")

	return resourceFTSDocument{
		path:         path,
		allText:      allText,
		displayName:  displayName,
		description:  description,
		pathText:     pathText,
		resourceType: resourceType,
		tagText:      tagText,
	}
}

func collectSearchText(value any) []string {
	switch typed := value.(type) {
	case map[string]any:
		parts := make([]string, 0, len(typed))
		for _, nested := range typed {
			parts = append(parts, collectSearchText(nested)...)
		}
		return parts
	case []any:
		parts := make([]string, 0, len(typed))
		for _, nested := range typed {
			parts = append(parts, collectSearchText(nested)...)
		}
		return parts
	case string:
		return []string{typed}
	case float64:
		return []string{fmt.Sprintf("%v", typed)}
	case bool:
		if typed {
			return []string{"true"}
		}
		return []string{"false"}
	default:
		return nil
	}
}

func collectTagText(value any) []string {
	tags, ok := value.([]any)
	if !ok {
		return nil
	}

	parts := []string{}
	for _, tag := range tags {
		tagObject, isObject := tag.(map[string]any)
		if !isObject {
			parts = append(parts, collectSearchText(tag)...)
			continue
		}
		parts = append(parts, stringValue(tagObject["scope"]), stringValue(tagObject["tag"]))
	}
	return parts
}

func buildSearchFields(payload map[string]any) []searchFieldValue {
	counters := map[string]int{}
	return collectSearchFields("", payload, counters)
}

func collectSearchFields(prefix string, value any, counters map[string]int) []searchFieldValue {
	switch typed := value.(type) {
	case map[string]any:
		return collectObjectSearchFields(prefix, typed, counters)
	case []any:
		return collectArraySearchFields(prefix, typed, counters)
	case string:
		return collectTextSearchField(prefix, typed, counters)
	case float64:
		return collectNumberSearchField(prefix, typed, counters)
	case bool:
		return collectBoolSearchField(prefix, typed, counters)
	default:
		return nil
	}
}

func collectObjectSearchFields(prefix string, value map[string]any, counters map[string]int) []searchFieldValue {
	fields := make([]searchFieldValue, 0, len(value))
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		nested := value[key]
		fieldName := key
		if prefix != "" {
			fieldName = prefix + "." + key
		}
		fields = append(fields, collectSearchFields(fieldName, nested, counters)...)
	}
	return fields
}

func collectArraySearchFields(prefix string, value []any, counters map[string]int) []searchFieldValue {
	fields := make([]searchFieldValue, 0, len(value))
	for _, nested := range value {
		fields = append(fields, collectSearchFields(prefix, nested, counters)...)
	}
	return fields
}

func collectTextSearchField(prefix string, value string, counters map[string]int) []searchFieldValue {
	if prefix == "" {
		return nil
	}
	return []searchFieldValue{nextTextSearchField(prefix, strings.ToLower(value), counters)}
}

func collectNumberSearchField(prefix string, value float64, counters map[string]int) []searchFieldValue {
	if prefix == "" {
		return nil
	}
	return []searchFieldValue{nextNumberSearchField(prefix, value, counters)}
}

func collectBoolSearchField(prefix string, value bool, counters map[string]int) []searchFieldValue {
	if prefix == "" {
		return nil
	}
	return []searchFieldValue{nextBoolSearchField(prefix, value, counters)}
}

func nextTextSearchField(name string, value string, counters map[string]int) searchFieldValue {
	ordinal := nextSearchFieldOrdinal(name, counters)
	return searchFieldValue{
		name:    name,
		ordinal: ordinal,
		textValue: sql.NullString{
			String: value,
			Valid:  true,
		},
	}
}

func nextNumberSearchField(name string, value float64, counters map[string]int) searchFieldValue {
	ordinal := nextSearchFieldOrdinal(name, counters)
	return searchFieldValue{
		name:    name,
		ordinal: ordinal,
		numValue: sql.NullFloat64{
			Float64: value,
			Valid:   true,
		},
	}
}

func nextBoolSearchField(name string, value bool, counters map[string]int) searchFieldValue {
	ordinal := nextSearchFieldOrdinal(name, counters)
	boolInt := int64(0)
	if value {
		boolInt = 1
	}
	return searchFieldValue{
		name:    name,
		ordinal: ordinal,
		boolValue: sql.NullInt64{
			Int64: boolInt,
			Valid: true,
		},
	}
}

func nextSearchFieldOrdinal(name string, counters map[string]int) int {
	ordinal := counters[name]
	counters[name] = ordinal + 1
	return ordinal
}

func stringValue(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

func nullableStringValue(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}

func nullableFloatValue(value sql.NullFloat64) any {
	if !value.Valid {
		return nil
	}
	return value.Float64
}

func nullableIntValue(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}
