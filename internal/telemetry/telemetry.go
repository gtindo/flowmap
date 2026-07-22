// Package telemetry configures OpenTelemetry at the process edge.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	logglobal "go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc/credentials"
)

const serviceName = "flowmap"

var enabled atomic.Bool

// Setup initializes OpenTelemetry exporters when OTLP configuration is present.
func Setup(ctx context.Context, version string, console io.Writer) (func(context.Context) error, bool, error) {
	if telemetryDisabled() {
		enabled.Store(false)
		return func(context.Context) error { return nil }, false, nil
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithHost(),
		resource.WithProcess(),
		resource.WithAttributes(
			attribute.String("service.name", serviceName),
			attribute.String("service.version", version),
		),
	)
	if err != nil {
		return nil, false, fmt.Errorf("create telemetry resource: %w", err)
	}

	traceOptions, metricOptions, logOptions, err := exporterOptions()
	if err != nil {
		return nil, false, err
	}

	traceExporter, err := otlptracegrpc.New(ctx, traceOptions...)
	if err != nil {
		return nil, false, fmt.Errorf("create trace exporter: %w", err)
	}

	metricExporter, err := otlpmetricgrpc.New(ctx, metricOptions...)
	if err != nil {
		return nil, false, fmt.Errorf("create metric exporter: %w", err)
	}

	logExporter, err := otlploggrpc.New(ctx, logOptions...)
	if err != nil {
		return nil, false, fmt.Errorf("create log exporter: %w", err)
	}

	tracerProvider := trace.NewTracerProvider(
		trace.WithResource(res),
		trace.WithBatcher(traceExporter),
	)
	meterProvider := metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(metric.NewPeriodicReader(metricExporter)),
	)
	loggerProvider := log.NewLoggerProvider(
		log.WithResource(res),
		log.WithProcessor(log.NewBatchProcessor(logExporter)),
	)

	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	logglobal.SetLoggerProvider(loggerProvider)

	slog.SetDefault(slog.New(fanoutHandler{
		handlers: []slog.Handler{
			slog.NewJSONHandler(console, nil),
			otelslog.NewHandler("github.com/gtindo/flowmap", otelslog.WithLoggerProvider(loggerProvider)),
		},
	}))
	enabled.Store(true)

	shutdown := func(ctx context.Context) error {
		shutdownContext, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		defer enabled.Store(false)

		return errors.Join(
			loggerProvider.Shutdown(shutdownContext),
			meterProvider.Shutdown(shutdownContext),
			tracerProvider.Shutdown(shutdownContext),
		)
	}

	return shutdown, true, nil
}

// Enabled reports whether telemetry was initialized for the current process.
func Enabled() bool {
	return enabled.Load()
}

func exporterOptions() ([]otlptracegrpc.Option, []otlpmetricgrpc.Option, []otlploggrpc.Option, error) {
	certificatePath := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_CERTIFICATE"))
	if certificatePath == "" {
		return nil, nil, nil, nil
	}

	creds, err := credentials.NewClientTLSFromFile(certificatePath, endpointServerName())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create OTLP TLS credentials: %w", err)
	}

	return []otlptracegrpc.Option{otlptracegrpc.WithTLSCredentials(creds)},
		[]otlpmetricgrpc.Option{otlpmetricgrpc.WithTLSCredentials(creds)},
		[]otlploggrpc.Option{otlploggrpc.WithTLSCredentials(creds)},
		nil
}

func endpointServerName() string {
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return ""
	}

	host, _, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		return parsed.Hostname()
	}

	return host
}

func telemetryDisabled() bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("OTEL_SDK_DISABLED")), "true") {
		return true
	}

	return strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) == ""
}

type fanoutHandler struct {
	handlers []slog.Handler
}

func (handler fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, next := range handler.handlers {
		if next.Enabled(ctx, level) {
			return true
		}
	}

	return false
}

func (handler fanoutHandler) Handle(ctx context.Context, record slog.Record) error {
	var err error

	for _, next := range handler.handlers {
		if next.Enabled(ctx, record.Level) {
			err = errors.Join(err, next.Handle(ctx, record.Clone()))
		}
	}

	return err
}

func (handler fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	nextHandlers := make([]slog.Handler, 0, len(handler.handlers))
	for _, next := range handler.handlers {
		nextHandlers = append(nextHandlers, next.WithAttrs(attrs))
	}

	return fanoutHandler{handlers: nextHandlers}
}

func (handler fanoutHandler) WithGroup(name string) slog.Handler {
	nextHandlers := make([]slog.Handler, 0, len(handler.handlers))
	for _, next := range handler.handlers {
		nextHandlers = append(nextHandlers, next.WithGroup(name))
	}

	return fanoutHandler{handlers: nextHandlers}
}
