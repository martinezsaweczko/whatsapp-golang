package o11

import (
	"context"
	"slices"
	"testing"

	"github.com/martinezsaweczko/whatsappBot-golang/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	logglobal "go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func TestNewPropagatorCarriesTraceContextAndBaggage(t *testing.T) {
	prop := newPropagator()

	fields := prop.Fields()
	for _, want := range []string{"traceparent", "baggage"} {
		if !slices.Contains(fields, want) {
			t.Errorf("propagator fields %v should include %q", fields, want)
		}
	}

	traceID, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	spanID, _ := trace.SpanIDFromHex("0102030405060708")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	}))
	member, err := baggage.NewMember("tenant", "kiosk")
	if err != nil {
		t.Fatalf("failed to build baggage member: %v", err)
	}
	bag, err := baggage.New(member)
	if err != nil {
		t.Fatalf("failed to build baggage: %v", err)
	}
	ctx = baggage.ContextWithBaggage(ctx, bag)

	carrier := propagation.MapCarrier{}
	prop.Inject(ctx, carrier)
	if got, want := carrier.Get("traceparent"), "00-0102030405060708090a0b0c0d0e0f10-0102030405060708-01"; got != want {
		t.Errorf("traceparent = %q, want %q", got, want)
	}

	extracted := prop.Extract(context.Background(), carrier)
	sc := trace.SpanContextFromContext(extracted)
	if sc.TraceID() != traceID || sc.SpanID() != spanID || !sc.IsRemote() {
		t.Errorf("extracted span context = %+v, want the injected remote one", sc)
	}
	if got := baggage.FromContext(extracted).Member("tenant").Value(); got != "kiosk" {
		t.Errorf("extracted baggage tenant = %q, want kiosk", got)
	}
}

// With neither a tracer endpoint nor a Prometheus path, telemetry must be
// inert: spans are never sampled and metrics go to a no-op provider. This is
// the only SetupOTelSDK configuration that needs no network and does not
// register a collector on the process-wide Prometheus registry.
func TestSetupOTelSDKWithoutExporters(t *testing.T) {
	prevTP, prevMP := otel.GetTracerProvider(), otel.GetMeterProvider()
	prevProp, prevLP := otel.GetTextMapPropagator(), logglobal.GetLoggerProvider()
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetMeterProvider(prevMP)
		otel.SetTextMapPropagator(prevProp)
		logglobal.SetLoggerProvider(prevLP)
	})

	ctx := context.Background()
	shutdown, inst, err := SetupOTelSDK(ctx, &config.O11Config{Environment: "test"}, "whatsappbot-test", "v0.0.0")
	if err != nil {
		t.Fatalf("SetupOTelSDK failed: %v", err)
	}

	if inst.Trace == nil {
		t.Fatal("a tracer provider must always be returned")
	}
	_, span := inst.Trace.Tracer("test").Start(ctx, "inert")
	if span.IsRecording() || span.SpanContext().IsSampled() {
		t.Error("spans must not be recorded or sampled without a tracer endpoint")
	}
	span.End()

	if _, ok := inst.MeterProvider.(noop.MeterProvider); !ok {
		t.Errorf("meter provider is %T, want the no-op provider", inst.MeterProvider)
	}
	if !slices.Contains(otel.GetTextMapPropagator().Fields(), "traceparent") {
		t.Error("the global propagator should carry W3C trace context")
	}

	if err := shutdown(ctx); err != nil {
		t.Errorf("shutdown failed: %v", err)
	}
	// Each cleanup runs once: a second call has nothing left to do.
	if err := shutdown(ctx); err != nil {
		t.Errorf("second shutdown should be a no-op, got: %v", err)
	}
}
