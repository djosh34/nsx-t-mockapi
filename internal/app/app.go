// Package app owns startup orchestration for the mock API process.
package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"nsx-t-mockapi/internal/clock"
	"nsx-t-mockapi/internal/config"
	"nsx-t-mockapi/internal/httpapi"
	appsqlite "nsx-t-mockapi/internal/sqlite"

	"go.uber.org/zap"
)

// Options configures application startup.
type Options struct {
	ConfigPath string
	Logger     *zap.Logger
	Clock      clock.Clock
}

// Application is a fully built mock API application.
type Application struct {
	Config           config.Config
	ManagerDatabases *appsqlite.ManagerDatabasePool
	Clock            clock.Clock
	Server           *http.Server
}

// Build loads config, initializes the manager database pool, and builds the HTTP server.
func Build(ctx context.Context, opts Options) (app *Application, retErr error) {
	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	appClock := opts.Clock
	if appClock == nil {
		appClock = clock.SystemClock{}
	}
	started := appClock.Now()
	logger.Info("starting application build", zap.String("config_path", opts.ConfigPath), zap.Time("started_at", started))

	cfg, err := config.LoadFile(opts.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	logConfig(logger, cfg)

	managerDatabases, err := appsqlite.NewManagerDatabasePool(appsqlite.ManagerDatabasePoolOptions{
		BasePath: cfg.Database.Path,
		ResourceStore: appsqlite.ResourceStoreOptions{
			Clock:       appClock,
			Realization: cfg.Realization,
			Logger:      logger,
		},
		Logger: logger,
	})
	if err != nil {
		return nil, fmt.Errorf("build manager database pool: %w", err)
	}
	defer func() {
		if retErr == nil {
			return
		}
		closeErr := managerDatabases.Close()
		if closeErr != nil {
			retErr = fmt.Errorf("%w; close manager database pool: %w", retErr, closeErr)
		}
	}()

	server, err := buildHTTPServer(ctx, logger, cfg, managerDatabases, appClock)
	if err != nil {
		return nil, err
	}

	finished := appClock.Now()
	logger.Info(
		"application build completed",
		zap.String("database_data_dir", cfg.Database.Path),
		zap.Duration("elapsed", finished.Sub(started)),
	)

	return &Application{
		Config:           cfg,
		ManagerDatabases: managerDatabases,
		Clock:            appClock,
		Server:           server,
	}, nil
}

func logConfig(logger *zap.Logger, cfg config.Config) {
	logger.Debug(
		"loaded config",
		zap.String("listen_addr", cfg.Server.ListenAddr),
		zap.String("db_path", cfg.Database.Path),
		zap.Int("search_default_page_size", cfg.Search.DefaultPageSize),
		zap.Int("search_max_page_size", cfg.Search.MaxPageSize),
	)
}

func buildHTTPServer(
	ctx context.Context,
	logger *zap.Logger,
	cfg config.Config,
	managerDatabases appsqlite.ManagerDatabaseProvider,
	appClock clock.Clock,
) (*http.Server, error) {
	handler, err := httpapi.NewHandler(ctx, httpapi.AppOptions{
		Config:           cfg,
		ManagerDatabases: managerDatabases,
		Clock:            appClock,
		Logger:           logger,
	})
	if err != nil {
		return nil, fmt.Errorf("build http handler: %w", err)
	}
	server, err := httpapi.NewServer(httpapi.ServerOptions{
		Config:  cfg,
		Handler: handler,
		Logger:  logger,
	})
	if err != nil {
		return nil, fmt.Errorf("build http server: %w", err)
	}
	return server, nil
}

// Run builds and starts the HTTP server. It blocks until serving stops.
func Run(ctx context.Context, opts Options) (retErr error) {
	app, err := Build(ctx, opts)
	if err != nil {
		return err
	}
	defer func() {
		closeErr := app.Close()
		if closeErr == nil {
			return
		}
		if retErr == nil {
			retErr = closeErr
			return
		}
		retErr = fmt.Errorf("%w; %w", retErr, closeErr)
	}()

	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	logger.Info("starting http server", zap.String("listen_addr", app.Config.Server.ListenAddr))
	err = app.Server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("serve http: %w", err)
	}

	return nil
}

// Close releases application resources.
func (a *Application) Close() error {
	if a == nil || a.ManagerDatabases == nil {
		return nil
	}
	if err := a.ManagerDatabases.Close(); err != nil {
		return fmt.Errorf("close manager database pool: %w", err)
	}
	return nil
}
