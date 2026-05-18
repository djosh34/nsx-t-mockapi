package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"sort"
	"strings"

	appsqlite "nsx-t-mockapi/internal/sqlite"

	"go.uber.org/zap"
)

const (
	maxNSXTags        = 30
	maxPolicyPathList = 128
)

type policyResourceConfig struct {
	Name          string
	CollectionKey string
	ResourceType  string
	RouteTemplate string
	Spec          func(*http.Request) appsqlite.ResourceSpec
	Parent        func(*http.Request) appsqlite.ResourceSpec
	Validate      func(http.ResponseWriter, *zap.Logger, map[string]any) bool
	Decorate      func(*router, *http.Request, appsqlite.StoredResource) (json.RawMessage, bool)
}

func (r *router) handlePolicyResourceList(collection func(*http.Request) policyCollectionSpec) routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		spec := collection(req)
		r.logger.Debug(
			"listing policy resources",
			zap.String("collection_key", spec.CollectionKey),
			zap.String("parent_path", spec.ParentPath),
		)
		resources, err := r.store.List(req.Context(), appsqlite.ListOptions{
			CollectionKey: spec.CollectionKey,
			ParentPath:    spec.ParentPath,
		})
		if err != nil {
			r.logger.Error("list policy resources failed", zap.String("collection_key", spec.CollectionKey), zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		results := make([]json.RawMessage, 0, len(resources))
		for _, resource := range resources {
			results = append(results, resource.Payload)
		}
		writePolicyListResult(w, r.logger, results)
	}
}

func (r *router) handlePolicyResourceGet(config policyResourceConfig) routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		spec := config.Spec(req)
		resource, found := r.readLiveResource(w, req, spec.Path, "read "+config.Name)
		if !found {
			return
		}
		responseBody := resource.Payload
		if config.Decorate != nil {
			var decorated bool
			responseBody, decorated = config.Decorate(r, req, resource)
			if !decorated {
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
		}
		writeRawOKJSON(w, r.logger, responseBody)
	}
}

func (r *router) handlePolicyResourcePut(config policyResourceConfig) routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		spec := config.Spec(req)
		if !r.requireOptionalParent(w, req, config) {
			return
		}
		body, payload, ok := readAndValidateJSONObject(w, req, r.logger)
		if !ok || !config.Validate(w, r.logger, payload) {
			return
		}
		current, found, err := r.store.Read(req.Context(), appsqlite.ReadOptions{Path: spec.Path})
		if err != nil {
			r.logger.Error("read policy resource before put failed", zap.String("path", spec.Path), zap.Error(err))
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
			RouteTemplate:   config.RouteTemplate,
			StatusCode:      http.StatusOK,
		})
		if err != nil {
			writeMutationError(w, r.logger, err, "put "+config.Name, spec.Path)
			return
		}
		if err = r.afterPolicyResourceMutation(req, spec, payload, current, found); err != nil {
			r.logger.Error("post policy resource put failed", zap.String("path", spec.Path), zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		responseBody := resource.Payload
		if config.Decorate != nil {
			var decorated bool
			responseBody, decorated = config.Decorate(r, req, resource)
			if !decorated {
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
		}
		writeRawOKJSON(w, r.logger, responseBody)
	}
}

func (r *router) handlePolicyResourcePatch(config policyResourceConfig) routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		spec := config.Spec(req)
		if !r.requireOptionalParent(w, req, config) {
			return
		}
		body, payload, ok := readAndValidateJSONObject(w, req, r.logger)
		if !ok || !config.Validate(w, r.logger, payload) {
			return
		}
		operation := appsqlite.ResourceOperationCreate
		if _, found, err := r.store.Read(req.Context(), appsqlite.ReadOptions{Path: spec.Path}); err != nil {
			r.logger.Error("read policy resource before patch failed", zap.String("path", spec.Path), zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		} else if found {
			operation = appsqlite.ResourceOperationPatch
		}
		_, err := r.store.Mutate(req.Context(), appsqlite.Mutation{
			Spec:          spec,
			Body:          body,
			Username:      requestUsername(req),
			Operation:     operation,
			RequestPath:   req.URL.Path,
			RouteTemplate: config.RouteTemplate,
			StatusCode:    http.StatusOK,
		})
		if err != nil {
			writeMutationError(w, r.logger, err, "patch "+config.Name, spec.Path)
			return
		}
		if err = r.afterPolicyResourceMutation(req, spec, payload, appsqlite.StoredResource{}, false); err != nil {
			r.logger.Error("post policy resource patch failed", zap.String("path", spec.Path), zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func (r *router) handlePolicyResourceDelete(config policyResourceConfig) routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		spec := config.Spec(req)
		if !r.requireOptionalParent(w, req, config) || !r.requireLiveResource(w, req, spec.Path) {
			return
		}
		_, err := r.store.Mutate(req.Context(), appsqlite.Mutation{
			Spec:          spec,
			Username:      requestUsername(req),
			Operation:     appsqlite.ResourceOperationDelete,
			RequestPath:   req.URL.Path,
			RouteTemplate: config.RouteTemplate,
			StatusCode:    http.StatusOK,
		})
		if err != nil {
			writeMutationError(w, r.logger, err, "delete "+config.Name, spec.Path)
			return
		}
		if err = r.afterPolicyResourceDelete(req, spec); err != nil {
			r.logger.Error("post policy resource delete failed", zap.String("path", spec.Path), zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func (r *router) handlePolicyResourceRevise(config policyResourceConfig) routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		spec := config.Spec(req)
		if !r.requireOptionalParent(w, req, config) || !r.requireLiveResource(w, req, spec.Path) {
			return
		}
		body, payload, ok := readAndValidateJSONObject(w, req, r.logger)
		if !ok || !config.Validate(w, r.logger, payload) {
			return
		}
		resource, err := r.store.Mutate(req.Context(), appsqlite.Mutation{
			Spec:          spec,
			Body:          body,
			Username:      requestUsername(req),
			Operation:     appsqlite.ResourceOperationRevise,
			RequestPath:   req.URL.Path,
			RouteTemplate: config.RouteTemplate,
			Action:        "revise",
			StatusCode:    http.StatusOK,
		})
		if err != nil {
			writeMutationError(w, r.logger, err, "revise "+config.Name, spec.Path)
			return
		}
		if err = r.afterPolicyResourceMutation(req, spec, payload, appsqlite.StoredResource{}, false); err != nil {
			r.logger.Error("post policy resource revise failed", zap.String("path", spec.Path), zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		responseBody := resource.Payload
		if config.Decorate != nil {
			var decorated bool
			responseBody, decorated = config.Decorate(r, req, resource)
			if !decorated {
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
		}
		writeRawOKJSON(w, r.logger, responseBody)
	}
}

func (r *router) requireOptionalParent(w http.ResponseWriter, req *http.Request, config policyResourceConfig) bool {
	if config.Parent == nil {
		return true
	}
	parent := config.Parent(req)
	return r.requireLiveResource(w, req, parent.Path)
}

func (r *router) readLiveResource(
	w http.ResponseWriter,
	req *http.Request,
	path string,
	action string,
) (appsqlite.StoredResource, bool) {
	resource, found, err := r.store.Read(req.Context(), appsqlite.ReadOptions{Path: path})
	if err != nil {
		r.logger.Error(action+" failed", zap.String("path", path), zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return appsqlite.StoredResource{}, false
	}
	if !found {
		http.NotFound(w, req)
		return appsqlite.StoredResource{}, false
	}
	return resource, true
}

func (r *router) afterPolicyResourceMutation(
	req *http.Request,
	spec appsqlite.ResourceSpec,
	payload map[string]any,
	_ appsqlite.StoredResource,
	_ bool,
) error {
	if spec.ResourceType != securityPolicyResourceType {
		return nil
	}
	if _, hasRules := payload["rules"]; !hasRules {
		return nil
	}
	return r.replaceSecurityPolicyRules(req, spec, payload)
}

func (r *router) afterPolicyResourceDelete(req *http.Request, spec appsqlite.ResourceSpec) error {
	if spec.ResourceType != securityPolicyResourceType {
		return nil
	}
	rules, err := r.store.List(req.Context(), appsqlite.ListOptions{
		CollectionKey: policySecurityRulesCollectionKey,
		ParentPath:    spec.Path,
	})
	if err != nil {
		return fmt.Errorf("list security policy child rules: %w", err)
	}
	for _, rule := range rules {
		ruleSpec := securityRuleSpecFromPolicyPath(spec.Path, lastPathPart(rule.Path))
		if _, err = r.store.Mutate(req.Context(), appsqlite.Mutation{
			Spec:          ruleSpec,
			Username:      requestUsername(req),
			Operation:     appsqlite.ResourceOperationDelete,
			RequestPath:   req.URL.Path,
			RouteTemplate: securityRuleItemRouteTemplate,
			StatusCode:    http.StatusOK,
		}); err != nil {
			return fmt.Errorf("delete child rule %q: %w", rule.Path, err)
		}
	}
	return nil
}

func writePolicyListResult(w http.ResponseWriter, logger *zap.Logger, results []json.RawMessage) {
	writeOKJSON(w, logger, listResult{
		Results:       results,
		ResultCount:   len(results),
		SortBy:        "display_name",
		SortAscending: true,
	})
}

func resourceSpec(
	collectionKey string,
	resourceType string,
	parentPath string,
	childSegment string,
	id string,
) appsqlite.ResourceSpec {
	return appsqlite.ResourceSpec{
		APIFamily:     appsqlite.ResourceAPIFamilyPolicy,
		CollectionKey: collectionKey,
		Kind:          resourceType,
		ResourceType:  resourceType,
		Path:          parentPath + "/" + childSegment + "/" + id,
		ParentPath:    parentPath,
		RelativePath:  id,
	}
}

func validateCommonPolicyPayload(w http.ResponseWriter, payload map[string]any, resourceType string) bool {
	if !validateOptionalResourceType(w, payload, resourceType) {
		return false
	}
	if !validateOptionalStringLength(w, payload, "display_name", maxNSXDisplayNameLength) {
		return false
	}
	if !validateOptionalStringLength(w, payload, "description", maxNSXDescriptionLength) {
		return false
	}
	if !validateOptionalArrayMax(w, payload, "tags", maxNSXTags) {
		return false
	}
	return true
}

func validateOptionalStringEnum(w http.ResponseWriter, payload map[string]any, key string, allowed ...string) bool {
	value, ok := payload[key]
	if !ok {
		return true
	}
	got, ok := value.(string)
	if !ok {
		http.Error(w, key+" must be a string", http.StatusBadRequest)
		return false
	}
	if slices.Contains(allowed, got) {
		return true
	}
	http.Error(w, key+" is not allowed", http.StatusBadRequest)
	return false
}

func validateOptionalIntegerRange(
	w http.ResponseWriter,
	payload map[string]any,
	key string,
	minValue int,
	maxValue int,
) bool {
	value, ok := payload[key]
	if !ok {
		return true
	}
	got, ok := value.(float64)
	if !ok || float64(int(got)) != got {
		http.Error(w, key+" must be an integer", http.StatusBadRequest)
		return false
	}
	asInt := int(got)
	if asInt < minValue || asInt > maxValue {
		http.Error(w, key+" is out of range", http.StatusBadRequest)
		return false
	}
	return true
}

func validateOptionalStringArrayMax(w http.ResponseWriter, payload map[string]any, key string, maxItems int) bool {
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
	for _, item := range items {
		if _, ok = item.(string); !ok {
			http.Error(w, key+" entries must be strings", http.StatusBadRequest)
			return false
		}
	}
	return true
}

func validateAnyNotMixed(w http.ResponseWriter, payload map[string]any, key string) bool {
	value, ok := payload[key]
	if !ok {
		return true
	}
	items, ok := value.([]any)
	if !ok {
		http.Error(w, key+" must be an array", http.StatusBadRequest)
		return false
	}
	hasAny := false
	for _, item := range items {
		if item == "ANY" {
			hasAny = true
		}
	}
	if hasAny && len(items) > 1 {
		http.Error(w, "ANY must not be combined with other "+key+" entries", http.StatusBadRequest)
		return false
	}
	return true
}

func validateOptionalSubnets(w http.ResponseWriter, payload map[string]any) bool {
	value, ok := payload["subnets"]
	if !ok {
		return true
	}
	items, ok := value.([]any)
	if !ok {
		http.Error(w, "subnets must be an array", http.StatusBadRequest)
		return false
	}
	if len(items) > 1 {
		http.Error(w, "subnets exceeds maximum items", http.StatusBadRequest)
		return false
	}
	for _, item := range items {
		subnet, isObject := item.(map[string]any)
		if !isObject {
			http.Error(w, "subnets entries must be objects", http.StatusBadRequest)
			return false
		}
		if !validateOptionalGatewayAddress(w, subnet) || !validateOptionalDHCPRanges(w, subnet) {
			return false
		}
	}
	return true
}

func validateOptionalGatewayAddress(w http.ResponseWriter, subnet map[string]any) bool {
	gateway, hasGateway := subnet["gateway_address"]
	if !hasGateway {
		return true
	}
	gatewayText, typeOK := gateway.(string)
	if !typeOK {
		http.Error(w, "gateway_address must be a string", http.StatusBadRequest)
		return false
	}
	if _, err := netip.ParsePrefix(gatewayText); err != nil {
		http.Error(w, "gateway_address must be an IP prefix", http.StatusBadRequest)
		return false
	}
	return true
}

func validateOptionalDHCPRanges(w http.ResponseWriter, subnet map[string]any) bool {
	ranges, hasRanges := subnet["dhcp_ranges"]
	if !hasRanges {
		return true
	}
	rangeItems, typeOK := ranges.([]any)
	if !typeOK || len(rangeItems) == 0 || len(rangeItems) > 99 {
		http.Error(w, "dhcp_ranges must contain 1 to 99 entries", http.StatusBadRequest)
		return false
	}
	return true
}

func stateFromRealization(status string) string {
	if status == "IN_PROGRESS" {
		return "in_progress"
	}
	return "success"
}

func segmentStatePayload(resource appsqlite.StoredResource) map[string]any {
	return map[string]any{
		"segment_path": resource.Path,
		"state":        stateFromRealization(resource.RealizationStatus),
		"details":      []any{},
	}
}

func sortedRulePayloads(resources []appsqlite.StoredResource) ([]any, error) {
	rules := make([]map[string]any, 0, len(resources))
	for _, resource := range resources {
		payload, err := decodePayloadObject(resource.Payload)
		if err != nil {
			return nil, err
		}
		rules = append(rules, payload)
	}
	sort.SliceStable(rules, func(left int, right int) bool {
		leftSeq := sortableSequence(rules[left])
		rightSeq := sortableSequence(rules[right])
		if leftSeq != rightSeq {
			return leftSeq < rightSeq
		}
		leftName := sortableName(rules[left])
		rightName := sortableName(rules[right])
		if leftName != rightName {
			return leftName < rightName
		}
		return fmt.Sprint(rules[left]["id"]) < fmt.Sprint(rules[right]["id"])
	})
	result := make([]any, 0, len(rules))
	for _, rule := range rules {
		result = append(result, rule)
	}
	return result, nil
}

func sortableSequence(payload map[string]any) int {
	value, ok := payload["sequence_number"].(float64)
	if !ok {
		return 0
	}
	return int(value)
}

func sortableName(payload map[string]any) string {
	if value, ok := payload["display_name"].(string); ok {
		return strings.ToLower(value)
	}
	if value, ok := payload["id"].(string); ok {
		return strings.ToLower(value)
	}
	return ""
}

func lastPathPart(path string) string {
	index := strings.LastIndex(path, "/")
	if index < 0 || index == len(path)-1 {
		return path
	}
	return path[index+1:]
}
