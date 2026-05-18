//nolint:tagliatelle // NSX SearchResponse JSON fields intentionally use snake_case.
package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	appsqlite "nsx-t-mockapi/internal/sqlite"

	"go.uber.org/zap"
)

const (
	defaultSearchSortBy         = "display_name"
	documentedMaxSearchPageSize = 1000
)

type searchResponse struct {
	nsxResponseMetadata

	Results       []json.RawMessage `json:"results"`
	ResultCount   *int              `json:"result_count,omitempty"`
	Cursor        string            `json:"cursor,omitempty"`
	SortBy        string            `json:"sort_by"`
	SortAscending bool              `json:"sort_ascending"`
}

func (r *router) handleSearchQuery() routeHandler {
	return r.handleSearch(appsqlite.SearchSyntaxQuery)
}

func (r *router) handleSearch(syntax appsqlite.SearchSyntax) routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		query := strings.TrimSpace(req.URL.Query().Get("query"))
		if query == "" {
			r.logger.Debug(
				"rejected search query without query parameter",
				zap.String("path", req.URL.Path),
				zap.String("syntax", string(syntax)),
			)
			http.Error(w, "query is required", http.StatusBadRequest)
			return
		}
		includeMarkedForDelete, err := appsqlite.SearchIncludesMarkedForDelete(appsqlite.SearchQueryOptions{
			Syntax: syntax,
			Query:  query,
		})
		if err != nil {
			r.logger.Debug(
				"search query rejected",
				zap.String("syntax", string(syntax)),
				zap.String("query", query),
				zap.Error(err),
			)
			http.Error(w, "invalid search query", http.StatusBadRequest)
			return
		}

		pageSize, ok := r.searchPageSize(w, req)
		if !ok {
			return
		}
		offset, ok := r.searchCursorOffset(w, req)
		if !ok {
			return
		}
		sortBy, sortAscending, ok := r.searchSortParams(w, req)
		if !ok {
			return
		}
		page, err := r.store.SearchPage(req.Context(), appsqlite.SearchQueryOptions{
			Syntax:                 syntax,
			Query:                  query,
			IncludeMarkedForDelete: includeMarkedForDelete,
			Limit:                  pageSize,
			Offset:                 offset,
			SortBy:                 sortBy,
			SortAscending:          sortAscending,
		})
		if err != nil {
			if appsqlite.IsSearchQueryError(err) {
				r.logger.Debug(
					"search query rejected",
					zap.String("syntax", string(syntax)),
					zap.String("query", query),
					zap.Error(err),
				)
				http.Error(w, "invalid search query", http.StatusBadRequest)
				return
			}
			r.logger.Error(
				"search query failed",
				zap.String("syntax", string(syntax)),
				zap.String("query", query),
				zap.Error(err),
			)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		results := make([]json.RawMessage, 0, len(page.Resources))
		includedFields := includedSearchFields(req)
		for _, resource := range page.Resources {
			payload, projectErr := searchResultPayload(resource, includedFields)
			if projectErr != nil {
				r.logger.Error("project search result failed", zap.String("path", resource.Path), zap.Error(projectErr))
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			results = append(results, payload)
		}
		r.logger.Info(
			"search query completed",
			zap.String("path", req.URL.Path),
			zap.String("syntax", string(syntax)),
			zap.String("query", query),
			zap.Int("result_count", page.ResultCount),
			zap.Int("page_count", len(results)),
			zap.Int("page_size", pageSize),
			zap.Int("offset", offset),
			zap.String("cursor", page.Cursor),
		)
		writeOKJSON(w, r.logger, newSearchResponse(req, page, results, sortBy, sortAscending, offset == 0))
	}
}

func newSearchResponse(
	req *http.Request,
	page appsqlite.SearchPage,
	results []json.RawMessage,
	sortBy string,
	sortAscending bool,
	includeResultCount bool,
) searchResponse {
	var resultCount *int
	if includeResultCount {
		resultCount = &page.ResultCount
	}
	return searchResponse{
		nsxResponseMetadata: newNSXResponseMetadata(req, searchResponseSchema),
		Results:             results,
		ResultCount:         resultCount,
		Cursor:              page.Cursor,
		SortBy:              sortBy,
		SortAscending:       sortAscending,
	}
}

func searchResultPayload(resource appsqlite.StoredResource, fields []string) (json.RawMessage, error) {
	raw := resource.Payload
	if resource.APIFamily == appsqlite.ResourceAPIFamilyPolicy {
		decorated, err := withPolicySearchStatus(resource)
		if err != nil {
			return nil, err
		}
		raw = decorated
	}
	return projectSearchResult(raw, fields)
}

func withPolicySearchStatus(resource appsqlite.StoredResource) (json.RawMessage, error) {
	payload, err := decodePayloadObject(resource.Payload)
	if err != nil {
		return nil, err
	}
	consolidated := map[string]any{"consolidated_status": resource.RealizationStatus}
	payload["status"] = map[string]any{
		"consolidated_status": consolidated,
		"consolidated_status_per_enforcement_point": []map[string]any{
			{
				"consolidated_status":  consolidated,
				"enforcement_point_id": "default",
			},
		},
		"intent_path": resource.Path,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal policy search status payload: %w", err)
	}
	return raw, nil
}

func (r *router) searchPageSize(w http.ResponseWriter, req *http.Request) (int, bool) {
	raw := strings.TrimSpace(req.URL.Query().Get("page_size"))
	if raw == "" {
		return r.search.DefaultPageSize, true
	}
	pageSize, err := strconv.Atoi(raw)
	if err != nil || pageSize < 0 {
		r.logger.Debug("rejected invalid search page_size", zap.String("page_size", raw), zap.Error(err))
		http.Error(w, "page_size must be a non-negative integer", http.StatusBadRequest)
		return 0, false
	}
	maxPageSize := r.search.MaxPageSize
	if maxPageSize > 0 && pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	if pageSize > documentedMaxSearchPageSize {
		pageSize = documentedMaxSearchPageSize
	}
	return pageSize, true
}

func (r *router) searchCursorOffset(w http.ResponseWriter, req *http.Request) (int, bool) {
	raw := strings.TrimSpace(req.URL.Query().Get("cursor"))
	if raw == "" {
		return 0, true
	}
	offset, err := strconv.Atoi(raw)
	if err != nil || offset < 0 {
		r.logger.Debug("rejected invalid search cursor", zap.String("cursor", raw), zap.Error(err))
		http.Error(w, "cursor must be a non-negative integer offset", http.StatusBadRequest)
		return 0, false
	}
	return offset, true
}

func (r *router) searchSortParams(w http.ResponseWriter, req *http.Request) (string, bool, bool) {
	sortBy := strings.TrimSpace(req.URL.Query().Get("sort_by"))
	if sortBy == "" {
		sortBy = defaultSearchSortBy
	}
	rawAscending := strings.TrimSpace(req.URL.Query().Get("sort_ascending"))
	if rawAscending == "" {
		return sortBy, true, true
	}
	switch rawAscending {
	case "true":
		return sortBy, true, true
	case "false":
		return sortBy, false, true
	default:
		r.logger.Debug("rejected invalid search sort_ascending", zap.String("sort_ascending", rawAscending))
		http.Error(w, "sort_ascending must be true or false", http.StatusBadRequest)
		return "", false, false
	}
}

func includedSearchFields(req *http.Request) []string {
	raw := strings.TrimSpace(req.URL.Query().Get("included_fields"))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	fields := make([]string, 0, len(parts))
	for _, part := range parts {
		field := strings.TrimSpace(part)
		if field != "" {
			fields = append(fields, field)
		}
	}
	return fields
}

func projectSearchResult(raw json.RawMessage, fields []string) (json.RawMessage, error) {
	if len(fields) == 0 {
		return raw, nil
	}
	payload, err := decodePayloadObject(raw)
	if err != nil {
		return nil, err
	}
	projected := map[string]any{}
	for _, field := range fields {
		value, ok := payload[field]
		if ok {
			projected[field] = value
		}
	}
	projectedRaw, err := json.Marshal(projected)
	if err != nil {
		return nil, fmt.Errorf("marshal projected search result: %w", err)
	}
	return projectedRaw, nil
}
