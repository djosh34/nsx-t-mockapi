package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	appsqlite "nsx-t-mockapi/internal/sqlite"

	"go.uber.org/zap"
)

const (
	policySecurityPoliciesCollectionKey = "policy.security_policies"
	policySecurityRulesCollectionKey    = "policy.security_rules"
	securityPolicyResourceType          = "SecurityPolicy"
	securityRuleResourceType            = "Rule"
	securityPolicyListRouteTemplate     = "/policy/api/v1/infra/domains/{domain-id}/security-policies"
	securityPolicyItemRouteTemplate     = "/policy/api/v1/infra/domains/{domain-id}/security-policies/{security-policy-id}"
	securityRuleListRouteTemplate       = "/policy/api/v1/infra/domains/{domain-id}/" +
		"security-policies/{security-policy-id}/rules"
	securityRuleItemRouteTemplate = "/policy/api/v1/infra/domains/{domain-id}/" +
		"security-policies/{security-policy-id}/rules/{rule-id}"
	maxSecurityPolicySequenceNumber = 999999
	maxRuleSequenceNumber           = 2147483647
	maxRuleNotesLength              = 2048
)

var errEmbeddedRuleInvalid = errors.New("embedded rule is not an object")

func securityPolicyConfig() policyResourceConfig {
	return policyResourceConfig{
		Name:          "security policy",
		CollectionKey: policySecurityPoliciesCollectionKey,
		ResourceType:  securityPolicyResourceType,
		RouteTemplate: securityPolicyItemRouteTemplate,
		Spec: func(req *http.Request) appsqlite.ResourceSpec {
			return securityPolicySpec(routeParam(req, "domain-id"), routeParam(req, "security-policy-id"))
		},
		Validate: validateSecurityPolicyPayload,
		Decorate: func(r *router, req *http.Request, resource appsqlite.StoredResource) (json.RawMessage, bool) {
			return r.securityPolicyWithRules(req, resource)
		},
	}
}

func securityRuleConfig() policyResourceConfig {
	return policyResourceConfig{
		Name:          "security rule",
		CollectionKey: policySecurityRulesCollectionKey,
		ResourceType:  securityRuleResourceType,
		RouteTemplate: securityRuleItemRouteTemplate,
		Spec: func(req *http.Request) appsqlite.ResourceSpec {
			return securityRuleSpec(
				routeParam(req, "domain-id"),
				routeParam(req, "security-policy-id"),
				routeParam(req, "rule-id"),
			)
		},
		Parent: func(req *http.Request) appsqlite.ResourceSpec {
			return securityPolicySpec(routeParam(req, "domain-id"), routeParam(req, "security-policy-id"))
		},
		Validate: validateRulePayload,
	}
}

func securityPolicyCollectionSpec(req *http.Request) policyCollectionSpec {
	domainID := routeParam(req, "domain-id")
	return policyCollectionSpec{
		CollectionKey: policySecurityPoliciesCollectionKey,
		ParentPath:    "/infra/domains/" + domainID,
	}
}

func securityRuleCollectionSpec(req *http.Request) policyCollectionSpec {
	return policyCollectionSpec{
		CollectionKey: policySecurityRulesCollectionKey,
		ParentPath:    securityPolicySpec(routeParam(req, "domain-id"), routeParam(req, "security-policy-id")).Path,
	}
}

func securityPolicySpec(domainID string, policyID string) appsqlite.ResourceSpec {
	return resourceSpec(
		policySecurityPoliciesCollectionKey,
		securityPolicyResourceType,
		"/infra/domains/"+domainID,
		"security-policies",
		policyID,
	)
}

func securityRuleSpec(domainID string, policyID string, ruleID string) appsqlite.ResourceSpec {
	return securityRuleSpecFromPolicyPath(securityPolicySpec(domainID, policyID).Path, ruleID)
}

func securityRuleSpecFromPolicyPath(policyPath string, ruleID string) appsqlite.ResourceSpec {
	return resourceSpec(policySecurityRulesCollectionKey, securityRuleResourceType, policyPath, "rules", ruleID)
}

func (r *router) handleSecurityRuleList() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		policy := securityPolicySpec(routeParam(req, "domain-id"), routeParam(req, "security-policy-id"))
		if !r.requireLiveResource(w, req, policy.Path) {
			return
		}
		r.handlePolicyResourceList(securityRuleCollectionSpec)(w, req)
	}
}

func (r *router) handleRuleStatistics() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		rule := securityRuleSpec(
			routeParam(req, "domain-id"),
			routeParam(req, "security-policy-id"),
			routeParam(req, "rule-id"),
		)
		if !r.requireLiveResource(w, req, rule.Path) {
			return
		}
		writeOKJSON(w, r.logger, listResult{Results: []json.RawMessage{}, ResultCount: 0})
	}
}

func (r *router) handleSecurityPolicyStatistics() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		spec := securityPolicySpec(routeParam(req, "domain-id"), routeParam(req, "security-policy-id"))
		if !r.requireLiveResource(w, req, spec.Path) {
			return
		}
		rules, err := r.store.List(req.Context(), appsqlite.ListOptions{
			CollectionKey: policySecurityRulesCollectionKey,
			ParentPath:    spec.Path,
		})
		if err != nil {
			r.logger.Error("list policy rules for statistics failed", zap.String("path", spec.Path), zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		item := map[string]any{
			"enforcement_point": "/infra/sites/default/enforcement-points/default",
			"statistics": map[string]any{
				"result_count": len(rules),
				"results":      []any{},
			},
		}
		raw, err := json.Marshal(item)
		if err != nil {
			r.logger.Error("marshal security policy statistics failed", zap.String("path", spec.Path), zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		writeOKJSON(w, r.logger, listResult{Results: []json.RawMessage{raw}, ResultCount: 1})
	}
}

func (r *router) securityPolicyWithRules(req *http.Request, resource appsqlite.StoredResource) (json.RawMessage, bool) {
	payload, err := decodePayloadObject(resource.Payload)
	if err != nil {
		r.logger.Error("decode security policy failed", zap.String("path", resource.Path), zap.Error(err))
		return nil, false
	}
	rules, err := r.store.List(req.Context(), appsqlite.ListOptions{
		CollectionKey: policySecurityRulesCollectionKey,
		ParentPath:    resource.Path,
	})
	if err != nil {
		r.logger.Error("list security policy rules failed", zap.String("path", resource.Path), zap.Error(err))
		return nil, false
	}
	if len(rules) > 0 {
		sortedRules, sortErr := sortedRulePayloads(rules)
		if sortErr != nil {
			r.logger.Error("sort security policy rules failed", zap.String("path", resource.Path), zap.Error(sortErr))
			return nil, false
		}
		payload["rules"] = sortedRules
		payload["rule_count"] = len(sortedRules)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		r.logger.Error("marshal security policy with rules failed", zap.String("path", resource.Path), zap.Error(err))
		return nil, false
	}
	return body, true
}

func (r *router) replaceSecurityPolicyRules(
	req *http.Request,
	policySpec appsqlite.ResourceSpec,
	payload map[string]any,
) error {
	rules, ok := payload["rules"].([]any)
	if !ok {
		return nil
	}
	existing, err := r.store.List(req.Context(), appsqlite.ListOptions{
		CollectionKey: policySecurityRulesCollectionKey,
		ParentPath:    policySpec.Path,
	})
	if err != nil {
		return fmt.Errorf("list current embedded rules: %w", err)
	}
	for _, resource := range existing {
		ruleID := lastPathPart(resource.Path)
		if _, err = r.store.Mutate(req.Context(), appsqlite.Mutation{
			Spec:          securityRuleSpecFromPolicyPath(policySpec.Path, ruleID),
			Username:      requestUsername(req),
			Operation:     appsqlite.ResourceOperationDelete,
			RequestPath:   req.URL.Path,
			RouteTemplate: securityRuleItemRouteTemplate,
			StatusCode:    http.StatusOK,
		}); err != nil {
			return fmt.Errorf("delete replaced rule %q: %w", resource.Path, err)
		}
	}
	for index, item := range rules {
		rulePayload, isObject := item.(map[string]any)
		if !isObject {
			return fmt.Errorf("%w: index %d", errEmbeddedRuleInvalid, index)
		}
		ruleID := embeddedRuleID(rulePayload, index)
		body, marshalErr := json.Marshal(rulePayload)
		if marshalErr != nil {
			return fmt.Errorf("marshal embedded rule %q: %w", ruleID, marshalErr)
		}
		if _, err = r.store.Mutate(req.Context(), appsqlite.Mutation{
			Spec:          securityRuleSpecFromPolicyPath(policySpec.Path, ruleID),
			Body:          body,
			Username:      requestUsername(req),
			Operation:     appsqlite.ResourceOperationCreate,
			RequestPath:   req.URL.Path,
			RouteTemplate: securityRuleItemRouteTemplate,
			StatusCode:    http.StatusOK,
		}); err != nil {
			return fmt.Errorf("create embedded rule %q: %w", ruleID, err)
		}
	}
	return nil
}

func embeddedRuleID(payload map[string]any, index int) string {
	for _, key := range []string{"id", "display_name"} {
		value, ok := payload[key].(string)
		if ok && value != "" {
			return value
		}
	}
	return fmt.Sprintf("rule-%d", index+1)
}

func validateSecurityPolicyPayload(w http.ResponseWriter, logger *zap.Logger, payload map[string]any) bool {
	if !validateCommonPolicyPayload(w, payload, securityPolicyResourceType) {
		return false
	}
	if !validateOptionalStringEnum(
		w,
		payload,
		"category",
		"Ethernet",
		"Emergency",
		"Infrastructure",
		"Environment",
		"Application",
		"SystemRules",
		"SharedPreRules",
		"LocalGatewayRules",
		"AutoServiceRules",
		"Default",
		"Rules",
	) {
		return false
	}
	if !validateOptionalIntegerRange(w, payload, "sequence_number", 0, maxSecurityPolicySequenceNumber) {
		return false
	}
	if !validateOptionalStringArrayMax(w, payload, "scope", maxPolicyPathList) {
		return false
	}
	if !validateEmbeddedRules(w, logger, payload) {
		return false
	}
	logger.Debug("validated security policy payload", zap.String("resource_type", securityPolicyResourceType))
	return true
}

func validateEmbeddedRules(w http.ResponseWriter, logger *zap.Logger, payload map[string]any) bool {
	value, ok := payload["rules"]
	if !ok {
		return true
	}
	items, ok := value.([]any)
	if !ok {
		http.Error(w, "rules must be an array", http.StatusBadRequest)
		return false
	}
	for _, item := range items {
		rule, isObject := item.(map[string]any)
		if !isObject {
			http.Error(w, "rules entries must be objects", http.StatusBadRequest)
			return false
		}
		if !validateRulePayload(w, logger, rule) {
			return false
		}
	}
	return true
}

//nolint:cyclop // Route-specific schema validation is clearer as one ordered guard list.
func validateRulePayload(w http.ResponseWriter, logger *zap.Logger, payload map[string]any) bool {
	if !validateCommonPolicyPayload(w, payload, securityRuleResourceType) {
		return false
	}
	if !validateOptionalStringEnum(w, payload, "action", "ALLOW", "DROP", "REJECT", "JUMP_TO_APPLICATION") {
		return false
	}
	if !validateOptionalStringEnum(w, payload, "direction", "IN", "OUT", "IN_OUT") {
		return false
	}
	if !validateOptionalStringEnum(w, payload, "ip_protocol", "IPV4", "IPV6", "IPV4_IPV6") {
		return false
	}
	if !validateOptionalIntegerRange(w, payload, "sequence_number", 0, maxRuleSequenceNumber) {
		return false
	}
	for _, key := range []string{"source_groups", "destination_groups", "services", "profiles", "scope"} {
		if !validateOptionalStringArrayMax(w, payload, key, maxPolicyPathList) ||
			!validateAnyNotMixed(w, payload, key) {
			return false
		}
	}
	if !validateOptionalArrayMax(w, payload, "service_entries", maxPolicyPathList) {
		return false
	}
	if !validateOptionalStringLength(w, payload, "notes", maxRuleNotesLength) {
		return false
	}
	logger.Debug("validated security rule payload", zap.String("resource_type", securityRuleResourceType))
	return true
}
