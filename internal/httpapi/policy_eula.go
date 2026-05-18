package httpapi

import (
	"net/http"

	"go.uber.org/zap"
)

func (r *router) handleEULAAcceptance() routeHandler {
	return func(w http.ResponseWriter, req *http.Request) {
		r.logger.Debug("serving eula acceptance", zap.String("path", req.URL.Path))
		writeOKJSON(w, r.logger, map[string]any{
			"id":                  "acceptance",
			"display_name":        "acceptance",
			"resource_type":       "EULAAcceptance",
			"path":                "/eula/acceptance",
			"parent_path":         "/eula",
			"relative_path":       "acceptance",
			"acceptance":          true,
			"_system_owned":       true,
			"_protection":         "NOT_PROTECTED",
			"_revision":           0,
			"_create_user":        "system",
			"_last_modified_user": "system",
			"_create_time":        0,
			"_last_modified_time": 0,
		})
	}
}
