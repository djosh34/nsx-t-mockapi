//nolint:tagliatelle // NSX ListResult JSON fields intentionally use snake_case.
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"nsx-t-mockapi/internal/clock"
	"nsx-t-mockapi/internal/config"
	appsqlite "nsx-t-mockapi/internal/sqlite"

	"go.uber.org/zap"
)

const contentTypeJSON = "application/json"

const readHeaderTimeout = 5 * time.Second

var (
	errHTTPAPIManagerDatabasesRequired = errors.New("http api manager database provider is required")
	errHTTPAPIContextRequired          = errors.New("http api context is required")
	errServerListenRequired            = errors.New("server listen address is required")
	errServerHandlerRequired           = errors.New("server handler is required")
	errRequestManagerStoresMissing     = errors.New("request manager stores missing from context")
)

// AppOptions configures HTTP handler construction.
type AppOptions struct {
	Config           config.Config
	DB               *sql.DB
	ManagerDatabases appsqlite.ManagerDatabaseProvider
	Clock            clock.Clock
	Logger           *zap.Logger
}

// ServerOptions configures HTTP server construction.
type ServerOptions struct {
	Config  config.Config
	Handler http.Handler
	Logger  *zap.Logger
}

type routeHandler func(http.ResponseWriter, *http.Request)

// Route describes a manually registered HTTP route.
type Route struct {
	Name        string
	Method      string
	Path        string
	Template    string
	Action      string
	Handler     routeHandler
	RequireAuth bool
}

type router struct {
	logger           *zap.Logger
	managerDatabases appsqlite.ManagerDatabaseProvider
	users            contextUserStore
	store            contextResourceStore
	resourceOptions  appsqlite.ResourceStoreOptions
	search           config.SearchConfig
	routes           []Route
}

type routeParamsContextKey struct{}
type managerContextKey struct{}

type listResult struct {
	Results       []json.RawMessage `json:"results"`
	ResultCount   int               `json:"result_count"`
	SortBy        string            `json:"sort_by,omitempty"`
	SortAscending bool              `json:"sort_ascending,omitempty"`
}

// NewHandler builds the manually routed HTTP API handler.
func NewHandler(ctx context.Context, opts AppOptions) (http.Handler, error) {
	if ctx == nil {
		return nil, errHTTPAPIContextRequired
	}
	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	apiClock := opts.Clock
	if apiClock == nil {
		apiClock = clock.SystemClock{}
	}
	resourceOptions := appsqlite.ResourceStoreOptions{
		Clock:       apiClock,
		Realization: opts.Config.Realization,
		Logger:      logger,
	}
	managerDatabases := opts.ManagerDatabases
	if managerDatabases == nil && opts.DB != nil {
		managerDatabases = appsqlite.NewStaticManagerDatabaseProvider(opts.DB)
		if err := appsqlite.NewResourceStore(opts.DB, resourceOptions).EnsureBootstrap(ctx); err != nil {
			return nil, fmt.Errorf("bootstrap resource store: %w", err)
		}
	}
	if managerDatabases == nil {
		return nil, errHTTPAPIManagerDatabasesRequired
	}
	r := &router{
		logger:           logger,
		managerDatabases: managerDatabases,
		users:            contextUserStore{},
		store:            contextResourceStore{},
		resourceOptions:  resourceOptions,
		search:           opts.Config.Search,
	}
	r.routes = append([]Route{}, r.managerRoutes()...)
	r.routes = append(r.routes, r.remainingPolicyRoutes()...)
	r.routes = append(r.routes, []Route{
		{
			Name:        "policy.search.query",
			Method:      http.MethodGet,
			Template:    "/policy/api/v1/search/query",
			Handler:     r.handleSearchQuery(),
			RequireAuth: true,
		},
		{
			Name:        "policy.search.dsl",
			Method:      http.MethodGet,
			Template:    "/policy/api/v1/search/dsl",
			Handler:     r.handleSearch(appsqlite.SearchSyntaxDSL),
			RequireAuth: true,
		},
		{
			Name:        "policy.groups.list",
			Method:      http.MethodGet,
			Template:    "/policy/api/v1/infra/domains/{domain-id}/groups",
			Handler:     r.handlePolicyGroupsList(),
			RequireAuth: true,
		},
		{
			Name:        "policy.groups.patch",
			Method:      http.MethodPatch,
			Template:    "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}",
			Handler:     r.handlePolicyGroupPatch(),
			RequireAuth: true,
		},
		{
			Name:        "policy.groups.put",
			Method:      http.MethodPut,
			Template:    "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}",
			Handler:     r.handlePolicyGroupPut(),
			RequireAuth: true,
		},
		{
			Name:        "policy.groups.get",
			Method:      http.MethodGet,
			Template:    "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}",
			Handler:     r.handlePolicyGroupGet(),
			RequireAuth: true,
		},
		{
			Name:        "policy.groups.delete",
			Method:      http.MethodDelete,
			Template:    "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}",
			Handler:     r.handlePolicyGroupDelete(),
			RequireAuth: true,
		},
		{
			Name:        "policy.groups.ip_address_expressions.patch",
			Method:      http.MethodPatch,
			Template:    "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}/ip-address-expressions/{expression-id}",
			Handler:     r.handlePolicyGroupIPAddressExpressionPatch(),
			RequireAuth: true,
		},
		{
			Name:        "policy.groups.ip_address_expressions.add",
			Method:      http.MethodPost,
			Template:    "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}/ip-address-expressions/{expression-id}",
			Action:      "add",
			Handler:     r.handlePolicyGroupIPAddressExpressionAction("add"),
			RequireAuth: true,
		},
		{
			Name:        "policy.groups.ip_address_expressions.remove",
			Method:      http.MethodPost,
			Template:    "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}/ip-address-expressions/{expression-id}",
			Action:      "remove",
			Handler:     r.handlePolicyGroupIPAddressExpressionAction("remove"),
			RequireAuth: true,
		},
		{
			Name:        "policy.groups.ip_address_expressions.delete",
			Method:      http.MethodDelete,
			Template:    "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}/ip-address-expressions/{expression-id}",
			Handler:     r.handlePolicyGroupIPAddressExpressionDelete(),
			RequireAuth: true,
		},
		{
			Name:        "policy.groups.path_expressions.patch",
			Method:      http.MethodPatch,
			Template:    "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}/path-expressions/{expression-id}",
			Handler:     r.handlePolicyGroupPathExpressionPatch(),
			RequireAuth: true,
		},
		{
			Name:        "policy.groups.members.ip_addresses",
			Method:      http.MethodGet,
			Template:    "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}/members/ip-addresses",
			Handler:     r.handlePolicyGroupMembers("ip-addresses"),
			RequireAuth: true,
		},
		{
			Name:        "policy.groups.members.ip_groups",
			Method:      http.MethodGet,
			Template:    "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}/members/ip-groups",
			Handler:     r.handlePolicyGroupMembers("ip-groups"),
			RequireAuth: true,
		},
		{
			Name:        "policy.groups.members.segments",
			Method:      http.MethodGet,
			Template:    "/policy/api/v1/infra/domains/{domain-id}/groups/{group-id}/members/segments",
			Handler:     r.handlePolicyGroupMembers("segments"),
			RequireAuth: true,
		},
	}...)
	for _, route := range r.routes {
		routePath := route.Path
		if routePath == "" {
			routePath = route.Template
		}
		logger.Info(
			"registered http route",
			zap.String("route_name", route.Name),
			zap.String("method", route.Method),
			zap.String("path", routePath),
			zap.String("action", route.Action),
			zap.Bool("require_auth", route.RequireAuth),
		)
	}

	return r, nil
}

// NewServer builds the configured HTTP server without starting it.
func NewServer(opts ServerOptions) (*http.Server, error) {
	if opts.Config.Server.ListenAddr == "" {
		return nil, errServerListenRequired
	}
	if opts.Handler == nil {
		return nil, errServerHandlerRequired
	}
	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	logger.Info("constructed http server", zap.String("listen_addr", opts.Config.Server.ListenAddr))

	return &http.Server{
		Addr:              opts.Config.Server.ListenAddr,
		Handler:           opts.Handler,
		ReadHeaderTimeout: readHeaderTimeout,
	}, nil
}

func (r *router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	action := req.URL.Query().Get("action")
	manager, err := r.managerDatabases.ResolveManagerDatabase(req.Context(), req.Host)
	if err != nil {
		r.logger.Error("resolve manager database failed", zap.String("request_host", req.Host), zap.Error(err))
		status := http.StatusInternalServerError
		if errors.Is(err, appsqlite.ErrManagerHostEmpty) || errors.Is(err, appsqlite.ErrManagerHostUnsafe) {
			status = http.StatusBadRequest
		}
		http.Error(w, http.StatusText(status), status)
		return
	}
	r.logger.Debug(
		"routing http request",
		zap.String("method", req.Method),
		zap.String("path", req.URL.Path),
		zap.String("action", action),
		zap.String("manager_name", manager.Name),
		zap.String("request_host", req.Host),
		zap.String("db_path", manager.Path),
	)

	var allowed []string
	for _, route := range r.routes {
		params, matched := route.match(req.URL.Path, action)
		if !matched {
			continue
		}
		if route.Method != req.Method {
			allowed = append(allowed, route.Method)
			continue
		}

		handler := http.HandlerFunc(route.Handler)
		if route.RequireAuth {
			handler = RequireBasicAuth(r.logger, r.users, handler).ServeHTTP
		}
		r.logger.Debug(
			"matched http route",
			zap.String("route_name", route.Name),
			zap.String("manager_name", manager.Name),
			zap.String("request_host", req.Host),
			zap.String("db_path", manager.Path),
		)
		managerStores := requestManagerStores{
			manager: manager,
			users:   appsqlite.NewUserStore(manager.DB),
			store:   appsqlite.NewResourceStore(manager.DB, r.resourceOptions),
		}
		ctx := context.WithValue(req.Context(), managerContextKey{}, managerStores)
		ctx = context.WithValue(ctx, routeParamsContextKey{}, params)
		handler.ServeHTTP(w, req.WithContext(ctx))
		return
	}

	if len(allowed) > 0 {
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		r.logger.Debug(
			"rejected http request method",
			zap.String("method", req.Method),
			zap.String("path", req.URL.Path),
			zap.Strings("allowed", allowed),
		)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	r.logger.Debug("http route not found", zap.String("method", req.Method), zap.String("path", req.URL.Path))
	http.NotFound(w, req)
}

type requestManagerStores struct {
	manager appsqlite.ManagerDatabase
	users   appsqlite.UserStore
	store   appsqlite.ResourceStore
}

type contextUserStore struct{}

func (contextUserStore) FindUser(ctx context.Context, username string) (appsqlite.User, bool, error) {
	stores, err := storesFromContext(ctx)
	if err != nil {
		return appsqlite.User{}, false, err
	}
	user, found, err := stores.users.FindUser(ctx, username)
	if err != nil {
		return appsqlite.User{}, false, fmt.Errorf("find user in request manager database: %w", err)
	}
	return user, found, nil
}

type contextResourceStore struct{}

func (contextResourceStore) Read(
	ctx context.Context,
	opts appsqlite.ReadOptions,
) (appsqlite.StoredResource, bool, error) {
	stores, err := storesFromContext(ctx)
	if err != nil {
		return appsqlite.StoredResource{}, false, err
	}
	resource, found, err := stores.store.Read(ctx, opts)
	if err != nil {
		return appsqlite.StoredResource{}, false, fmt.Errorf("read resource from request manager database: %w", err)
	}
	return resource, found, nil
}

func (contextResourceStore) List(ctx context.Context, opts appsqlite.ListOptions) ([]appsqlite.StoredResource, error) {
	stores, err := storesFromContext(ctx)
	if err != nil {
		return nil, err
	}
	resources, err := stores.store.List(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("list resources from request manager database: %w", err)
	}
	return resources, nil
}

func (contextResourceStore) Mutate(
	ctx context.Context,
	mutation appsqlite.Mutation,
) (appsqlite.StoredResource, error) {
	stores, err := storesFromContext(ctx)
	if err != nil {
		return appsqlite.StoredResource{}, err
	}
	resource, err := stores.store.Mutate(ctx, mutation)
	if err != nil {
		return appsqlite.StoredResource{}, fmt.Errorf("mutate resource in request manager database: %w", err)
	}
	return resource, nil
}

func (contextResourceStore) SearchPage(
	ctx context.Context,
	opts appsqlite.SearchQueryOptions,
) (appsqlite.SearchPage, error) {
	stores, err := storesFromContext(ctx)
	if err != nil {
		return appsqlite.SearchPage{}, err
	}
	page, err := stores.store.SearchPage(ctx, opts)
	if err != nil {
		return appsqlite.SearchPage{}, fmt.Errorf("search resources in request manager database: %w", err)
	}
	return page, nil
}

func storesFromContext(ctx context.Context) (requestManagerStores, error) {
	stores, ok := ctx.Value(managerContextKey{}).(requestManagerStores)
	if !ok {
		return requestManagerStores{}, errRequestManagerStoresMissing
	}
	return stores, nil
}

func (route Route) match(path string, action string) (map[string]string, bool) {
	if route.Action != action {
		return nil, false
	}
	if route.Path != "" {
		return nil, route.Path == path
	}
	if route.Template == "" {
		return nil, false
	}

	pathParts := strings.Split(strings.Trim(path, "/"), "/")
	templateParts := strings.Split(strings.Trim(route.Template, "/"), "/")
	if len(pathParts) != len(templateParts) {
		return nil, false
	}

	params := map[string]string{}
	for index, templatePart := range templateParts {
		if strings.HasPrefix(templatePart, "{") && strings.HasSuffix(templatePart, "}") {
			key := strings.TrimSuffix(strings.TrimPrefix(templatePart, "{"), "}")
			params[key] = pathParts[index]
			continue
		}
		if templatePart != pathParts[index] {
			return nil, false
		}
	}
	return params, true
}

func routeParam(req *http.Request, name string) string {
	params, ok := req.Context().Value(routeParamsContextKey{}).(map[string]string)
	if !ok {
		return ""
	}
	return params[name]
}
