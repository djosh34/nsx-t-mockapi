//nolint:tagliatelle // NSX member detail JSON fields intentionally use snake_case.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/netip"
	"strings"

	appsqlite "nsx-t-mockapi/internal/sqlite"

	"go.uber.org/zap"
)

const (
	maxNSXDisplayNameLength = 255
	maxNSXDescriptionLength = 1024
	maxNSXIPAddresses       = 25000
	ipRangePartCount        = 2

	policyGroupsCollectionKey = "policy.groups"
	groupResourceType         = "Group"
	ipAddressExpressionType   = "IPAddressExpression"
	pathExpressionType        = "PathExpression"
	groupListRouteTemplate    = "/policy/api/v1/infra/domains/{domain-id}/groups"
	groupItemRouteTemplate    = "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}"
	ipExpressionCollectionKey = "policy.group_ip_address_expressions"
	ipExpressionRouteTemplate = "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}/" +
		"ip-address-expressions/{expression-id}"
	pathExpressionCollection = "policy.group_path_expressions"
	pathExpressionRoute      = "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}/" +
		"path-expressions/{expression-id}"
)

var (
	errUnsupportedIPAddressAction = errors.New("unsupported ip expression action")
	errPayloadFieldMissing        = errors.New("payload field is missing")
	errPayloadFieldWrongType      = errors.New("payload field has wrong type")
)

type policyCollectionSpec struct {
	CollectionKey string
	ParentPath    string
}

func (r *router) handlePolicyGroupsList() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		domainID := routeParam(req, "domain-id")
		spec := policyGroupCollectionSpec(domainID)
		r.logger.Debug("listing policy groups", zap.String("domain_id", domainID), zap.String("parent_path", spec.ParentPath))

		resources, err := r.store.List(req.Context(), appsqlite.ListOptions{
			CollectionKey: spec.CollectionKey,
			ParentPath:    spec.ParentPath,
		})
		if err != nil {
			r.logger.Error("list policy groups failed", zap.String("domain_id", domainID), zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		results := make([]json.RawMessage, 0, len(resources))
		for _, resource := range resources {
			payload, decorateErr := withState(resource.Payload, resource.RealizationStatus)
			if decorateErr != nil {
				r.logger.Error(
					"decorate policy group list payload failed",
					zap.String("path", resource.Path),
					zap.Error(decorateErr),
				)
				http.Error(w, fmt.Sprintf("encode response: %v", decorateErr), http.StatusInternalServerError)
				return
			}
			results = append(results, payload)
		}

		writeOKJSON(w, r.logger, listResult{
			Results:       results,
			ResultCount:   len(results),
			SortBy:        "display_name",
			SortAscending: true,
		})
	}
}

func (r *router) handlePolicyGroupPut() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		spec := policyGroupSpec(routeParam(req, "domain-id"), routeParam(req, "group-id"))
		body, payload, ok := readAndValidateJSONObject(w, req, r.logger)
		if !ok {
			return
		}
		if !validateGroupPayload(w, r.logger, payload) {
			return
		}

		_, found, err := r.store.Read(req.Context(), appsqlite.ReadOptions{Path: spec.Path})
		if err != nil {
			r.logger.Error("read policy group before put failed", zap.String("path", spec.Path), zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		operation := appsqlite.ResourceOperationCreate
		enforceRevision := false
		if found {
			operation = appsqlite.ResourceOperationUpdate
			enforceRevision = true
		}
		resource, err := r.store.Mutate(req.Context(), appsqlite.Mutation{
			Spec:            spec,
			Body:            body,
			Username:        requestUsername(req),
			Operation:       operation,
			EnforceRevision: enforceRevision,
			RequestPath:     req.URL.Path,
			RouteTemplate:   groupItemRouteTemplate,
			StatusCode:      http.StatusOK,
		})
		if err != nil {
			writeMutationError(w, r.logger, err, "put policy group", spec.Path)
			return
		}

		response, err := withState(resource.Payload, resource.RealizationStatus)
		if err != nil {
			r.logger.Error("decorate policy group put response failed", zap.String("path", spec.Path), zap.Error(err))
			http.Error(w, fmt.Sprintf("encode response: %v", err), http.StatusInternalServerError)
			return
		}
		writeRawOKJSON(w, r.logger, response)
	}
}

func (r *router) handlePolicyGroupGet() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		spec := policyGroupSpec(routeParam(req, "domain-id"), routeParam(req, "group-id"))
		resource, found, err := r.store.Read(req.Context(), appsqlite.ReadOptions{Path: spec.Path})
		if err != nil {
			r.logger.Error("read policy group failed", zap.String("path", spec.Path), zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		if !found {
			http.NotFound(w, req)
			return
		}

		response, err := withState(resource.Payload, resource.RealizationStatus)
		if err != nil {
			r.logger.Error("decorate policy group get response failed", zap.String("path", spec.Path), zap.Error(err))
			http.Error(w, fmt.Sprintf("encode response: %v", err), http.StatusInternalServerError)
			return
		}
		writeRawOKJSON(w, r.logger, response)
	}
}

func (r *router) handlePolicyGroupPatch() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		spec := policyGroupSpec(routeParam(req, "domain-id"), routeParam(req, "group-id"))
		body, payload, ok := readAndValidateJSONObject(w, req, r.logger)
		if !ok {
			return
		}
		if !validateGroupPayload(w, r.logger, payload) {
			return
		}

		operation := appsqlite.ResourceOperationCreate
		mutationBody := body
		current, found, err := r.store.Read(req.Context(), appsqlite.ReadOptions{Path: spec.Path})
		if err != nil {
			r.logger.Error("read policy group before patch failed", zap.String("path", spec.Path), zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		if found {
			operation = appsqlite.ResourceOperationPatch
			mutationBody, err = mergeJSONObject(current.Payload, payload)
			if err != nil {
				r.logger.Error("merge policy group patch failed", zap.String("path", spec.Path), zap.Error(err))
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
		}

		_, err = r.store.Mutate(req.Context(), appsqlite.Mutation{
			Spec:          spec,
			Body:          mutationBody,
			Username:      requestUsername(req),
			Operation:     operation,
			RequestPath:   req.URL.Path,
			RouteTemplate: groupItemRouteTemplate,
			StatusCode:    http.StatusOK,
		})
		if err != nil {
			writeMutationError(w, r.logger, err, "patch policy group", spec.Path)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func (r *router) handlePolicyGroupDelete() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		spec := policyGroupSpec(routeParam(req, "domain-id"), routeParam(req, "group-id"))
		if !r.requireLiveGroup(w, req, spec) {
			return
		}

		_, err := r.store.Mutate(req.Context(), appsqlite.Mutation{
			Spec:          spec,
			Username:      requestUsername(req),
			Operation:     appsqlite.ResourceOperationDelete,
			RequestPath:   req.URL.Path,
			RouteTemplate: groupItemRouteTemplate,
			StatusCode:    http.StatusOK,
		})
		if err != nil {
			writeMutationError(w, r.logger, err, "delete policy group", spec.Path)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func (r *router) handlePolicyGroupIPAddressExpressionPatch() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		groupSpec, expressionSpec := policyGroupExpressionSpecs(req, ipAddressExpressionType)
		if !r.requireLiveGroup(w, req, groupSpec) {
			return
		}
		body, payload, ok := readAndValidateJSONObject(w, req, r.logger)
		if !ok {
			return
		}
		if !validateIPAddressExpressionPayload(w, r.logger, payload) {
			return
		}
		if err := r.upsertExpression(req, expressionSpec, body, ipExpressionRouteTemplate); err != nil {
			writeMutationError(w, r.logger, err, "patch ip address expression", expressionSpec.Path)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func (r *router) handlePolicyGroupIPAddressExpressionAction(action string) routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		groupSpec, expressionSpec := policyGroupExpressionSpecs(req, ipAddressExpressionType)
		if !r.requireLiveGroup(w, req, groupSpec) {
			return
		}
		_, payload, ok := readAndValidateJSONObject(w, req, r.logger)
		if !ok {
			return
		}
		requestIPs, valid := validateIPAddressListPayload(w, r.logger, payload)
		if !valid {
			return
		}

		current, found, err := r.store.Read(req.Context(), appsqlite.ReadOptions{Path: expressionSpec.Path})
		if err != nil {
			r.logger.Error(
				"read ip address expression before action failed",
				zap.String("path", expressionSpec.Path),
				zap.Error(err),
			)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		if !found {
			http.NotFound(w, req)
			return
		}

		nextBody, err := applyIPExpressionAction(current.Payload, requestIPs, action)
		if err != nil {
			r.logger.Error("apply ip address expression action failed", zap.String("path", expressionSpec.Path), zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		_, err = r.store.Mutate(req.Context(), appsqlite.Mutation{
			Spec:          expressionSpec,
			Body:          nextBody,
			Username:      requestUsername(req),
			Operation:     appsqlite.ResourceOperationPatch,
			RequestPath:   req.URL.Path,
			RouteTemplate: ipExpressionRouteTemplate,
			Action:        action,
			StatusCode:    http.StatusOK,
		})
		if err != nil {
			writeMutationError(w, r.logger, err, "update ip address expression action", expressionSpec.Path)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func (r *router) handlePolicyGroupIPAddressExpressionDelete() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		groupSpec, expressionSpec := policyGroupExpressionSpecs(req, ipAddressExpressionType)
		if !r.requireLiveGroup(w, req, groupSpec) {
			return
		}
		if !r.requireLiveResource(w, req, expressionSpec.Path) {
			return
		}
		_, err := r.store.Mutate(req.Context(), appsqlite.Mutation{
			Spec:          expressionSpec,
			Username:      requestUsername(req),
			Operation:     appsqlite.ResourceOperationDelete,
			RequestPath:   req.URL.Path,
			RouteTemplate: ipExpressionRouteTemplate,
			StatusCode:    http.StatusOK,
		})
		if err != nil {
			writeMutationError(w, r.logger, err, "delete ip address expression", expressionSpec.Path)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func (r *router) handlePolicyGroupPathExpressionPatch() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		groupSpec, expressionSpec := policyGroupExpressionSpecs(req, pathExpressionType)
		if !r.requireLiveGroup(w, req, groupSpec) {
			return
		}
		body, payload, ok := readAndValidateJSONObject(w, req, r.logger)
		if !ok {
			return
		}
		if !validatePathExpressionPayload(w, r.logger, payload) {
			return
		}
		if err := r.upsertExpression(req, expressionSpec, body, pathExpressionRoute); err != nil {
			writeMutationError(w, r.logger, err, "patch path expression", expressionSpec.Path)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func (r *router) handlePolicyGroupMembers(kind string) routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		groupSpec := policyGroupSpec(routeParam(req, "domain-id"), routeParam(req, "group-id"))
		group, found, err := r.store.Read(req.Context(), appsqlite.ReadOptions{Path: groupSpec.Path})
		if err != nil {
			r.logger.Error("read group for members failed", zap.String("path", groupSpec.Path), zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		if !found {
			http.NotFound(w, req)
			return
		}

		collector, err := r.collectGroupMembers(req, group)
		if err != nil {
			r.logger.Error("collect group members failed", zap.String("path", groupSpec.Path), zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		r.writeCollectedGroupMembers(w, req, kind, collector)
	}
}

func (r *router) writeCollectedGroupMembers(
	w http.ResponseWriter,
	req *http.Request,
	kind string,
	collector groupMemberCollector,
) {
	switch kind {
	case "ip-addresses":
		r.writeStringMemberList(w, "ip address", collector.ipAddresses)
	case "ip-groups":
		r.writeDetailMemberList(w, "ip group", collector.ipGroups)
	case "segments":
		r.writeDetailMemberList(w, "segment", collector.segments)
	default:
		http.NotFound(w, req)
	}
}

func (r *router) writeStringMemberList(w http.ResponseWriter, label string, values []string) {
	results, err := stringsToRawMessages(values)
	if err != nil {
		r.logger.Error("marshal "+label+" members failed", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	writeOKJSON(w, r.logger, listResult{Results: results, ResultCount: len(results)})
}

func (r *router) writeDetailMemberList(w http.ResponseWriter, label string, values []policyGroupMemberDetails) {
	results, err := memberDetailsToRawMessages(values)
	if err != nil {
		r.logger.Error("marshal "+label+" members failed", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	writeOKJSON(w, r.logger, listResult{Results: results, ResultCount: len(results)})
}

func (r *router) requireLiveGroup(w http.ResponseWriter, req *http.Request, spec appsqlite.ResourceSpec) bool {
	return r.requireLiveResource(w, req, spec.Path)
}

func (r *router) requireLiveResource(w http.ResponseWriter, req *http.Request, path string) bool {
	_, found, err := r.store.Read(req.Context(), appsqlite.ReadOptions{Path: path})
	if err != nil {
		r.logger.Error("read policy resource failed", zap.String("path", path), zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return false
	}
	if !found {
		http.NotFound(w, req)
		return false
	}
	return true
}

func policyGroupCollectionSpec(domainID string) policyCollectionSpec {
	return policyCollectionSpec{
		CollectionKey: policyGroupsCollectionKey,
		ParentPath:    "/infra/domains/" + domainID,
	}
}

func policyGroupSpec(domainID string, groupID string) appsqlite.ResourceSpec {
	parentPath := "/infra/domains/" + domainID
	return appsqlite.ResourceSpec{
		APIFamily:     appsqlite.ResourceAPIFamilyPolicy,
		CollectionKey: policyGroupsCollectionKey,
		Kind:          groupResourceType,
		ResourceType:  groupResourceType,
		Path:          parentPath + "/groups/" + groupID,
		ParentPath:    parentPath,
		RelativePath:  groupID,
	}
}

func policyGroupExpressionSpecs(
	req *http.Request,
	resourceType string,
) (appsqlite.ResourceSpec, appsqlite.ResourceSpec) {
	groupSpec := policyGroupSpec(routeParam(req, "domain-id"), routeParam(req, "group-id"))
	expressionID := routeParam(req, "expression-id")
	collectionKey := ipExpressionCollectionKey
	kind := ipAddressExpressionType
	segment := "ip-address-expressions"
	if resourceType == pathExpressionType {
		collectionKey = pathExpressionCollection
		kind = pathExpressionType
		segment = "path-expressions"
	}
	expressionSpec := appsqlite.ResourceSpec{
		APIFamily:     appsqlite.ResourceAPIFamilyPolicy,
		CollectionKey: collectionKey,
		Kind:          kind,
		ResourceType:  kind,
		Path:          groupSpec.Path + "/" + segment + "/" + expressionID,
		ParentPath:    groupSpec.Path,
		RelativePath:  expressionID,
	}
	return groupSpec, expressionSpec
}

func (r *router) upsertExpression(
	req *http.Request,
	spec appsqlite.ResourceSpec,
	body json.RawMessage,
	routeTemplate string,
) error {
	_, found, err := r.store.Read(req.Context(), appsqlite.ReadOptions{Path: spec.Path})
	if err != nil {
		return fmt.Errorf("read expression before upsert: %w", err)
	}
	operation := appsqlite.ResourceOperationCreate
	if found {
		operation = appsqlite.ResourceOperationPatch
	}
	_, err = r.store.Mutate(req.Context(), appsqlite.Mutation{
		Spec:          spec,
		Body:          body,
		Username:      requestUsername(req),
		Operation:     operation,
		RequestPath:   req.URL.Path,
		RouteTemplate: routeTemplate,
		StatusCode:    http.StatusOK,
	})
	if err != nil {
		return fmt.Errorf("mutate expression: %w", err)
	}
	return nil
}

func readAndValidateJSONObject(
	w http.ResponseWriter,
	req *http.Request,
	logger *zap.Logger,
) (json.RawMessage, map[string]any, bool) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		logger.Error("read request body failed", zap.String("path", req.URL.Path), zap.Error(err))
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return nil, nil, false
	}
	if err = req.Body.Close(); err != nil {
		logger.Error("close request body failed", zap.String("path", req.URL.Path), zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return nil, nil, false
	}
	if len(body) == 0 {
		http.Error(w, "request body must be a JSON object", http.StatusBadRequest)
		return nil, nil, false
	}

	var payload map[string]any
	if err = json.Unmarshal(body, &payload); err != nil {
		logger.Debug("decode request body failed", zap.String("path", req.URL.Path), zap.Error(err))
		http.Error(w, "request body must be a JSON object", http.StatusBadRequest)
		return nil, nil, false
	}
	if payload == nil {
		http.Error(w, "request body must be a JSON object", http.StatusBadRequest)
		return nil, nil, false
	}
	return json.RawMessage(body), payload, true
}

func validateGroupPayload(w http.ResponseWriter, logger *zap.Logger, payload map[string]any) bool {
	if !validateOptionalResourceType(w, payload, groupResourceType) {
		return false
	}
	if !validateOptionalStringLength(w, payload, "display_name", maxNSXDisplayNameLength) {
		return false
	}
	if !validateOptionalStringLength(w, payload, "description", maxNSXDescriptionLength) {
		return false
	}
	if !validateOptionalArrayMax(w, payload, "group_type", 1) {
		return false
	}
	if !validateOptionalArrayMax(w, payload, "extended_expression", 1) {
		return false
	}
	if !validateGroupExpression(w, payload) {
		return false
	}
	logger.Debug("validated group payload", zap.String("resource_type", groupResourceType))
	return true
}

func validateOptionalResourceType(w http.ResponseWriter, payload map[string]any, want string) bool {
	value, ok := payload["resource_type"]
	if !ok {
		return true
	}
	got, ok := value.(string)
	if !ok || got != want {
		http.Error(w, "resource_type does not match route", http.StatusBadRequest)
		return false
	}
	return true
}

func validateOptionalStringLength(w http.ResponseWriter, payload map[string]any, key string, maxLength int) bool {
	value, ok := payload[key]
	if !ok {
		return true
	}
	got, ok := value.(string)
	if !ok {
		http.Error(w, key+" must be a string", http.StatusBadRequest)
		return false
	}
	if len(got) > maxLength {
		http.Error(w, key+" exceeds maximum length", http.StatusBadRequest)
		return false
	}
	return true
}

func validateOptionalArrayMax(w http.ResponseWriter, payload map[string]any, key string, maxItems int) bool {
	value, ok := payload[key]
	if !ok {
		return true
	}
	items, ok := value.([]any)
	if !ok {
		http.Error(w, key+" must be an array", http.StatusBadRequest)
		return false
	}
	if len(items) > maxItems {
		http.Error(w, key+" exceeds maximum items", http.StatusBadRequest)
		return false
	}
	return true
}

func validateGroupExpression(w http.ResponseWriter, payload map[string]any) bool {
	value, ok := payload["expression"]
	if !ok {
		return true
	}
	items, ok := value.([]any)
	if !ok {
		http.Error(w, "expression must be an array", http.StatusBadRequest)
		return false
	}
	if len(items) == 0 {
		return true
	}
	if len(items)%2 == 0 {
		http.Error(w, "expression must contain odd number of entries", http.StatusBadRequest)
		return false
	}
	for index, item := range items {
		if !validateGroupExpressionEntry(w, item, index) {
			return false
		}
	}
	return true
}

func validateGroupExpressionEntry(w http.ResponseWriter, item any, index int) bool {
	itemPayload, typeOK := item.(map[string]any)
	if !typeOK {
		http.Error(w, "expression entries must be objects", http.StatusBadRequest)
		return false
	}
	resourceType, typeOK := itemPayload["resource_type"].(string)
	if !typeOK || resourceType == "" {
		http.Error(w, "expression entries require resource_type", http.StatusBadRequest)
		return false
	}
	isConjunction := resourceType == "ConjunctionOperator"
	if index%2 == 0 && isConjunction {
		http.Error(w, "expression conjunction operators must be at odd indices", http.StatusBadRequest)
		return false
	}
	if index%2 == 1 && !isConjunction {
		http.Error(w, "expression non-conjunction entries must be at even indices", http.StatusBadRequest)
		return false
	}
	return true
}

func validateIPAddressExpressionPayload(w http.ResponseWriter, logger *zap.Logger, payload map[string]any) bool {
	if !validateOptionalResourceType(w, payload, ipAddressExpressionType) {
		return false
	}
	if !validateOptionalStringLength(w, payload, "display_name", maxNSXDisplayNameLength) {
		return false
	}
	if !validateOptionalStringLength(w, payload, "description", maxNSXDescriptionLength) {
		return false
	}
	_, ok := validateIPAddressListPayload(w, logger, payload)
	return ok
}

func validateIPAddressListPayload(w http.ResponseWriter, logger *zap.Logger, payload map[string]any) ([]string, bool) {
	value, ok := payload["ip_addresses"]
	if !ok {
		http.Error(w, "ip_addresses is required", http.StatusBadRequest)
		return nil, false
	}
	items, ok := value.([]any)
	if !ok {
		http.Error(w, "ip_addresses must be an array", http.StatusBadRequest)
		return nil, false
	}
	if len(items) == 0 {
		http.Error(w, "ip_addresses must not be empty", http.StatusBadRequest)
		return nil, false
	}
	if len(items) > maxNSXIPAddresses {
		http.Error(w, "ip_addresses exceeds maximum items", http.StatusBadRequest)
		return nil, false
	}
	addresses := make([]string, 0, len(items))
	family := ""
	for _, item := range items {
		address, nextFamily, valid := validateIPAddressListItem(w, item, family)
		if !valid {
			return nil, false
		}
		family = nextFamily
		addresses = append(addresses, address)
	}
	logger.Debug("validated ip address list", zap.Int("ip_count", len(addresses)), zap.String("family", family))
	return addresses, true
}

func validateIPAddressListItem(w http.ResponseWriter, item any, currentFamily string) (string, string, bool) {
	address, typeOK := item.(string)
	if !typeOK || address == "" {
		http.Error(w, "ip_addresses entries must be strings", http.StatusBadRequest)
		return "", "", false
	}
	itemFamily, valid := ipElementFamily(address)
	if !valid {
		http.Error(w, "ip_addresses entries must be valid IP elements", http.StatusBadRequest)
		return "", "", false
	}
	if currentFamily == "" {
		return address, itemFamily, true
	}
	if currentFamily != itemFamily {
		http.Error(w, "ip_addresses must not mix IPv4 and IPv6", http.StatusBadRequest)
		return "", "", false
	}
	return address, currentFamily, true
}

func validatePathExpressionPayload(w http.ResponseWriter, logger *zap.Logger, payload map[string]any) bool {
	if !validateOptionalResourceType(w, payload, pathExpressionType) {
		return false
	}
	if !validateOptionalStringLength(w, payload, "display_name", maxNSXDisplayNameLength) {
		return false
	}
	if !validateOptionalStringLength(w, payload, "description", maxNSXDescriptionLength) {
		return false
	}
	value, ok := payload["paths"]
	if !ok {
		http.Error(w, "paths is required", http.StatusBadRequest)
		return false
	}
	items, ok := value.([]any)
	if !ok {
		http.Error(w, "paths must be an array", http.StatusBadRequest)
		return false
	}
	if len(items) == 0 {
		http.Error(w, "paths must not be empty", http.StatusBadRequest)
		return false
	}
	for _, item := range items {
		path, typeOK := item.(string)
		if !typeOK || !isAllowedMemberPath(path) {
			http.Error(w, "paths entries must be supported policy paths", http.StatusBadRequest)
			return false
		}
	}
	logger.Debug("validated path expression", zap.Int("path_count", len(items)))
	return true
}

func ipElementFamily(value string) (string, bool) {
	if strings.Contains(value, "-") {
		return ipRangeFamily(value)
	}
	if strings.Contains(value, "/") {
		return ipPrefixFamily(value)
	}
	return ipAddressFamily(value)
}

func ipRangeFamily(value string) (string, bool) {
	parts := strings.Split(value, "-")
	if len(parts) != ipRangePartCount {
		return "", false
	}
	left, leftOK := ipAddressFamily(parts[0])
	right, rightOK := ipAddressFamily(parts[1])
	return left, leftOK && rightOK && left == right
}

func ipPrefixFamily(value string) (string, bool) {
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return "", false
	}
	return parsedAddressFamily(prefix.Addr())
}

func ipAddressFamily(value string) (string, bool) {
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return "", false
	}
	return parsedAddressFamily(addr)
}

func parsedAddressFamily(addr netip.Addr) (string, bool) {
	if addr.Is4() {
		return "ipv4", true
	}
	if addr.Is6() {
		return "ipv6", true
	}
	return "", false
}

func isAllowedMemberPath(path string) bool {
	return strings.HasPrefix(path, "/infra/domains/") && strings.Contains(path, "/groups/") ||
		strings.HasPrefix(path, "/infra/segments/") ||
		strings.HasPrefix(path, "/infra/tier-1s/") && strings.Contains(path, "/segments/") ||
		strings.Contains(path, "/ports/")
}

func applyIPExpressionAction(raw json.RawMessage, requestIPs []string, action string) (json.RawMessage, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode ip expression payload: %w", err)
	}
	existing, err := stringArrayField(payload, "ip_addresses")
	if err != nil {
		return nil, err
	}
	switch action {
	case "add":
		payload["ip_addresses"] = appendMissing(existing, requestIPs)
	case "remove":
		payload["ip_addresses"] = removeStrings(existing, requestIPs)
	default:
		return nil, fmt.Errorf("%w: %q", errUnsupportedIPAddressAction, action)
	}
	next, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal ip expression payload: %w", err)
	}
	return next, nil
}

func stringArrayField(payload map[string]any, key string) ([]string, error) {
	value, ok := payload[key]
	if !ok {
		return nil, fmt.Errorf("%w: %q", errPayloadFieldMissing, key)
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%w: %q is not an array", errPayloadFieldWrongType, key)
	}
	values := make([]string, 0, len(items))
	for _, item := range items {
		itemValue, typeOK := item.(string)
		if !typeOK {
			return nil, fmt.Errorf("%w: %q contains non-string", errPayloadFieldWrongType, key)
		}
		values = append(values, itemValue)
	}
	return values, nil
}

func appendMissing(existing []string, additions []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(existing)+len(additions))
	for _, value := range existing {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	for _, value := range additions {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func removeStrings(existing []string, removals []string) []string {
	removeSet := map[string]struct{}{}
	for _, value := range removals {
		removeSet[value] = struct{}{}
	}
	result := make([]string, 0, len(existing))
	for _, value := range existing {
		if _, remove := removeSet[value]; remove {
			continue
		}
		result = append(result, value)
	}
	return result
}

type groupMemberCollector struct {
	ipAddresses []string
	ipGroups    []policyGroupMemberDetails
	segments    []policyGroupMemberDetails
}

type policyGroupMemberDetails struct {
	DisplayName string `json:"display_name"`
	ID          string `json:"id"`
	Path        string `json:"path"`
}

func (r *router) collectGroupMembers(req *http.Request, group appsqlite.StoredResource) (groupMemberCollector, error) {
	var collector groupMemberCollector
	groupPayload, err := decodePayloadObject(group.Payload)
	if err != nil {
		return groupMemberCollector{}, err
	}
	collector.addGroupExpressionMembers(groupPayload)

	for _, collectionKey := range []string{ipExpressionCollectionKey, pathExpressionCollection} {
		children, listErr := r.store.List(req.Context(), appsqlite.ListOptions{
			CollectionKey: collectionKey,
			ParentPath:    group.Path,
		})
		if listErr != nil {
			return groupMemberCollector{}, fmt.Errorf("list expression children: %w", listErr)
		}
		for _, child := range children {
			payload, decodeErr := decodePayloadObject(child.Payload)
			if decodeErr != nil {
				return groupMemberCollector{}, decodeErr
			}
			collector.addExpressionMembers(payload)
		}
	}
	return collector.deduplicated(), nil
}

func (c *groupMemberCollector) addGroupExpressionMembers(payload map[string]any) {
	items, ok := payload["expression"].([]any)
	if !ok {
		return
	}
	for _, item := range items {
		expression, typeOK := item.(map[string]any)
		if !typeOK {
			continue
		}
		c.addExpressionMembers(expression)
	}
}

func (c *groupMemberCollector) addExpressionMembers(payload map[string]any) {
	resourceType, ok := payload["resource_type"].(string)
	if !ok {
		return
	}
	switch resourceType {
	case ipAddressExpressionType:
		ips, err := stringArrayField(payload, "ip_addresses")
		if err == nil {
			c.ipAddresses = append(c.ipAddresses, ips...)
		}
	case pathExpressionType:
		paths, err := stringArrayField(payload, "paths")
		if err == nil {
			c.addPathMembers(paths)
		}
	}
}

func (c *groupMemberCollector) addPathMembers(paths []string) {
	for _, path := range paths {
		switch {
		case strings.Contains(path, "/groups/"):
			c.ipGroups = append(c.ipGroups, policyMemberDetails(path))
		case strings.Contains(path, "/segments/"):
			c.segments = append(c.segments, policyMemberDetails(path))
		}
	}
}

func (c *groupMemberCollector) deduplicated() groupMemberCollector {
	return groupMemberCollector{
		ipAddresses: uniqueStrings(c.ipAddresses),
		ipGroups:    uniqueMemberDetails(c.ipGroups),
		segments:    uniqueMemberDetails(c.segments),
	}
}

func decodePayloadObject(raw json.RawMessage) (map[string]any, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode payload object: %w", err)
	}
	return payload, nil
}

func policyMemberDetails(path string) policyGroupMemberDetails {
	id := path
	if index := strings.LastIndex(path, "/"); index >= 0 && index < len(path)-1 {
		id = path[index+1:]
	}
	return policyGroupMemberDetails{
		DisplayName: id,
		ID:          id,
		Path:        path,
	}
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func uniqueMemberDetails(values []policyGroupMemberDetails) []policyGroupMemberDetails {
	seen := map[string]struct{}{}
	result := make([]policyGroupMemberDetails, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value.Path]; ok {
			continue
		}
		seen[value.Path] = struct{}{}
		result = append(result, value)
	}
	return result
}

func stringsToRawMessages(values []string) ([]json.RawMessage, error) {
	results := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("marshal string member: %w", err)
		}
		results = append(results, raw)
	}
	return results, nil
}

func memberDetailsToRawMessages(values []policyGroupMemberDetails) ([]json.RawMessage, error) {
	results := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("marshal member details: %w", err)
		}
		results = append(results, raw)
	}
	return results, nil
}

func mergeJSONObject(current json.RawMessage, patch map[string]any) (json.RawMessage, error) {
	var merged map[string]any
	if err := json.Unmarshal(current, &merged); err != nil {
		return nil, fmt.Errorf("decode current resource: %w", err)
	}
	maps.Copy(merged, patch)
	raw, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("marshal merged resource: %w", err)
	}
	return raw, nil
}

func withState(raw json.RawMessage, status string) (json.RawMessage, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode resource payload: %w", err)
	}
	payload["state"] = status
	decorated, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal resource payload: %w", err)
	}
	return decorated, nil
}

func requestUsername(req *http.Request) string {
	user, ok := UserFromContext(req.Context())
	if !ok {
		return appsqlite.DefaultAdminUsername
	}
	return user.Username
}

func writeMutationError(w http.ResponseWriter, logger *zap.Logger, err error, action string, path string) {
	if errors.Is(err, appsqlite.ErrRevisionConflict) {
		logger.Debug(action+" revision conflict", zap.String("path", path), zap.Error(err))
		http.Error(w, http.StatusText(http.StatusPreconditionFailed), http.StatusPreconditionFailed)
		return
	}
	logger.Error(action+" failed", zap.String("path", path), zap.Error(err))
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}

func writeOKJSON(w http.ResponseWriter, logger *zap.Logger, value any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		logger.Error("encode json response failed", zap.Error(err))
	}
}

func writeRawOKJSON(w http.ResponseWriter, logger *zap.Logger, value json.RawMessage) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(value); err != nil {
		logger.Error("write json response failed", zap.Error(err))
	}
}
