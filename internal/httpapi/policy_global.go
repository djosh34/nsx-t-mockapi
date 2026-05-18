package httpapi

import (
	"encoding/json"
	"net/http"

	appsqlite "nsx-t-mockapi/internal/sqlite"
)

func (r *router) handleGlobalConsolidatedIPMembers() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		localGroup := policyGroupSpec(routeParam(req, "domain-id"), routeParam(req, "group-id"))
		if !r.requireLiveResource(w, req, localGroup.Path) {
			return
		}
		writeOKJSON(w, r.logger, listResult{Results: []json.RawMessage{}, ResultCount: 0})
	}
}

func globalTier1SegmentSpec(req *http.Request) appsqlite.ResourceSpec {
	return tier1SegmentSpec(routeParam(req, "tier-1-id"), routeParam(req, "segments-id"))
}
