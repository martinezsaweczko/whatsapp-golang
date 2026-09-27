package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/gofrs/uuid"

	"github.com/martinezsaweczko/whatsappBot-golang/app"
	"github.com/martinezsaweczko/whatsappBot-golang/config"
	"github.com/martinezsaweczko/whatsappBot-golang/http_server"
	"github.com/martinezsaweczko/whatsappBot-golang/model"
	"github.com/martinezsaweczko/whatsappBot-golang/o11"
	"github.com/martinezsaweczko/whatsappBot-golang/repository"
	"github.com/martinezsaweczko/whatsappBot-golang/whatsapp"
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

	// Create logger with config options, wrapped with trace correlation
	file, errFile := config.LogFilePath(cfg.Log.FilePath)
	if errFile != nil {
		fmt.Printf("Error opening log file: %v\n", errFile)
		os.Exit(1)
	}
	defer file.Close()

	logLevel := config.ParseLogLevel(cfg.Log.Level)
	baseHandler := slog.NewTextHandler(file, &slog.HandlerOptions{Level: logLevel})
	log := slog.New(o11.NewTraceLogHandler(baseHandler))

	// Group execution UUID for all logs in this run
	executionID, _ := uuid.NewV4()
	log = log.With("execution_id", executionID)

	// Print build info
	log.Info("Build Info information", "version", version, "build_time", buildTime, "app_name", appName)

	// Set up OpenTelemetry providers.
	otelShutdown, observabilityInst, otelErr := o11.SetupOTelSDK(ctx, &cfg.O11, "whatsappbot-golang", version)
	if otelErr != nil {
		log.Error("OpenTelemetry setup error", "error", otelErr)
		os.Exit(1)
	}
	defer func() {
		if err := otelShutdown(ctx); err != nil {
			log.Error("OpenTelemetry shutdown error", "error", err)
		}
	}()

	// Open the application database
	repo, repoErr := repository.New(cfg.Bot.DBPath, log)
	if repoErr != nil {
		log.Error("Database setup error", "error", repoErr)
		os.Exit(1)
	}
	defer func() {
		if err := repo.Close(); err != nil {
			log.Error("Database close error", "error", err)
		}
	}()

	// Create the WhatsApp client and command router
	waClient, waErr := whatsapp.NewClient(cfg.Bot.SessionDBPath, log)
	if waErr != nil {
		log.Error("WhatsApp client setup error", "error", waErr)
		os.Exit(1)
	}

	commandMetrics, cmdErr := o11.NewCommandMetrics(observabilityInst.MeterProvider)
	if cmdErr != nil {
		log.Error("Command metrics setup error", "error", cmdErr)
		os.Exit(1)
	}

	botJIDs := []string{
		cfg.Bot.MentionedBotNumber,
		cfg.Bot.BotNumber + "@c.us",
	}
	router := whatsapp.NewRouter(log, commandMetrics, observabilityInst.Trace, botJIDs,
		time.Duration(cfg.Bot.CommandTimeout)*time.Second)
	waClient.AddEventHandler(router.HandleEvent)

	// HTTP servers
	publicServer := (&http_server.HTTPServerConfig{
		Addr:         cfg.HttpServer.Address,
		Port:         cfg.HttpServer.Port,
		Timeout:      cfg.HttpServer.Timeout,
		ReadTimeout:  cfg.HttpServer.ReadTimeout,
		WriteTimeout: cfg.HttpServer.WriteTimeout,
		Log:          log,
	}).New()

	internalServer := (&http_server.HTTPServerConfig{
		Addr:         cfg.InternalServer.Address,
		Port:         cfg.InternalServer.Port,
		Timeout:      cfg.InternalServer.Timeout,
		ReadTimeout:  cfg.InternalServer.ReadTimeout,
		WriteTimeout: cfg.InternalServer.WriteTimeout,
		Log:          log,
	}).New()

	// Build app with all dependencies injected
	application := app.NewApp(cfg).
		WithPublicServer(publicServer).
		WithInternalServer(internalServer).
		WithWhatsApp(waClient, router).
		WithRepository(repo).
		WithLog(log).
		WithObservability(&observabilityInst).
		WithBuildInfo(model.BuildInfo{
			Version:   version,
			BuildTime: buildTime,
			AppName:   appName,
		}).
		WithBasePath("/api/v1")

	// Run the application
	if err := application.Run(); err != nil {
		log.Error("Application error", "error", err)
		os.Exit(1)
	}

	log.Info("Application terminated")
}
