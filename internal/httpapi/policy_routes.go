package httpapi

import "net/http"

const (
	globalIPMembersRouteTemplate = "/policy/api/v1/global-infra/domains/{domain-id}/groups/{group-id}/" +
		"members/consolidated-effective-ip-addresses"
	globalTier1SegmentStateRouteTemplate = "/policy/api/v1/global-infra/tier-1s/{tier-1-id}/" +
		"segments/{segments-id}/state"
	globalTier1SegmentStatsRouteTemplate = "/policy/api/v1/global-infra/tier-1s/{tier-1-id}/" +
		"segments/{segments-id}/statistics"
	tier1RouteTemplate             = "/policy/api/v1/infra/tier-1s/{tier-1-id}"
	tier1StateRouteTemplate        = "/policy/api/v1/infra/tier-1s/{tier-1-id}/state"
	tier1SegmentStateRouteTemplate = "/policy/api/v1/infra/tier-1s/{tier-1-id}/" +
		"segments/{segments-id}/state"
	tier1SegmentStatsRouteTemplate = "/policy/api/v1/infra/tier-1s/{tier-1-id}/" +
		"segments/{segments-id}/statistics"
)

func (r *router) remainingPolicyRoutes() []Route {
	securityPolicy := securityPolicyConfig()
	securityRule := securityRuleConfig()
	infraSegment := infraSegmentConfig()
	tier1Segment := tier1SegmentConfig()

	return []Route{
		authPathRoute("policy.eula.acceptance", "/policy/api/v1/eula/acceptance", r.handleEULAAcceptance()),
		authTemplateRoute(
			"policy.global.groups.consolidated_effective_ip_addresses",
			http.MethodGet,
			globalIPMembersRouteTemplate,
			r.handleGlobalConsolidatedIPMembers(),
		),
		authTemplateRoute(
			"policy.global.tier1.segments.state",
			http.MethodGet,
			globalTier1SegmentStateRouteTemplate,
			r.handleSegmentState(globalTier1SegmentSpec),
		),
		authTemplateRoute(
			"policy.global.tier1.segments.statistics",
			http.MethodGet,
			globalTier1SegmentStatsRouteTemplate,
			r.handleSegmentStatistics(globalTier1SegmentSpec),
		),
		authTemplateRoute(
			"policy.security_policies.list",
			http.MethodGet,
			securityPolicyListRouteTemplate,
			r.handlePolicyResourceList(securityPolicyCollectionSpec),
		),
		authTemplateRoute(
			"policy.security_policies.put",
			http.MethodPut,
			securityPolicyItemRouteTemplate,
			r.handlePolicyResourcePut(securityPolicy),
		),
		authTemplateRoute(
			"policy.security_policies.delete",
			http.MethodDelete,
			securityPolicyItemRouteTemplate,
			r.handlePolicyResourceDelete(securityPolicy),
		),
		authTemplateRoute(
			"policy.security_policies.get",
			http.MethodGet,
			securityPolicyItemRouteTemplate,
			r.handlePolicyResourceGet(securityPolicy),
		),
		authTemplateRoute(
			"policy.security_policies.patch",
			http.MethodPatch,
			securityPolicyItemRouteTemplate,
			r.handlePolicyResourcePatch(securityPolicy),
		),
		authActionRoute(
			"policy.security_policies.revise",
			http.MethodPost,
			securityPolicyItemRouteTemplate,
			"revise",
			r.handlePolicyResourceRevise(securityPolicy),
		),
		authTemplateRoute(
			"policy.security_rules.list",
			http.MethodGet,
			securityRuleListRouteTemplate,
			r.handleSecurityRuleList(),
		),
		authTemplateRoute(
			"policy.security_rules.patch",
			http.MethodPatch,
			securityRuleItemRouteTemplate,
			r.handlePolicyResourcePatch(securityRule),
		),
		authTemplateRoute(
			"policy.security_rules.delete",
			http.MethodDelete,
			securityRuleItemRouteTemplate,
			r.handlePolicyResourceDelete(securityRule),
		),
		authTemplateRoute(
			"policy.security_rules.put",
			http.MethodPut,
			securityRuleItemRouteTemplate,
			r.handlePolicyResourcePut(securityRule),
		),
		authTemplateRoute(
			"policy.security_rules.get",
			http.MethodGet,
			securityRuleItemRouteTemplate,
			r.handlePolicyResourceGet(securityRule),
		),
		authTemplateRoute(
			"policy.security_rules.statistics",
			http.MethodGet,
			securityRuleItemRouteTemplate+"/statistics",
			r.handleRuleStatistics(),
		),
		authActionRoute(
			"policy.security_rules.revise",
			http.MethodPost,
			securityRuleItemRouteTemplate,
			"revise",
			r.handlePolicyResourceRevise(securityRule),
		),
		authTemplateRoute(
			"policy.security_policies.statistics",
			http.MethodGet,
			securityPolicyItemRouteTemplate+"/statistics",
			r.handleSecurityPolicyStatistics(),
		),
		authPathRoute(
			"policy.infra.segments.list",
			infraSegmentListRouteTemplate,
			r.handlePolicyResourceList(infraSegmentCollectionSpec),
		),
		authPathRoute(
			"policy.infra.segments.state.list",
			infraSegmentListRouteTemplate+"/state",
			r.handleSegmentStateList(infraSegmentCollectionSpec),
		),
		authTemplateRoute(
			"policy.infra.segments.put",
			http.MethodPut,
			infraSegmentItemRouteTemplate,
			r.handlePolicyResourcePut(infraSegment),
		),
		authTemplateRoute(
			"policy.infra.segments.delete",
			http.MethodDelete,
			infraSegmentItemRouteTemplate,
			r.handlePolicyResourceDelete(infraSegment),
		),
		authTemplateRoute(
			"policy.infra.segments.patch",
			http.MethodPatch,
			infraSegmentItemRouteTemplate,
			r.handlePolicyResourcePatch(infraSegment),
		),
		authTemplateRoute(
			"policy.infra.segments.get",
			http.MethodGet,
			infraSegmentItemRouteTemplate,
			r.handlePolicyResourceGet(infraSegment),
		),
		authTemplateRoute(
			"policy.infra.segments.state",
			http.MethodGet,
			infraSegmentItemRouteTemplate+"/state",
			r.handleSegmentState(infraSegmentSpecFromRoute),
		),
		authTemplateRoute(
			"policy.infra.segments.statistics",
			http.MethodGet,
			infraSegmentItemRouteTemplate+"/statistics",
			r.handleSegmentStatistics(infraSegmentSpecFromRoute),
		),
		authPathRoute(
			"policy.tier0s.list",
			"/policy/api/v1/infra/tier-0s",
			r.handlePolicyResourceList(tier0CollectionSpec),
		),
		authPathRoute(
			"policy.tier1s.list",
			"/policy/api/v1/infra/tier-1s",
			r.handlePolicyResourceList(tier1CollectionSpec),
		),
		authTemplateRoute("policy.tier1s.get", http.MethodGet, tier1RouteTemplate, r.handleTier1Get()),
		authTemplateRoute(
			"policy.tier1.segments.list",
			http.MethodGet,
			tier1SegmentListRouteTemplate,
			r.handlePolicyResourceList(tier1SegmentCollectionSpec),
		),
		authTemplateRoute(
			"policy.tier1.segments.state.list",
			http.MethodGet,
			tier1SegmentListRouteTemplate+"/state",
			r.handleSegmentStateList(tier1SegmentCollectionSpec),
		),
		authTemplateRoute(
			"policy.tier1.segments.delete",
			http.MethodDelete,
			tier1SegmentItemRouteTemplate,
			r.handlePolicyResourceDelete(tier1Segment),
		),
		authTemplateRoute(
			"policy.tier1.segments.patch",
			http.MethodPatch,
			tier1SegmentItemRouteTemplate,
			r.handlePolicyResourcePatch(tier1Segment),
		),
		authTemplateRoute(
			"policy.tier1.segments.put",
			http.MethodPut,
			tier1SegmentItemRouteTemplate,
			r.handlePolicyResourcePut(tier1Segment),
		),
		authTemplateRoute(
			"policy.tier1.segments.get",
			http.MethodGet,
			tier1SegmentItemRouteTemplate,
			r.handlePolicyResourceGet(tier1Segment),
		),
		authTemplateRoute(
			"policy.tier1.segments.state",
			http.MethodGet,
			tier1SegmentStateRouteTemplate,
			r.handleSegmentState(tier1SegmentSpecFromSegmentsIDRoute),
		),
		authTemplateRoute(
			"policy.tier1.segments.statistics",
			http.MethodGet,
			tier1SegmentStatsRouteTemplate,
			r.handleSegmentStatistics(tier1SegmentSpecFromSegmentsIDRoute),
		),
		authTemplateRoute("policy.tier1s.state", http.MethodGet, tier1StateRouteTemplate, r.handleTier1State()),
	}
}

func authPathRoute(name string, path string, handler routeHandler) Route {
	return Route{Name: name, Method: http.MethodGet, Path: path, Handler: handler, RequireAuth: true}
}

func authTemplateRoute(name string, method string, template string, handler routeHandler) Route {
	return Route{Name: name, Method: method, Template: template, Handler: handler, RequireAuth: true}
}

func authActionRoute(name string, method string, template string, action string, handler routeHandler) Route {
	return Route{Name: name, Method: method, Template: template, Action: action, Handler: handler, RequireAuth: true}
}
