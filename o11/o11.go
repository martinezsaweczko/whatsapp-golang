package o11

import (
	"context"
	"errors"
	"time"

	"github.com/martinezsaweczko/whatsappBot-golang/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutlog"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/trace"
)

type ObservabilityInst struct {
	Trace         *trace.TracerProvider
	MeterProvider metric.MeterProvider
}

// SetupOTelSDK bootstraps the OpenTelemetry pipeline.
// If it does not return an error, make sure to call shutdown for proper cleanup.
func SetupOTelSDK(ctx context.Context, config *config.O11Config) (func(context.Context) error, ObservabilityInst, error) {
	var shutdownFuncs []func(context.Context) error
	var err error

	observabilityInst := ObservabilityInst{}

	// shutdown calls cleanup functions registered via shutdownFuncs.
	// The errors from the calls are joined.
	// Each registered cleanup will be invoked once.
	shutdown := func(ctx context.Context) error {
		var err error
		for _, fn := range shutdownFuncs {
			err = errors.Join(err, fn(ctx))
		}
		shutdownFuncs = nil
		return err
	}

	// handleErr calls shutdown for cleanup and makes sure that all errors are returned.
	handleErr := func(inErr error) {
		err = errors.Join(inErr, shutdown(ctx))
	}

	// Set up propagator.
	prop := newPropagator()
	otel.SetTextMapPropagator(prop)

	// Set up trace provider.
	if config.TracerEndpoint != "" {
		tracerProvider, err := newTracerProvider(ctx, config.TracerEndpoint)
		if err != nil {
			handleErr(err)
			return shutdown, observabilityInst, err
		}
		shutdownFuncs = append(shutdownFuncs, tracerProvider.Shutdown)
		observabilityInst.Trace = tracerProvider
		otel.SetTracerProvider(tracerProvider)
	} else {
		// If no trace endpoint is provided, set a no-op tracer provider to avoid errors when tracing is attempted.
		observabilityInst.Trace = trace.NewTracerProvider(trace.WithSampler(trace.NeverSample()))
		otel.SetTracerProvider(observabilityInst.Trace)
	}

	// Set up meter provider.
	// If no Prometheus path is provided, we can skip setting up the meter provider, as the application currently doesn't use it for anything else.
	if config.PrometheusPath != "" {
		meterProvider, err := newMeterProvider()
		if err != nil {
			handleErr(err)
			return shutdown, observabilityInst, err
		}
		shutdownFuncs = append(shutdownFuncs, meterProvider.Shutdown)
		otel.SetMeterProvider(meterProvider)

	} else {
		noopMeterProvider := noop.NewMeterProvider()

		otel.SetMeterProvider(noopMeterProvider)

	}

	observabilityInst.MeterProvider = otel.GetMeterProvider()

	// Set up logger provider.
	loggerProvider, err := newLoggerProvider()
	if err != nil {
		handleErr(err)
		return shutdown, observabilityInst, err
	}
	shutdownFuncs = append(shutdownFuncs, loggerProvider.Shutdown)
	global.SetLoggerProvider(loggerProvider)

	return shutdown, observabilityInst, err
}

func newPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

func newTracerProvider(ctx context.Context, tracerEndpoint string) (*trace.TracerProvider, error) {
	// traceExporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	// if err != nil {
	// 	return nil, err
	// }

	traceExporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(tracerEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}

	tracerProvider := trace.NewTracerProvider(
		trace.WithBatcher(traceExporter,
			// Default is 5s. Set to 1s for demonstrative purposes.
			trace.WithBatchTimeout(time.Second)),
	)
	return tracerProvider, nil
}

func newMeterProvider() (*sdkmetric.MeterProvider, error) {
	// metricExporter, err := stdoutmetric.New(stdoutmetric.WithPrettyPrint())
	// if err != nil {
	// 	return nil, err
	// }
	// Meter exporter to PromQL
	// The exporter embeds a default OpenTelemetry Reader and
	// implements prometheus.Collector, allowing it to be used as
	// both a Reader and Collector.
	exporter, err := prometheus.New()
	if err != nil {
		return nil, err
	}

	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))
	return meterProvider, nil
}

func newLoggerProvider() (*log.LoggerProvider, error) {
	logExporter, err := stdoutlog.New(stdoutlog.WithPrettyPrint())
	if err != nil {
		return nil, err
	}

	loggerProvider := log.NewLoggerProvider(
		log.WithProcessor(log.NewBatchProcessor(logExporter)),
	)
	return loggerProvider, nil
}
