package httpapi

import (
	"net/http"

	appsqlite "nsx-t-mockapi/internal/sqlite"
)

const (
	policyTier0CollectionKey = "policy.tier0s"
	policyTier1CollectionKey = "policy.tier1s"
	tier0ResourceType        = "Tier0"
	tier1ResourceType        = "Tier1"
)

func tier0CollectionSpec(_ *http.Request) policyCollectionSpec {
	return policyCollectionSpec{
		CollectionKey: policyTier0CollectionKey,
		ParentPath:    "/infra",
	}
}

func tier1CollectionSpec(_ *http.Request) policyCollectionSpec {
	return policyCollectionSpec{
		CollectionKey: policyTier1CollectionKey,
		ParentPath:    "/infra",
	}
}

func tier1Spec(tier1ID string) appsqlite.ResourceSpec {
	return resourceSpec(policyTier1CollectionKey, tier1ResourceType, "/infra", "tier-1s", tier1ID)
}

func (r *router) handleTier1Get() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		resource, found := r.readLiveResource(w, req, tier1Spec(routeParam(req, "tier-1-id")).Path, "read tier1")
		if !found {
			return
		}
		writeRawOKJSON(w, r.logger, resource.Payload)
	}
}

func (r *router) handleTier1State() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		resource, found := r.readLiveResource(w, req, tier1Spec(routeParam(req, "tier-1-id")).Path, "read tier1 state")
		if !found {
			return
		}
		writeOKJSON(w, r.logger, map[string]any{
			"tier1_state": map[string]any{
				"state": stateFromRealization(resource.RealizationStatus),
			},
		})
	}
}
