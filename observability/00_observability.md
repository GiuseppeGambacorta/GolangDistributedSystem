# Modulo `observability/` — Log Strutturati, Metriche e Tracce

Documentazione del layer di osservabilità del progetto (Capitolo 6 di *Distributed Services with Go*), implementato con lo **stack moderno di Go 1.26**:
- **`log/slog`** (Standard Library Go) per i **Log Strutturati JSON** (al posto del vecchio `go-grpc-middleware/logging/zap`).
- **OpenTelemetry (`go.opentelemetry.io/otel` + `otelgrpc`)** per **Metriche** e **Tracce** (al posto dell'archiviato `go.opencensus.io`).

---

## I 3 Pilastri dell'Osservabilità

| Pilastro | Domanda a cui risponde | Libreria usata | File di output locale |
| :--- | :--- | :--- | :--- |
| **1. Structured Logs** | *"Cosa è successo esattamente su quella chiamata?"* | `log/slog` (stdlib) | `observability/output/server.log` |
| **2. Metrics** | *"Quante richieste, quanti errori, quanto è lento?"* | OpenTelemetry `sdk/metric` + `otelgrpc` | `observability/output/metrics.log` |
| **3. Traces** | *"Qual è stato il percorso e dove ha perso tempo?"* | OpenTelemetry `sdk/trace` + `otelgrpc` | `observability/output/traces.log` |

> **Nota su `.gitignore`:** la cartella `observability/output/` è ignorata da Git perché contiene solo log e telemetria generati a runtime.

---

## I 4 Golden Signals (Metriche chiave di Google)

1. **Latency**: quanto tempo impiega il server a rispondere (`rpc.server.duration`).
2. **Traffic**: quante richieste arrivano al secondo e quanti byte entrano/escono (`rpc.server.request.size`, `rpc.server.response.size`).
3. **Errors**: quanti errori si verificano (`PermissionDenied`, `OutOfRange`, `Internal`, ecc.).
4. **Saturation**: quanto siamo vicini al limite di risorse (CPU, memoria, spazio su disco del commit log).

---

## Struttura di `observability/telemetry.go`

- **`Setup(Options)`**: crea `observability/output/` e apre i 3 file:
  - `Logger` (`*slog.Logger`): scrive righe JSON strutturate in `server.log`.
  - `TracerProvider`: campionamento al 100% (`AlwaysSample()`, ideale in sviluppo) ed export JSON leggibile in `traces.log`.
  - `MeterProvider`: lettura periodica (default ogni `5s`) ed export JSON leggibile in `metrics.log`.
- **`GRPCStatsHandler()`**: restituisce un `stats.Handler` (`otelgrpc.NewServerHandler(...)`) pronto da agganciare al server gRPC con `grpc.StatsHandler(...)` per tracciare e misurare automaticamente ogni RPC.
- **`Shutdown(ctx)`**: forza il flush di tracce e metriche rimaste in memoria e chiude tutti i file con `errors.Join(...)`.
