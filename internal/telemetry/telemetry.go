// Package telemetry initialises optional OpenTelemetry trace, metric, and log
// providers backed by an OTLP gRPC collector. All three providers share a
// single gRPC connection to reduce overhead.
//
// Call [Setup] once during startup. The returned [ShutdownFunc] must be called
// before the process exits to flush pending telemetry.
//
// If telemetry is not configured, the global providers remain no-ops and the
// rest of the codebase incurs no overhead.
package telemetry

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Config groups all telemetry settings. It maps 1-to-1 with the
// [config.TelemetryConfig] YAML block.
type Config struct {
	// OTLPEndpoint is the gRPC host:port of your OTLP collector,
	// e.g. "localhost:4317" or "otelcol.example.com:4317".
	OTLPEndpoint string

	// Insecure disables TLS for the collector connection.
	// Set to true for local collectors that have no TLS cert.
	Insecure bool

	// ServiceName overrides the OTel service.name resource attribute.
	// Defaults to "reminderrelay".
	ServiceName string

	// Headers is sent as gRPC metadata on every OTLP request.
	// Equivalent to the OTEL_EXPORTER_OTLP_HEADERS environment variable.
	// Typical use: authentication tokens such as {"Authorization": "Bearer <token>"}.
	Headers map[string]string
}

// ShutdownFunc flushes and closes all OTel providers.
// It must be called with a fresh context (the main context may already be
// cancelled by the time shutdown runs).
type ShutdownFunc func(context.Context) error

// Setup initialises the global OpenTelemetry trace, metric, and log providers.
// The three exporters share a single gRPC connection to cfg.OTLPEndpoint.
//
// Returns a [ShutdownFunc] that must be deferred by the caller to flush and
// close all providers. The function is always non-nil — on error it becomes a
// no-op so callers can defer unconditionally.
func Setup(ctx context.Context, cfg Config) (ShutdownFunc, error) {
	svcName := cfg.ServiceName
	if svcName == "" {
		svcName = "reminderrelay"
	}

	// Build the OTel resource describing this service instance.
	// resource.NewSchemaless avoids the schema URL mismatch that occurs when
	// resource.Default() (SDK semconv) and our semconv import are different
	// versions.
	svcRes := resource.NewSchemaless(semconv.ServiceName(svcName))
	res, err := resource.Merge(resource.Default(), svcRes)
	if err != nil {
		return noopShutdown, fmt.Errorf("building OTel resource: %w", err)
	}

	// Dial the collector once; all three exporters share this connection.
	var creds credentials.TransportCredentials
	if cfg.Insecure {
		creds = insecure.NewCredentials()
	} else {
		creds = credentials.NewTLS(nil) // system root CAs
	}
	conn, err := grpc.NewClient(cfg.OTLPEndpoint, grpc.WithTransportCredentials(creds))
	if err != nil {
		return noopShutdown, fmt.Errorf("dialling OTLP collector at %q: %w", cfg.OTLPEndpoint, err)
	}

	// --- Trace provider -------------------------------------------------

	traceExp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithGRPCConn(conn),
		otlptracegrpc.WithHeaders(cfg.Headers),
	)
	if err != nil {
		_ = conn.Close()
		return noopShutdown, fmt.Errorf("creating OTLP trace exporter: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	// --- Metric provider ------------------------------------------------

	metricExp, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithGRPCConn(conn),
		otlpmetricgrpc.WithHeaders(cfg.Headers),
	)
	if err != nil {
		_ = tp.Shutdown(ctx)
		_ = conn.Close()
		return noopShutdown, fmt.Errorf("creating OTLP metric exporter: %w", err)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp)),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	// --- Log provider ---------------------------------------------------

	logExp, err := otlploggrpc.New(ctx,
		otlploggrpc.WithGRPCConn(conn),
		otlploggrpc.WithHeaders(cfg.Headers),
	)
	if err != nil {
		_ = tp.Shutdown(ctx)
		_ = mp.Shutdown(ctx)
		_ = conn.Close()
		return noopShutdown, fmt.Errorf("creating OTLP log exporter: %w", err)
	}
	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)),
		sdklog.WithResource(res),
	)
	global.SetLoggerProvider(lp)

	// Return a shutdown function that flushes all providers and closes the
	// shared gRPC connection.
	return func(ctx context.Context) error {
		var errs []error
		if err := tp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("trace provider shutdown: %w", err))
		}
		if err := mp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("metric provider shutdown: %w", err))
		}
		if err := lp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("log provider shutdown: %w", err))
		}
		if err := conn.Close(); err != nil {
			errs = append(errs, fmt.Errorf("OTLP gRPC connection close: %w", err))
		}
		return errors.Join(errs...)
	}, nil
}

// noopShutdown is returned on error so callers can always defer unconditionally.
func noopShutdown(_ context.Context) error { return nil }
