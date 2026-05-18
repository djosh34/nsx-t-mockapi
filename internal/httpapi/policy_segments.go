package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	appsqlite "nsx-t-mockapi/internal/sqlite"

	"go.uber.org/zap"
)

const (
	policyInfraSegmentsCollectionKey = "policy.infra_segments"
	policyTier1SegmentsCollectionKey = "policy.tier1_segments"
	segmentResourceType              = "Segment"
	infraSegmentListRouteTemplate    = "/policy/api/v1/infra/segments"
	infraSegmentItemRouteTemplate    = "/policy/api/v1/infra/segments/{segment-id}"
	tier1SegmentListRouteTemplate    = "/policy/api/v1/infra/tier-1s/{tier-1-id}/segments"
	tier1SegmentItemRouteTemplate    = "/policy/api/v1/infra/tier-1s/{tier-1-id}/segments/{segment-id}"
)

func infraSegmentConfig() policyResourceConfig {
	return policyResourceConfig{
		Name:          "infra segment",
		CollectionKey: policyInfraSegmentsCollectionKey,
		ResourceType:  segmentResourceType,
		RouteTemplate: infraSegmentItemRouteTemplate,
		Spec: func(req *http.Request) appsqlite.ResourceSpec {
			return infraSegmentSpec(routeParam(req, "segment-id"))
		},
		Validate: validateSegmentPayload,
		Decorate: func(r *router, _ *http.Request, resource appsqlite.StoredResource) (json.RawMessage, bool) {
			raw, err := withState(resource.Payload, resource.RealizationStatus)
			if err != nil {
				r.logger.Error("decorate infra segment state failed", zap.String("path", resource.Path), zap.Error(err))
				return nil, false
			}
			return raw, true
		},
	}
}

func tier1SegmentConfig() policyResourceConfig {
	return policyResourceConfig{
		Name:          "tier1 segment",
		CollectionKey: policyTier1SegmentsCollectionKey,
		ResourceType:  segmentResourceType,
		RouteTemplate: tier1SegmentItemRouteTemplate,
		Spec: func(req *http.Request) appsqlite.ResourceSpec {
			return tier1SegmentSpec(routeParam(req, "tier-1-id"), routeParam(req, "segment-id"))
		},
		Validate: validateSegmentPayload,
		Decorate: func(r *router, _ *http.Request, resource appsqlite.StoredResource) (json.RawMessage, bool) {
			raw, err := withState(resource.Payload, resource.RealizationStatus)
			if err != nil {
				r.logger.Error("decorate tier1 segment state failed", zap.String("path", resource.Path), zap.Error(err))
				return nil, false
			}
			return raw, true
		},
	}
}

func infraSegmentCollectionSpec(_ *http.Request) policyCollectionSpec {
	return policyCollectionSpec{
		CollectionKey: policyInfraSegmentsCollectionKey,
		ParentPath:    "/infra",
	}
}

func tier1SegmentCollectionSpec(req *http.Request) policyCollectionSpec {
	return policyCollectionSpec{
		CollectionKey: policyTier1SegmentsCollectionKey,
		ParentPath:    "/infra/tier-1s/" + routeParam(req, "tier-1-id"),
	}
}

func infraSegmentSpec(segmentID string) appsqlite.ResourceSpec {
	return resourceSpec(policyInfraSegmentsCollectionKey, segmentResourceType, "/infra", "segments", segmentID)
}

func infraSegmentSpecFromRoute(req *http.Request) appsqlite.ResourceSpec {
	return infraSegmentSpec(routeParam(req, "segment-id"))
}

func tier1SegmentSpec(tier1ID string, segmentID string) appsqlite.ResourceSpec {
	return resourceSpec(
		policyTier1SegmentsCollectionKey,
		segmentResourceType,
		"/infra/tier-1s/"+tier1ID,
		"segments",
		segmentID,
	)
}

func tier1SegmentSpecFromSegmentsIDRoute(req *http.Request) appsqlite.ResourceSpec {
	return tier1SegmentSpec(routeParam(req, "tier-1-id"), routeParam(req, "segments-id"))
}

func (r *router) handleSegmentStateList(collection func(*http.Request) policyCollectionSpec) routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		spec := collection(req)
		resources, err := r.store.List(req.Context(), appsqlite.ListOptions{
			CollectionKey: spec.CollectionKey,
			ParentPath:    spec.ParentPath,
		})
		if err != nil {
			r.logger.Error("list segment states failed", zap.String("parent_path", spec.ParentPath), zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		results := make([]json.RawMessage, 0, len(resources))
		for _, resource := range resources {
			raw, marshalErr := json.Marshal(segmentStatePayload(resource))
			if marshalErr != nil {
				r.logger.Error("marshal segment state failed", zap.String("path", resource.Path), zap.Error(marshalErr))
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			results = append(results, raw)
		}
		writePolicyListResult(w, r.logger, results)
	}
}

func (r *router) handleSegmentState(specFunc func(*http.Request) appsqlite.ResourceSpec) routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		resource, found := r.readLiveResource(w, req, specFunc(req).Path, "read segment for state")
		if !found {
			return
		}
		writeOKJSON(w, r.logger, segmentStatePayload(resource))
	}
}

func (r *router) handleSegmentStatistics(specFunc func(*http.Request) appsqlite.ResourceSpec) routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		spec := specFunc(req)
		if !r.requireLiveResource(w, req, spec.Path) {
			return
		}
		writeOKJSON(w, r.logger, map[string]any{
			"logical_switch_id":     spec.RelativePath,
			"last_update_timestamp": 0,
		})
	}
}

func validateSegmentPayload(w http.ResponseWriter, logger *zap.Logger, payload map[string]any) bool {
	if !validateCommonPolicyPayload(w, payload, segmentResourceType) {
		return false
	}
	if !validateOptionalStringEnum(w, payload, "replication_mode", "MTEP", "SOURCE") {
		return false
	}
	if !validateOptionalStringEnum(w, payload, "admin_state", "UP", "DOWN") {
		return false
	}
	if !validateOptionalSubnets(w, payload) {
		return false
	}
	for _, key := range []string{"transport_zone_path", "connectivity_path", "dhcp_config_path"} {
		if value, ok := payload[key]; ok {
			text, typeOK := value.(string)
			if !typeOK || (text != "" && !strings.HasPrefix(text, "/")) {
				http.Error(w, key+" must be an absolute policy path", http.StatusBadRequest)
				return false
			}
		}
	}
	logger.Debug("validated segment payload", zap.String("resource_type", segmentResourceType))
	return true
}
