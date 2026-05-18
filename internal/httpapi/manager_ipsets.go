package httpapi

import (
	"encoding/json"
	"net/http"

	appsqlite "nsx-t-mockapi/internal/sqlite"

	"go.uber.org/zap"
)

const (
	managerIPSetsCollectionKey       = "manager.ip-sets"
	managerIPSetResourceType         = "IPSet"
	managerIPSetsRouteTemplate       = "/api/v1/ip-sets"
	managerIPSetItemRouteTemplate    = "/api/v1/ip-sets/{ip-set-id}"
	managerIPSetMembersRouteTemplate = "/api/v1/ip-sets/{ip-set-id}/members"
	maxManagerIPSetAddresses         = 4000
)

func (r *router) handleManagerIPSetList() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		resources, err := r.store.List(req.Context(), appsqlite.ListOptions{
			CollectionKey: managerIPSetsCollectionKey,
			ParentPath:    managerIPSetsRouteTemplate,
		})
		if err != nil {
			r.logger.Error("list manager ip sets failed", zap.Error(err))
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

func (r *router) handleManagerIPSetCreate() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		body, payload, ok := readAndValidateJSONObject(w, req, r.logger)
		if !ok || !validateManagerIPSetPayload(w, r.logger, payload) {
			return
		}
		ipSetID, idOK := managerPayloadID(w, payload)
		if !idOK {
			return
		}
		if ipSetID == "" {
			var err error
			ipSetID, err = generatedManagerID()
			if err != nil {
				r.logger.Error("generate manager ip set id failed", zap.Error(err))
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
		}
		spec := managerIPSetSpec(ipSetID)
		resource, err := r.store.Mutate(req.Context(), appsqlite.Mutation{
			Spec:          spec,
			Body:          body,
			Username:      requestUsername(req),
			Operation:     appsqlite.ResourceOperationCreate,
			RequestPath:   req.URL.Path,
			RouteTemplate: managerIPSetsRouteTemplate,
			StatusCode:    http.StatusCreated,
		})
		if err != nil {
			writeManagerMutationError(w, r.logger, err, "create manager ip set", spec.Path)
			return
		}
		writeRawCreatedJSON(w, r.logger, resource.Payload)
	}
}

func (r *router) handleManagerIPSetGet() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		spec := managerIPSetSpec(routeParam(req, "ip-set-id"))
		resource, found := r.readLiveResource(w, req, spec.Path, "read manager ip set")
		if !found {
			return
		}
		writeRawOKJSON(w, r.logger, resource.Payload)
	}
}

func (r *router) handleManagerIPSetUpdate() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		spec := managerIPSetSpec(routeParam(req, "ip-set-id"))
		if !r.requireLiveResource(w, req, spec.Path) {
			return
		}
		body, payload, ok := readAndValidateJSONObject(w, req, r.logger)
		if !ok || !validateManagerIPSetPayload(w, r.logger, payload) {
			return
		}
		resource, err := r.store.Mutate(req.Context(), appsqlite.Mutation{
			Spec:            spec,
			Body:            body,
			Username:        requestUsername(req),
			Operation:       appsqlite.ResourceOperationUpdate,
			EnforceRevision: true,
			RequestPath:     req.URL.Path,
			RouteTemplate:   managerIPSetItemRouteTemplate,
			StatusCode:      http.StatusOK,
		})
		if err != nil {
			writeManagerMutationError(w, r.logger, err, "update manager ip set", spec.Path)
			return
		}
		writeRawOKJSON(w, r.logger, resource.Payload)
	}
}

func (r *router) handleManagerIPSetDelete() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		spec := managerIPSetSpec(routeParam(req, "ip-set-id"))
		if !r.requireLiveResource(w, req, spec.Path) {
			return
		}
		_, err := r.store.Mutate(req.Context(), appsqlite.Mutation{
			Spec:          spec,
			Username:      requestUsername(req),
			Operation:     appsqlite.ResourceOperationDelete,
			RequestPath:   req.URL.Path,
			RouteTemplate: managerIPSetItemRouteTemplate,
			StatusCode:    http.StatusOK,
		})
		if err != nil {
			writeManagerMutationError(w, r.logger, err, "delete manager ip set", spec.Path)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func (r *router) handleManagerIPSetMemberAction(action string) routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		spec := managerIPSetSpec(routeParam(req, "ip-set-id"))
		resource, found := r.readLiveResource(w, req, spec.Path, "read manager ip set for member action")
		if !found {
			return
		}
		_, payload, ok := readAndValidateJSONObject(w, req, r.logger)
		if !ok {
			return
		}
		ipAddress, valid := validateManagerIPAddressElement(w, payload)
		if !valid {
			return
		}
		nextBody, applied := r.managerIPSetMemberActionBody(w, resource.Payload, ipAddress, action)
		if !applied {
			return
		}
		_, err := r.store.Mutate(req.Context(), appsqlite.Mutation{
			Spec:          spec,
			Body:          nextBody,
			Username:      requestUsername(req),
			Operation:     appsqlite.ResourceOperationUpdate,
			RequestPath:   req.URL.Path,
			RouteTemplate: managerIPSetItemRouteTemplate,
			Action:        action,
			StatusCode:    http.StatusCreated,
		})
		if err != nil {
			writeManagerMutationError(w, r.logger, err, "update manager ip set member", spec.Path)
			return
		}
		writeCreatedJSON(w, r.logger, map[string]any{"ip_address": ipAddress})
	}
}

func (r *router) handleManagerIPSetMembers() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		spec := managerIPSetSpec(routeParam(req, "ip-set-id"))
		resource, found := r.readLiveResource(w, req, spec.Path, "read manager ip set members")
		if !found {
			return
		}
		payload, err := decodePayloadObject(resource.Payload)
		if err != nil {
			r.logger.Error("decode manager ip set members failed", zap.String("path", spec.Path), zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		addresses, err := optionalStringArrayField(payload, "ip_addresses")
		if err != nil {
			r.logger.Error("read manager ip set members failed", zap.String("path", spec.Path), zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		results := make([]json.RawMessage, 0, len(addresses))
		for _, address := range addresses {
			raw, marshalErr := json.Marshal(map[string]any{"ip_address": address})
			if marshalErr != nil {
				r.logger.Error("marshal manager ip set member failed", zap.String("path", spec.Path), zap.Error(marshalErr))
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			results = append(results, raw)
		}
		writeManagerListResult(w, r.logger, results)
	}
}

func managerIPSetSpec(ipSetID string) appsqlite.ResourceSpec {
	return appsqlite.ResourceSpec{
		APIFamily:     appsqlite.ResourceAPIFamilyManager,
		CollectionKey: managerIPSetsCollectionKey,
		Kind:          managerIPSetResourceType,
		ResourceType:  managerIPSetResourceType,
		Path:          managerIPSetsRouteTemplate + "/" + ipSetID,
		ParentPath:    managerIPSetsRouteTemplate,
		RelativePath:  ipSetID,
	}
}

func validateManagerIPSetPayload(w http.ResponseWriter, logger *zap.Logger, payload map[string]any) bool {
	if !validateCommonPolicyPayload(w, payload, managerIPSetResourceType) {
		return false
	}
	if _, ok := payload["ip_addresses"]; ok {
		addresses, valid := validateIPAddressListPayload(w, logger, payload)
		if !valid {
			return false
		}
		if len(addresses) > maxManagerIPSetAddresses {
			http.Error(w, "ip_addresses exceeds maximum items", http.StatusBadRequest)
			return false
		}
	}
	logger.Debug("validated manager ip set payload", zap.String("resource_type", managerIPSetResourceType))
	return true
}

func validateManagerIPAddressElement(w http.ResponseWriter, payload map[string]any) (string, bool) {
	value, ok := payload["ip_address"]
	if !ok {
		http.Error(w, "ip_address is required", http.StatusBadRequest)
		return "", false
	}
	address, ok := value.(string)
	if !ok || address == "" {
		http.Error(w, "ip_address must be a string", http.StatusBadRequest)
		return "", false
	}
	if _, valid := ipElementFamily(address); !valid {
		http.Error(w, "ip_address must be a valid IP element", http.StatusBadRequest)
		return "", false
	}
	return address, true
}

func (r *router) managerIPSetMemberActionBody(
	w http.ResponseWriter,
	raw json.RawMessage,
	ipAddress string,
	action string,
) (json.RawMessage, bool) {
	payload, err := decodePayloadObject(raw)
	if err != nil {
		r.logger.Error("decode manager ip set for member action failed", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return nil, false
	}
	existing, err := optionalStringArrayField(payload, "ip_addresses")
	if err != nil {
		r.logger.Error("read manager ip set addresses failed", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return nil, false
	}
	if !ipAddressFamilyCompatible(w, existing, ipAddress) {
		return nil, false
	}
	switch action {
	case "add_ip":
		payload["ip_addresses"] = appendMissing(existing, []string{ipAddress})
	case "remove_ip":
		payload["ip_addresses"] = removeStrings(existing, []string{ipAddress})
	default:
		http.Error(w, "unsupported ip set member action", http.StatusBadRequest)
		return nil, false
	}
	next, err := json.Marshal(payload)
	if err != nil {
		r.logger.Error("marshal manager ip set member action failed", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return nil, false
	}
	return next, true
}

func optionalStringArrayField(payload map[string]any, key string) ([]string, error) {
	if _, ok := payload[key]; !ok {
		return []string{}, nil
	}
	return stringArrayField(payload, key)
}

func ipAddressFamilyCompatible(w http.ResponseWriter, existing []string, next string) bool {
	nextFamily, valid := ipElementFamily(next)
	if !valid {
		http.Error(w, "ip_address must be a valid IP element", http.StatusBadRequest)
		return false
	}
	for _, address := range existing {
		existingFamily, existingValid := ipElementFamily(address)
		if !existingValid || existingFamily != nextFamily {
			http.Error(w, "ip_addresses must not mix IPv4 and IPv6", http.StatusBadRequest)
			return false
		}
	}
	return true
}
