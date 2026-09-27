package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/gofrs/uuid"

	"github.com/martinezsaweczko/whatsappBot-golang/app"
	"github.com/martinezsaweczko/whatsappBot-golang/config"
	"github.com/martinezsaweczko/whatsappBot-golang/http_server"
	"github.com/martinezsaweczko/whatsappBot-golang/model"
	"github.com/martinezsaweczko/whatsappBot-golang/o11"
	_ "github.com/mattn/go-sqlite3"
)

// Buildinfo variables set via LDFlags in the Makefile
var (
	version   string
	buildTime string
	appName   string
)

func main() {

	// Create context for the application
	ctx := context.Background()
	// Create an instance of the application
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Printf("Configuration error: %v\n", err)
		os.Exit(1)
	}

	// Create logger with config options
	file, errFile := config.LogFilePath(cfg.Log.FilePath)
	if errFile != nil {
		fmt.Printf("Error opening log file: %v\n", errFile)
		os.Exit(1)
	}

	defer file.Close()

	logLevel := config.ParseLogLevel(cfg.Log.Level)
	log := slog.New(slog.NewTextHandler(file, &slog.HandlerOptions{Level: logLevel}))

	// Group execution UUID for all logs in this run
	executionID, _ := uuid.NewV4()
	log = log.With("execution_id", executionID)

	httpConf := &http_server.HTTPServerConfig{
		Addr:         cfg.HttpServer.Address,
		Port:         cfg.HttpServer.Port,
		Timeout:      cfg.HttpServer.Timeout,
		ReadTimeout:  cfg.HttpServer.ReadTimeout,
		WriteTimeout: cfg.HttpServer.WriteTimeout,
		Log:          log,
	}

	// Print build info and configuration details to the log
	log.Info("Build Info information", "version", version, "build_time", buildTime, "app_name", appName)
	log.Info("Configuration loaded", "http_address", httpConf.Addr, "http_port", httpConf.Port, "http_timeout", httpConf.Timeout, "http_read_timeout", httpConf.ReadTimeout, "http_write_timeout", httpConf.WriteTimeout, "log_level", cfg.Log.Level, "log_file_path", cfg.Log.FilePath)

	// Set up OpenTelemetry providers.
	otelShutdown, observabilityInst, otelErr := o11.SetupOTelSDK(ctx, &cfg.O11)
	if otelErr != nil {
		log.Error("OpenTelemetry setup error", "error", otelErr)
		os.Exit(1)
	}

	defer otelShutdown(ctx)

	//httpServer is an implementation of the Server interface defined in the app package
	httpServer := httpConf.New()

	// Build app with all dependencies injected
	app := app.NewApp(cfg).
		WithServer(httpServer).
		WithLog(log).
		WithObservability(&observabilityInst).
		WithBuildInfo(model.BuildInfo{
			Version:   version,
			BuildTime: buildTime,
			AppName:   appName,
		}).
		WithBasePath("/api/v1")

	// Run the application
	errApp := app.Run()
	if errApp != nil {
		log.Error("Application error", "error", errApp)
		os.Exit(1)
	}

	log.Info("Application terminated")
}
