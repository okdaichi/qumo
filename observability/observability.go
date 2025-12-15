// Package observability provides tracing, logging, and metrics instrumentation.
//
// The package follows Go standard library patterns:
//   - [log/slog]: Config struct, New, Default, SetDefault
//   - [context]: simple context key, WithValue pattern
//   - [database/sql]: Open with simple arguments
//
// # Quick Start
//
//	// Setup (typically in main)
//	observability.Setup(ctx, observability.Config{
//	    Service:      "my-service",
//	    TraceAddr:    "localhost:4317",
//	    LogAddr:      "localhost:4317",
//	    SamplingRate: 0.1,
//	})
//	defer observability.Shutdown(ctx)
//
//	// Usage (in application code)
//	ctx, span := observability.Start(ctx, "operation")
//	defer span.End()
//
// # Logging
//
// After Setup, use slog as usual - logs are sent to OTel Collector:
//
//	slog.Info("request received", "path", r.URL.Path)
//	slog.ErrorContext(ctx, "failed", "err", err) // includes trace context
//
// # Span Operations
//
// Start returns a Span that must be ended:
//
//	ctx, span := observability.Start(ctx, "db.query", observability.Track("video"))
//	defer span.End()
//
//	// On error
//	if err != nil {
//	    span.Error(err)
//	    return err
//	}
//
// # Metrics
//
// Use Recorder for track-scoped metrics:
//
//	rec := observability.NewRecorder("video")
//	rec.GroupReceived()
//	rec.CacheHit()
package observability

import (
	"context"
	"log"
	"log/slog"
	"sync"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otellog "go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
	"go.opentelemetry.io/otel/trace"
)

// Config holds observability configuration.
// Zero value disables all observability features.
type Config struct {
	// Service is the service name for telemetry.
	Service string

	// Version is the service version. Optional.
	Version string

	// TraceAddr is the OpenTelemetry collector address (e.g., "localhost:4317").
	// Empty string disables tracing.
	TraceAddr string

	// LogAddr is the OpenTelemetry collector address for logs.
	// Empty string disables log export to OTel (logs still go to stdout).
	// If same as TraceAddr, uses the same endpoint.
	LogAddr string

	// SamplingRate is the trace sampling rate (0.0 to 1.0).
	// Default is 1.0 (sample everything).
	SamplingRate float64

	// Metrics enables Prometheus metrics. Default is true.
	Metrics bool
}

var (
	global   state
	globalMu sync.RWMutex
)

type state struct {
	config    Config
	tracer    trace.Tracer
	shutdowns []func(context.Context) error
}

// Setup initializes observability with the given configuration.
// It should be called once at program startup.
//
// Example:
//
//	observability.Setup(ctx, observability.Config{
//	    Service:      "qumo-relay",
//	    TraceAddr:    "localhost:4317",
//	    LogAddr:      "localhost:4317",
//	    SamplingRate: 0.1,
//	    Metrics:      true,
//	})
func Setup(ctx context.Context, cfg Config) error {
	globalMu.Lock()
	defer globalMu.Unlock()

	global.config = cfg
	global.shutdowns = nil

	// Initialize tracing if configured
	if cfg.TraceAddr != "" {
		shutdown, tracer, err := setupTracing(ctx, cfg)
		if err != nil {
			return err
		}
		global.tracer = tracer
		global.shutdowns = append(global.shutdowns, shutdown)
		log.Printf("observability: tracing enabled (endpoint=%s, sampling=%.0f%%)",
			cfg.TraceAddr, cfg.SamplingRate*100)
	}

	// Initialize logging if configured
	if cfg.LogAddr != "" {
		shutdown, err := setupLogging(ctx, cfg)
		if err != nil {
			return err
		}
		global.shutdowns = append(global.shutdowns, shutdown)
		log.Printf("observability: logging enabled (endpoint=%s)", cfg.LogAddr)
	}

	if cfg.Metrics {
		log.Println("observability: metrics enabled")
	}

	return nil
}

// Shutdown gracefully shuts down observability components.
// It should be called before program exit.
func Shutdown(ctx context.Context) error {
	globalMu.RLock()
	shutdowns := global.shutdowns
	globalMu.RUnlock()

	var firstErr error
	for _, shutdown := range shutdowns {
		if err := shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Enabled reports whether tracing is enabled.
func Enabled() bool {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return global.tracer != nil
}

// MetricsEnabled reports whether metrics are enabled.
func MetricsEnabled() bool {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return global.config.Metrics
}

func setupTracing(ctx context.Context, cfg Config) (func(context.Context) error, trace.Tracer, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.Service),
			semconv.ServiceVersion(cfg.Version),
		),
	)
	if err != nil {
		return nil, nil, err
	}

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(cfg.TraceAddr),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, nil, err
	}

	var sampler sdktrace.Sampler
	switch {
	case cfg.SamplingRate >= 1.0:
		sampler = sdktrace.AlwaysSample()
	case cfg.SamplingRate <= 0.0:
		sampler = sdktrace.NeverSample()
	default:
		sampler = sdktrace.TraceIDRatioBased(cfg.SamplingRate)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	tracer := tp.Tracer(cfg.Service)

	return tp.Shutdown, tracer, nil
}

func setupLogging(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.Service),
			semconv.ServiceVersion(cfg.Version),
		),
	)
	if err != nil {
		return nil, err
	}

	exporter, err := otlploggrpc.New(ctx,
		otlploggrpc.WithEndpoint(cfg.LogAddr),
		otlploggrpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}

	lp := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
	)

	// Set global logger provider for bridges
	otellog.SetLoggerProvider(lp)

	// Create slog handler that bridges to OTel
	handler := otelslog.NewHandler(cfg.Service,
		otelslog.WithVersion(cfg.Version),
		otelslog.WithSource(true),
	)

	// Set as default slog handler
	slog.SetDefault(slog.New(handler))

	return lp.Shutdown, nil
}

func tracer() trace.Tracer {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return global.tracer
}
