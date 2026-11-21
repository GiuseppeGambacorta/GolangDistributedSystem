package observability

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"google.golang.org/grpc/stats"
)

// Options configura i 3 pilastri dell'osservabilità (Log strutturati, Metriche e Tracce).
type Options struct {
	ServiceName     string        // Nome logico del servizio riportato in metriche e tracce (default: "proglog-server")
	OutputDir       string        // Cartella dove scrivere server.log, metrics.log e traces.log (default: observability/output/)
	MetricsInterval time.Duration // Ogni quanto salvare le metriche su metrics.log (default: 5s)
}

// Telemetry raccoglie logger strutturato (log/slog) e provider OpenTelemetry (Traces + Metrics).
type Telemetry struct {
	Logger         *slog.Logger
	TracerProvider *sdktrace.TracerProvider
	MeterProvider  *sdkmetric.MeterProvider

	LogsFilePath    string
	MetricsFilePath string
	TracesFilePath  string

	files []*os.File
}

// DefaultOutputDir restituisce sempre observability/output/ rispetto a questo file
// (oppure la directory indicata dalla variabile d'ambiente OBSERVABILITY_DIR).
func DefaultOutputDir() string {
	if customDir := os.Getenv("OBSERVABILITY_DIR"); customDir != "" {
		return customDir
	}
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		return filepath.Join("observability", "output")
	}
	return filepath.Join(filepath.Dir(currentFile), "output")
}

// Setup inizializza:
//  1. Logger JSON strutturato (log/slog -> server.log)
//  2. OpenTelemetry TracerProvider (stdouttrace -> traces.log)
//  3. OpenTelemetry MeterProvider  (stdoutmetric -> metrics.log)
func Setup(options Options) (*Telemetry, error) {
	if options.ServiceName == "" {
		options.ServiceName = "proglog-server"
	}
	if options.OutputDir == "" {
		options.OutputDir = DefaultOutputDir()
	}
	if options.MetricsInterval <= 0 {
		options.MetricsInterval = 5 * time.Second
	}

	if err := os.MkdirAll(options.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("impossibile creare la cartella observability output: %w", err)
	}

	logsPath := filepath.Join(options.OutputDir, "server.log")
	metricsPath := filepath.Join(options.OutputDir, "metrics.log")
	tracesPath := filepath.Join(options.OutputDir, "traces.log")

	logsFile, err := openLogFile(logsPath)
	if err != nil {
		return nil, err
	}
	metricsFile, err := openLogFile(metricsPath)
	if err != nil {
		_ = logsFile.Close()
		return nil, err
	}
	tracesFile, err := openLogFile(tracesPath)
	if err != nil {
		_ = logsFile.Close()
		_ = metricsFile.Close()
		return nil, err
	}

	// 1. Structured Logger (Go stdlib log/slog in formato JSON)
	logger := slog.New(slog.NewJSONHandler(logsFile, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})).With("service", options.ServiceName)

	// Risorsa condivisa OpenTelemetry (identifica il nostro servizio in tracce e metriche)
	res, err := resource.New(
		context.Background(),
		resource.WithAttributes(semconv.ServiceName(options.ServiceName)),
	)
	if err != nil {
		_ = closeAll(logsFile, metricsFile, tracesFile)
		return nil, fmt.Errorf("impossibile creare la risorsa OpenTelemetry: %w", err)
	}

	// 2. Traces Exporter (salva ogni Span in JSON leggibile dentro traces.log)
	traceExporter, err := stdouttrace.New(
		stdouttrace.WithWriter(tracesFile),
		stdouttrace.WithPrettyPrint(),
	)
	if err != nil {
		_ = closeAll(logsFile, metricsFile, tracesFile)
		return nil, fmt.Errorf("impossibile creare il trace exporter: %w", err)
	}
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()), // In sviluppo campioniamo il 100% delle richieste
		sdktrace.WithBatcher(traceExporter),
	)

	// 3. Metrics Exporter (salva periodicamente le metriche in JSON dentro metrics.log)
	metricExporter, err := stdoutmetric.New(
		stdoutmetric.WithWriter(metricsFile),
		stdoutmetric.WithPrettyPrint(),
	)
	if err != nil {
		_ = tracerProvider.Shutdown(context.Background())
		_ = closeAll(logsFile, metricsFile, tracesFile)
		return nil, fmt.Errorf("impossibile creare il metric exporter: %w", err)
	}
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(
			metricExporter,
			sdkmetric.WithInterval(options.MetricsInterval),
		)),
	)

	// Registra i provider globali OpenTelemetry e il propagatore W3C TraceContext
	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return &Telemetry{
		Logger:          logger,
		TracerProvider:  tracerProvider,
		MeterProvider:   meterProvider,
		LogsFilePath:    logsPath,
		MetricsFilePath: metricsPath,
		TracesFilePath:  tracesPath,
		files:           []*os.File{logsFile, metricsFile, tracesFile},
	}, nil
}

// GRPCStatsHandler restituisce lo StatsHandler OpenTelemetry pronto da passare a
// grpc.StatsHandler(...) per raccogliere automaticamente tracce e metriche di ogni RPC.
func (t *Telemetry) GRPCStatsHandler() stats.Handler {
	return otelgrpc.NewServerHandler(
		otelgrpc.WithTracerProvider(t.TracerProvider),
		otelgrpc.WithMeterProvider(t.MeterProvider),
	)
}

// Shutdown svuota i buffer di metriche/tracce su disco e chiude i file in modo pulito.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	var errs []error
	if t.TracerProvider != nil {
		errs = append(errs, t.TracerProvider.Shutdown(ctx))
	}
	if t.MeterProvider != nil {
		errs = append(errs, t.MeterProvider.Shutdown(ctx))
	}
	errs = append(errs, closeAll(t.files...))
	return errors.Join(errs...)
}

func openLogFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("impossibile aprire il file %q: %w", path, err)
	}
	return file, nil
}

func closeAll(files ...*os.File) error {
	var errs []error
	for _, f := range files {
		if f != nil {
			errs = append(errs, f.Close())
		}
	}
	return errors.Join(errs...)
}
