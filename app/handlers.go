package app

import (
	"github.com/martinezsaweczko/whatsappBot-golang/http/handlers"
	"github.com/martinezsaweczko/whatsappBot-golang/http/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// initHandlers initializes all HTTP handlers and registers them with the server
func (app *App) initHandlers() error {

	var err error
	// Initialize metrics handler
	metricsHandlerConf := handlers.MetricsHandlerConfig{
		Log:           app.log,
		Observability: app.observability,
		Cfg:           app.config,
	}

	app.handlers = &Handlers{}
	app.handlers.metricsHandler = metricsHandlerConf.NewMetricsHandler()

	// Initialize version handler
	versionHandlerConf := handlers.VersionHandlerConfig{
		Log:           app.log,
		Observability: app.observability,
		Cfg:           app.config,
		AppBuildInfo:  app.buildInfo,
		BasePath:      app.basePath,
	}
	app.handlers.versionHandler, err = versionHandlerConf.NewVersionHandler()
	if err != nil {
		app.log.Error("Failed to initialize version handler", "error", err)
		return err
	}

	// Initialize health handler
	healthHandlerConf := handlers.HealthHandlerConfig{
		Log:           app.log,
		Observability: app.observability,
		Cfg:           app.config,
		BasePath:      app.basePath,
	}
	app.handlers.healthHandler, err = healthHandlerConf.NewHealthHandler()
	if err != nil {
		app.log.Error("Failed to initialize health handler", "error", err)
		return err
	}

	return nil
}

// registerRoutes registers all HTTP routes to the server's mux with appropriate middleware
func (app *App) registerRoutes() {
	// Define middleware chains for different endpoints
	loggingMiddleware := middleware.LoggingMiddleware(app.log)

	// Register metrics handler (no middleware)
	if app.config.O11.PrometheusPath != "" {
		app.log.Info("Registering metrics handler", "path", app.config.O11.PrometheusPath)
		app.handlers.metricsHandler.RegisterRoutes(app.server, promhttp.Handler())
	}

	// Register health handler (no middleware)
	app.handlers.healthHandler.RegisterRoutes(app.server)

	// Register version handler with logging middleware
	app.handlers.versionHandler.RegisterRoutes(app.server, loggingMiddleware)
}
