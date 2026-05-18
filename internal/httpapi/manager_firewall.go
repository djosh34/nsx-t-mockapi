package httpapi

import (
	"encoding/json"
	"net/http"

	appsqlite "nsx-t-mockapi/internal/sqlite"

	"go.uber.org/zap"
)

const (
	managerFirewallSectionsCollectionKey    = "manager.firewall.sections"
	managerFirewallRulesCollectionKey       = "manager.firewall.rules"
	managerFirewallSectionResourceType      = "FirewallSection"
	managerFirewallRuleResourceType         = "FirewallRule"
	managerFirewallSectionsRouteTemplate    = "/api/v1/firewall/sections"
	managerFirewallSectionItemRouteTemplate = "/api/v1/firewall/sections/{section-id}"
	managerFirewallRulesRouteTemplate       = "/api/v1/firewall/sections/{section-id}/rules"
	managerFirewallRuleItemRouteTemplate    = "/api/v1/firewall/sections/{section-id}/rules/{rule-id}"
	managerFirewallRuleStatsRouteTemplate   = "/api/v1/firewall/sections/{section-id}/rules/stats"
	maxManagerFirewallRuleList              = 1000
	maxManagerFirewallRuleTagLength         = 32
)

type managerFirewallSectionWithRulesInput struct {
	sectionID   string
	sectionBody json.RawMessage
	rules       []map[string]any
	bodySize    int
}

func (r *router) handleManagerFirewallSectionList() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		r.logger.Debug("listing manager firewall sections")
		resources, err := r.store.List(req.Context(), appsqlite.ListOptions{
			CollectionKey: managerFirewallSectionsCollectionKey,
			ParentPath:    managerFirewallSectionsRouteTemplate,
		})
		if err != nil {
			r.logger.Error("list manager firewall sections failed", zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		results := make([]json.RawMessage, 0, len(resources))
		for _, resource := range resources {
			results = append(results, resource.Payload)
		}
		writeManagerListResult(w, r.logger, results)
	}
}

func (r *router) handleManagerFirewallSectionCreate() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		body, payload, ok := readAndValidateJSONObject(w, req, r.logger)
		if !ok || !validateManagerFirewallSectionPayload(w, r.logger, payload) {
			return
		}
		sectionID, idOK := managerPayloadID(w, payload)
		if !idOK {
			return
		}
		if sectionID == "" {
			var err error
			sectionID, err = generatedManagerID()
			if err != nil {
				r.logger.Error("generate manager firewall section id failed", zap.Error(err))
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
		}
		spec := managerFirewallSectionSpec(sectionID)
		resource, err := r.store.Mutate(req.Context(), appsqlite.Mutation{
			Spec:          spec,
			Body:          body,
			Username:      requestUsername(req),
			Operation:     appsqlite.ResourceOperationCreate,
			RequestPath:   req.URL.Path,
			RouteTemplate: managerFirewallSectionsRouteTemplate,
			StatusCode:    http.StatusCreated,
		})
		if err != nil {
			writeManagerMutationError(w, r.logger, err, "create manager firewall section", spec.Path)
			return
		}
		responseBody, decorated := r.managerFirewallSectionWithRuleCount(req, resource)
		if !decorated {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		writeRawCreatedJSON(w, r.logger, responseBody)
	}
}

func (r *router) handleManagerFirewallSectionGet() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		spec := managerFirewallSectionSpec(routeParam(req, "section-id"))
		resource, found := r.readLiveResource(w, req, spec.Path, "read manager firewall section")
		if !found {
			return
		}
		responseBody, decorated := r.managerFirewallSectionWithRuleCount(req, resource)
		if !decorated {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		writeRawOKJSON(w, r.logger, responseBody)
	}
}

func (r *router) handleManagerFirewallSectionCreateWithRules() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		input, ok := r.readManagerFirewallSectionWithRulesInput(w, req)
		if !ok {
			return
		}
		spec := managerFirewallSectionSpec(input.sectionID)
		resource, err := r.store.Mutate(req.Context(), appsqlite.Mutation{
			Spec:          spec,
			Body:          input.sectionBody,
			Username:      requestUsername(req),
			Operation:     appsqlite.ResourceOperationCreate,
			RequestPath:   req.URL.Path,
			RouteTemplate: managerFirewallSectionsRouteTemplate,
			Action:        "create_with_rules",
			StatusCode:    http.StatusCreated,
		})
		if err != nil {
			writeManagerMutationError(w, r.logger, err, "create manager firewall section with rules", spec.Path)
			return
		}
		_, created := r.createManagerFirewallRules(w, req, spec.Path, input.rules, managerFirewallRulesRouteTemplate)
		if !created {
			return
		}
		responseBody, decorated := r.managerFirewallSectionWithRules(req, resource)
		if !decorated {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		r.logger.Debug(
			"accepted manager firewall section create_with_rules",
			zap.String("path", spec.Path),
			zap.Int("rule_count", len(input.rules)),
			zap.Int("body_size", input.bodySize),
		)
		writeRawCreatedJSON(w, r.logger, responseBody)
	}
}

func (r *router) handleManagerFirewallSectionUpdate() routeHandler {
	return r.handleManagerFirewallSectionWrite(
		appsqlite.ResourceOperationUpdate,
		"",
		managerFirewallSectionItemRouteTemplate,
	)
}

func (r *router) readManagerFirewallSectionWithRulesInput(
	w http.ResponseWriter,
	req *http.Request,
) (managerFirewallSectionWithRulesInput, bool) {
	body, payload, ok := readAndValidateJSONObject(w, req, r.logger)
	if !ok || !validateManagerFirewallSectionPayload(w, r.logger, payload) {
		return managerFirewallSectionWithRulesInput{}, false
	}
	rules, rulesOK := validateManagerFirewallRuleListPayload(w, r.logger, payload)
	if !rulesOK {
		return managerFirewallSectionWithRulesInput{}, false
	}
	sectionID, idOK := managerPayloadID(w, payload)
	if !idOK {
		return managerFirewallSectionWithRulesInput{}, false
	}
	if sectionID == "" {
		var err error
		sectionID, err = generatedManagerID()
		if err != nil {
			r.logger.Error("generate manager firewall section id failed", zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return managerFirewallSectionWithRulesInput{}, false
		}
	}
	sectionBody, marshalOK := managerSectionBodyWithoutRules(w, r.logger, payload)
	if !marshalOK {
		return managerFirewallSectionWithRulesInput{}, false
	}
	return managerFirewallSectionWithRulesInput{
		sectionID:   sectionID,
		sectionBody: sectionBody,
		rules:       rules,
		bodySize:    len(body),
	}, true
}

func (r *router) handleManagerFirewallSectionRevise() routeHandler {
	return r.handleManagerFirewallSectionWrite(
		appsqlite.ResourceOperationRevise,
		"revise",
		managerFirewallSectionItemRouteTemplate,
	)
}

func (r *router) handleManagerFirewallSectionListWithRules() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		spec := managerFirewallSectionSpec(routeParam(req, "section-id"))
		resource, found := r.readLiveResource(w, req, spec.Path, "read manager firewall section with rules")
		if !found {
			return
		}
		responseBody, decorated := r.managerFirewallSectionWithRules(req, resource)
		if !decorated {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		writeRawOKJSON(w, r.logger, responseBody)
	}
}

func (r *router) handleManagerFirewallSectionUpdateWithRules(action string) routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		spec := managerFirewallSectionSpec(routeParam(req, "section-id"))
		if !r.requireLiveResource(w, req, spec.Path) {
			return
		}
		_, payload, ok := readAndValidateJSONObject(w, req, r.logger)
		if !ok || !validateManagerFirewallSectionPayload(w, r.logger, payload) {
			return
		}
		rules, rulesOK := validateManagerFirewallRuleListPayload(w, r.logger, payload)
		if !rulesOK {
			return
		}
		sectionBody, marshalOK := managerSectionBodyWithoutRules(w, r.logger, payload)
		if !marshalOK {
			return
		}
		resource, err := r.store.Mutate(req.Context(), appsqlite.Mutation{
			Spec:            spec,
			Body:            sectionBody,
			Username:        requestUsername(req),
			Operation:       appsqlite.ResourceOperationUpdate,
			EnforceRevision: true,
			RequestPath:     req.URL.Path,
			RouteTemplate:   managerFirewallSectionItemRouteTemplate,
			Action:          action,
			StatusCode:      http.StatusOK,
		})
		if err != nil {
			writeManagerMutationError(w, r.logger, err, "update manager firewall section with rules", spec.Path)
			return
		}
		if !r.replaceManagerFirewallRules(w, req, spec.Path, rules) {
			return
		}
		responseBody, decorated := r.managerFirewallSectionWithRules(req, resource)
		if !decorated {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		writeRawOKJSON(w, r.logger, responseBody)
	}
}

func (r *router) handleManagerFirewallSectionWrite(
	operation string,
	action string,
	routeTemplate string,
) routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		spec := managerFirewallSectionSpec(routeParam(req, "section-id"))
		if !r.requireLiveResource(w, req, spec.Path) {
			return
		}
		body, payload, ok := readAndValidateJSONObject(w, req, r.logger)
		if !ok || !validateManagerFirewallSectionPayload(w, r.logger, payload) {
			return
		}
		resource, err := r.store.Mutate(req.Context(), appsqlite.Mutation{
			Spec:            spec,
			Body:            body,
			Username:        requestUsername(req),
			Operation:       operation,
			EnforceRevision: true,
			RequestPath:     req.URL.Path,
			RouteTemplate:   routeTemplate,
			Action:          action,
			StatusCode:      http.StatusOK,
		})
		if err != nil {
			writeManagerMutationError(w, r.logger, err, "write manager firewall section", spec.Path)
			return
		}
		responseBody, decorated := r.managerFirewallSectionWithRuleCount(req, resource)
		if !decorated {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		writeRawOKJSON(w, r.logger, responseBody)
	}
}

func (r *router) handleManagerFirewallSectionDelete() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		spec := managerFirewallSectionSpec(routeParam(req, "section-id"))
		if !r.requireLiveResource(w, req, spec.Path) {
			return
		}
		childRules, ok := r.managerFirewallRules(w, req, spec.Path)
		if !ok {
			return
		}
		if len(childRules) > 0 && req.URL.Query().Get("cascade") != "true" {
			http.Error(w, "firewall section has rules; use cascade=true", http.StatusConflict)
			return
		}
		for _, childRule := range childRules {
			childSpec := managerFirewallRuleSpecFromSectionPath(spec.Path, lastPathPart(childRule.Path))
			if _, err := r.store.Mutate(req.Context(), appsqlite.Mutation{
				Spec:          childSpec,
				Username:      requestUsername(req),
				Operation:     appsqlite.ResourceOperationDelete,
				RequestPath:   req.URL.Path,
				RouteTemplate: managerFirewallRuleItemRouteTemplate,
				StatusCode:    http.StatusOK,
			}); err != nil {
				writeManagerMutationError(w, r.logger, err, "cascade delete manager firewall rule", childSpec.Path)
				return
			}
		}
		_, err := r.store.Mutate(req.Context(), appsqlite.Mutation{
			Spec:          spec,
			Username:      requestUsername(req),
			Operation:     appsqlite.ResourceOperationDelete,
			RequestPath:   req.URL.Path,
			RouteTemplate: managerFirewallSectionItemRouteTemplate,
			StatusCode:    http.StatusOK,
		})
		if err != nil {
			writeManagerMutationError(w, r.logger, err, "delete manager firewall section", spec.Path)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func (r *router) handleManagerFirewallRuleList() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		section := managerFirewallSectionSpec(routeParam(req, "section-id"))
		if !r.requireLiveResource(w, req, section.Path) {
			return
		}
		resources, ok := r.managerFirewallRules(w, req, section.Path)
		if !ok {
			return
		}
		results := make([]json.RawMessage, 0, len(resources))
		for _, resource := range resources {
			results = append(results, resource.Payload)
		}
		writeManagerListResult(w, r.logger, results)
	}
}

func (r *router) handleManagerFirewallRuleCreate() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		section := managerFirewallSectionSpec(routeParam(req, "section-id"))
		if !r.requireLiveResource(w, req, section.Path) {
			return
		}
		body, payload, ok := readAndValidateJSONObject(w, req, r.logger)
		if !ok || !validateManagerFirewallRulePayload(w, r.logger, payload) {
			return
		}
		ruleID, idOK := managerPayloadID(w, payload)
		if !idOK {
			return
		}
		if ruleID == "" {
			var err error
			ruleID, err = generatedManagerID()
			if err != nil {
				r.logger.Error("generate manager firewall rule id failed", zap.String("section_path", section.Path), zap.Error(err))
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
		}
		spec := managerFirewallRuleSpec(routeParam(req, "section-id"), ruleID)
		resource, err := r.store.Mutate(req.Context(), appsqlite.Mutation{
			Spec:          spec,
			Body:          body,
			Username:      requestUsername(req),
			Operation:     appsqlite.ResourceOperationCreate,
			RequestPath:   req.URL.Path,
			RouteTemplate: managerFirewallRulesRouteTemplate,
			StatusCode:    http.StatusOK,
		})
		if err != nil {
			writeManagerMutationError(w, r.logger, err, "create manager firewall rule", spec.Path)
			return
		}
		writeRawOKJSON(w, r.logger, resource.Payload)
	}
}

func (r *router) handleManagerFirewallRuleGet() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		section := managerFirewallSectionSpec(routeParam(req, "section-id"))
		if !r.requireLiveResource(w, req, section.Path) {
			return
		}
		spec := managerFirewallRuleSpec(routeParam(req, "section-id"), routeParam(req, "rule-id"))
		resource, found := r.readLiveResource(w, req, spec.Path, "read manager firewall rule")
		if !found {
			return
		}
		writeRawOKJSON(w, r.logger, resource.Payload)
	}
}

func (r *router) handleManagerFirewallRuleCreateMultiple() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		section := managerFirewallSectionSpec(routeParam(req, "section-id"))
		if !r.requireLiveResource(w, req, section.Path) {
			return
		}
		_, payload, ok := readAndValidateJSONObject(w, req, r.logger)
		if !ok {
			return
		}
		rules, rulesOK := validateManagerFirewallRuleListPayload(w, r.logger, payload)
		if !rulesOK {
			return
		}
		created, createOK := r.createManagerFirewallRules(w, req, section.Path, rules, managerFirewallRulesRouteTemplate)
		if !createOK {
			return
		}
		writeOKJSON(w, r.logger, map[string]any{"rules": created})
	}
}

func (r *router) handleManagerFirewallRuleStats() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		section := managerFirewallSectionSpec(routeParam(req, "section-id"))
		if !r.requireLiveResource(w, req, section.Path) {
			return
		}
		stats := map[string]any{
			"section_id": routeParam(req, "section-id"),
			"statistics": map[string]any{
				"results":      []any{},
				"result_count": 0,
			},
		}
		raw, err := json.Marshal(stats)
		if err != nil {
			r.logger.Error("marshal manager firewall stats failed", zap.String("section_path", section.Path), zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		writeOKJSON(w, r.logger, listResult{Results: []json.RawMessage{raw}, ResultCount: 1})
	}
}

func (r *router) handleManagerFirewallRuleUpdate() routeHandler {
	return r.handleManagerFirewallRuleWrite(appsqlite.ResourceOperationUpdate, "")
}

func (r *router) handleManagerFirewallRuleRevise() routeHandler {
	return r.handleManagerFirewallRuleWrite(appsqlite.ResourceOperationRevise, "revise")
}

func (r *router) handleManagerFirewallRuleWrite(operation string, action string) routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		section := managerFirewallSectionSpec(routeParam(req, "section-id"))
		if !r.requireLiveResource(w, req, section.Path) {
			return
		}
		spec := managerFirewallRuleSpec(routeParam(req, "section-id"), routeParam(req, "rule-id"))
		if !r.requireLiveResource(w, req, spec.Path) {
			return
		}
		body, payload, ok := readAndValidateJSONObject(w, req, r.logger)
		if !ok || !validateManagerFirewallRulePayload(w, r.logger, payload) {
			return
		}
		resource, err := r.store.Mutate(req.Context(), appsqlite.Mutation{
			Spec:            spec,
			Body:            body,
			Username:        requestUsername(req),
			Operation:       operation,
			EnforceRevision: true,
			RequestPath:     req.URL.Path,
			RouteTemplate:   managerFirewallRuleItemRouteTemplate,
			Action:          action,
			StatusCode:      http.StatusOK,
		})
		if err != nil {
			writeManagerMutationError(w, r.logger, err, "write manager firewall rule", spec.Path)
			return
		}
		writeRawOKJSON(w, r.logger, resource.Payload)
	}
}

func (r *router) handleManagerFirewallRuleDelete() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		section := managerFirewallSectionSpec(routeParam(req, "section-id"))
		if !r.requireLiveResource(w, req, section.Path) {
			return
		}
		spec := managerFirewallRuleSpec(routeParam(req, "section-id"), routeParam(req, "rule-id"))
		if !r.requireLiveResource(w, req, spec.Path) {
			return
		}
		_, err := r.store.Mutate(req.Context(), appsqlite.Mutation{
			Spec:          spec,
			Username:      requestUsername(req),
			Operation:     appsqlite.ResourceOperationDelete,
			RequestPath:   req.URL.Path,
			RouteTemplate: managerFirewallRuleItemRouteTemplate,
			StatusCode:    http.StatusOK,
		})
		if err != nil {
			writeManagerMutationError(w, r.logger, err, "delete manager firewall rule", spec.Path)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func managerFirewallSectionSpec(sectionID string) appsqlite.ResourceSpec {
	return appsqlite.ResourceSpec{
		APIFamily:     appsqlite.ResourceAPIFamilyManager,
		CollectionKey: managerFirewallSectionsCollectionKey,
		Kind:          managerFirewallSectionResourceType,
		ResourceType:  managerFirewallSectionResourceType,
		Path:          managerFirewallSectionsRouteTemplate + "/" + sectionID,
		ParentPath:    managerFirewallSectionsRouteTemplate,
		RelativePath:  sectionID,
	}
}

func managerFirewallRuleSpec(sectionID string, ruleID string) appsqlite.ResourceSpec {
	return managerFirewallRuleSpecFromSectionPath(managerFirewallSectionSpec(sectionID).Path, ruleID)
}

func managerFirewallRuleSpecFromSectionPath(sectionPath string, ruleID string) appsqlite.ResourceSpec {
	return appsqlite.ResourceSpec{
		APIFamily:     appsqlite.ResourceAPIFamilyManager,
		CollectionKey: managerFirewallRulesCollectionKey,
		Kind:          managerFirewallRuleResourceType,
		ResourceType:  managerFirewallRuleResourceType,
		Path:          sectionPath + "/rules/" + ruleID,
		ParentPath:    sectionPath,
		RelativePath:  ruleID,
	}
}

func (r *router) managerFirewallSectionWithRuleCount(
	req *http.Request,
	resource appsqlite.StoredResource,
) (json.RawMessage, bool) {
	payload, err := decodePayloadObject(resource.Payload)
	if err != nil {
		r.logger.Error("decode manager firewall section failed", zap.String("path", resource.Path), zap.Error(err))
		return nil, false
	}
	rules, ok := r.managerFirewallRules(nil, req, resource.Path)
	if !ok {
		return nil, false
	}
	payload["rule_count"] = len(rules)
	body, err := json.Marshal(payload)
	if err != nil {
		r.logger.Error("marshal manager firewall section failed", zap.String("path", resource.Path), zap.Error(err))
		return nil, false
	}
	r.logger.Debug(
		"decorated manager firewall section",
		zap.String("path", resource.Path),
		zap.Int("rule_count", len(rules)),
	)
	return body, true
}

func (r *router) managerFirewallRules(
	w http.ResponseWriter,
	req *http.Request,
	sectionPath string,
) ([]appsqlite.StoredResource, bool) {
	rules, err := r.store.List(req.Context(), appsqlite.ListOptions{
		CollectionKey: managerFirewallRulesCollectionKey,
		ParentPath:    sectionPath,
	})
	if err != nil {
		r.logger.Error("list manager firewall rules failed", zap.String("section_path", sectionPath), zap.Error(err))
		if w != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		return nil, false
	}
	return rules, true
}

func (r *router) managerFirewallSectionWithRules(
	req *http.Request,
	resource appsqlite.StoredResource,
) (json.RawMessage, bool) {
	payload, err := decodePayloadObject(resource.Payload)
	if err != nil {
		r.logger.Error("decode manager firewall section failed", zap.String("path", resource.Path), zap.Error(err))
		return nil, false
	}
	rules, ok := r.managerFirewallRules(nil, req, resource.Path)
	if !ok {
		return nil, false
	}
	sortedRules, err := sortedRulePayloads(rules)
	if err != nil {
		r.logger.Error("sort manager firewall rules failed", zap.String("path", resource.Path), zap.Error(err))
		return nil, false
	}
	payload["rules"] = sortedRules
	payload["rule_count"] = len(sortedRules)
	body, err := json.Marshal(payload)
	if err != nil {
		r.logger.Error(
			"marshal manager firewall section with rules failed",
			zap.String("path", resource.Path),
			zap.Error(err),
		)
		return nil, false
	}
	return body, true
}

func (r *router) createManagerFirewallRules(
	w http.ResponseWriter,
	req *http.Request,
	sectionPath string,
	rules []map[string]any,
	routeTemplate string,
) ([]map[string]any, bool) {
	created := make([]map[string]any, 0, len(rules))
	for index, rule := range rules {
		ruleID := embeddedRuleID(rule, index)
		body, err := json.Marshal(rule)
		if err != nil {
			r.logger.Error("marshal manager firewall rule failed", zap.String("section_path", sectionPath), zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return nil, false
		}
		spec := managerFirewallRuleSpecFromSectionPath(sectionPath, ruleID)
		resource, err := r.store.Mutate(req.Context(), appsqlite.Mutation{
			Spec:          spec,
			Body:          body,
			Username:      requestUsername(req),
			Operation:     appsqlite.ResourceOperationCreate,
			RequestPath:   req.URL.Path,
			RouteTemplate: routeTemplate,
			StatusCode:    http.StatusOK,
		})
		if err != nil {
			writeManagerMutationError(w, r.logger, err, "create manager firewall rule", spec.Path)
			return nil, false
		}
		payload, err := decodePayloadObject(resource.Payload)
		if err != nil {
			r.logger.Error("decode created manager firewall rule failed", zap.String("path", spec.Path), zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return nil, false
		}
		created = append(created, payload)
	}
	return created, true
}

func (r *router) replaceManagerFirewallRules(
	w http.ResponseWriter,
	req *http.Request,
	sectionPath string,
	rules []map[string]any,
) bool {
	existing, ok := r.managerFirewallRules(w, req, sectionPath)
	if !ok {
		return false
	}
	for _, resource := range existing {
		spec := managerFirewallRuleSpecFromSectionPath(sectionPath, lastPathPart(resource.Path))
		if _, err := r.store.Mutate(req.Context(), appsqlite.Mutation{
			Spec:          spec,
			Username:      requestUsername(req),
			Operation:     appsqlite.ResourceOperationDelete,
			RequestPath:   req.URL.Path,
			RouteTemplate: managerFirewallRuleItemRouteTemplate,
			StatusCode:    http.StatusOK,
		}); err != nil {
			writeManagerMutationError(w, r.logger, err, "replace delete manager firewall rule", spec.Path)
			return false
		}
	}
	_, created := r.createManagerFirewallRules(w, req, sectionPath, rules, managerFirewallRulesRouteTemplate)
	return created
}

func validateManagerFirewallSectionPayload(w http.ResponseWriter, logger *zap.Logger, payload map[string]any) bool {
	if !validateCommonPolicyPayload(w, payload, managerFirewallSectionResourceType) {
		return false
	}
	if !validateRequiredStringEnum(w, payload, "section_type", "LAYER2", "LAYER3", "L3REDIRECT", "IDS") {
		return false
	}
	if !validateRequiredBool(w, payload, "stateful") {
		return false
	}
	if !validateOptionalArrayMax(w, payload, "applied_tos", maxPolicyPathList) {
		return false
	}
	logger.Debug(
		"validated manager firewall section payload",
		zap.String("resource_type", managerFirewallSectionResourceType),
	)
	return true
}

func validateManagerFirewallRulePayload(w http.ResponseWriter, logger *zap.Logger, payload map[string]any) bool {
	if !validateCommonPolicyPayload(w, payload, managerFirewallRuleResourceType) {
		return false
	}
	if !validateRequiredStringEnum(
		w,
		payload,
		"action",
		"ALLOW",
		"DROP",
		"REJECT",
		"REDIRECT",
		"DO_NOT_REDIRECT",
		"DETECT",
		"ALLOW_CONTINUE",
		"DETECT_PREVENT",
	) {
		return false
	}
	if !validateOptionalStringEnum(w, payload, "direction", "IN", "OUT", "IN_OUT") {
		return false
	}
	if !validateOptionalStringEnum(w, payload, "ip_protocol", "IPV4", "IPV6", "IPV4_IPV6") {
		return false
	}
	managerRuleArrayFields := []string{
		"sources",
		"destinations",
		"services",
		"applied_tos",
		"context_profiles",
		"extended_sources",
	}
	for _, key := range managerRuleArrayFields {
		if !validateOptionalArrayMax(w, payload, key, maxPolicyPathList) {
			return false
		}
	}
	if !validateOptionalStringLength(w, payload, "notes", maxRuleNotesLength) {
		return false
	}
	if !validateOptionalStringLength(w, payload, "rule_tag", maxManagerFirewallRuleTagLength) {
		return false
	}
	logger.Debug("validated manager firewall rule payload", zap.String("resource_type", managerFirewallRuleResourceType))
	return true
}

func validateManagerFirewallRuleListPayload(
	w http.ResponseWriter,
	logger *zap.Logger,
	payload map[string]any,
) ([]map[string]any, bool) {
	value, ok := payload["rules"]
	if !ok {
		http.Error(w, "rules is required", http.StatusBadRequest)
		return nil, false
	}
	items, ok := value.([]any)
	if !ok {
		http.Error(w, "rules must be an array", http.StatusBadRequest)
		return nil, false
	}
	if len(items) > maxManagerFirewallRuleList {
		http.Error(w, "rules exceeds maximum items", http.StatusBadRequest)
		return nil, false
	}
	rules := make([]map[string]any, 0, len(items))
	for _, item := range items {
		rule, isObject := item.(map[string]any)
		if !isObject {
			http.Error(w, "rules entries must be objects", http.StatusBadRequest)
			return nil, false
		}
		if !validateManagerFirewallRulePayload(w, logger, rule) {
			return nil, false
		}
		rules = append(rules, rule)
	}
	logger.Debug("validated manager firewall rule list", zap.Int("rule_count", len(rules)))
	return rules, true
}

func managerSectionBodyWithoutRules(
	w http.ResponseWriter,
	logger *zap.Logger,
	payload map[string]any,
) (json.RawMessage, bool) {
	section := make(map[string]any, len(payload))
	for key, value := range payload {
		if key == "rules" {
			continue
		}
		section[key] = value
	}
	body, err := json.Marshal(section)
	if err != nil {
		logger.Error("marshal manager firewall section body failed", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return nil, false
	}
	return body, true
}

func writeManagerListResult(w http.ResponseWriter, logger *zap.Logger, results []json.RawMessage) {
	writeOKJSON(w, logger, listResult{
		Results:       results,
		ResultCount:   len(results),
		SortBy:        "display_name",
		SortAscending: true,
	})
}
