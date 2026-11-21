# 01 — Osservabilità da Zero: i 3 File, `Setup()` ed Esempi JSON

Guida pratica per capire **da zero** cosa sono i 3 file in `observability/output/` (`server.log`, `metrics.log`, `traces.log`), in cosa differiscono, cosa fa ogni oggetto dentro [`telemetry.go`](telemetry.go) e come si presentano i dati generati.

---

## 1. Analogia della Pizzeria: perché servono 3 file diversi?

Immagina che il tuo server gRPC sia una **Pizzeria**. Un solo quaderno non basta per capire tutto quello che succede; servono **3 strumenti complementari**:

### A. `server.log` (Structured Logs) = **Il Diario degli Eventi**
Registra **ogni singolo fatto**, riga per riga, in formato JSON:
- *"Ore 14:10:01 — L'utente `root` ha chiamato `Produce` → `OK` in 0.4ms"*
- *"Ore 14:10:05 — L'utente `reader` ha chiamato `Produce` → `PermissionDenied`!"*
- **A cosa serve**: capire *chi* ha fatto *cosa* e *perché* una chiamata è fallita.
- **Dove si scrive**: in un **interceptor gRPC** (così logga in automatico ogni RPC senza ripeterlo in ogni metodo).

### B. `metrics.log` (Metrics) = **Il Cruscotto dell'Auto**
Non salva i dettagli del singolo cliente, ma **solo numeri riassunti nel tempo** (ogni 5 secondi):
- *"Negli ultimi 5 secondi: 42 `Produce`, durata media 0.5ms, 3 errori, 12 KB ricevuti."*
- **A cosa serve**: vedere a colpo d'occhio la salute generale (lentezza, traffico, picchi di errori).

### C. `traces.log` (Traces) = **Il Cronometro a Tappe del singolo ordine**
Dà a **ogni singola richiesta** un numero di scontrino univoco (`TraceID`) e cronometra ogni sua tappa (**Span**):
- Scontrino `#3e3343b7`: Ingresso gRPC (`0.1ms`) → Controllo ACL (`0.2ms`) → Scrittura su disco (`12.0ms` ← collo di bottiglia!).
- **A cosa serve**: scoprire *esattamente dove* una singola richiesta ha perso tempo (soprattutto se attraversa più server).

**Riassunto in 3 righe:**
- **Metrics (`metrics.log`)** → *i numeri* ("c'è un problema!")
- **Traces (`traces.log`)** → *il cronometro a tappe* ("si è bloccato esattamente qui!")
- **Logs (`server.log`)** → *il diario dettagliato* ("è successo a `reader` con errore `PermissionDenied`!")

---

## 2. Cos'è `otel` e Anatomia di `Setup()` in [`telemetry.go`](telemetry.go)

**`otel`** (`go.opentelemetry.io/otel`) è la **centralina globale di OpenTelemetry** dentro il tuo programma Go. Registrando lì `TracerProvider`, `MeterProvider` e `Propagator`, qualunque libreria compatibile (come `otelgrpc`) sa automaticamente dove inviare tracce e metriche.

| Oggetto in `telemetry.go` | Cosa fa (in parole semplici) | Dove viene inserito |
| :--- | :--- | :--- |
| **`res`** (`resource.New`) | Il **"cartellino identificativo"** del servizio (`service.name = "proglog-server"`). | Dentro `TracerProvider` e `MeterProvider`. |
| **`slog.NewJSONHandler`** | Formatta ogni log come riga **JSON** dentro `server.log`. | Dentro `slog.New(...)` → salvato in `Telemetry.Logger`. |
| **`traceExporter`** (`stdouttrace.New`) | Lo **stampante delle tracce**: scrive gli Span in JSON in `traces.log`. | Nel `Batcher` di `TracerProvider`. |
| **`TracerProvider`** (`sdktrace.NewTracerProvider`) | Il **motore delle tracce**: campiona il 100% (`AlwaysSample`) e scrive a blocchi (`WithBatcher`). | In `Telemetry.TracerProvider`, `otel.SetTracerProvider(...)` e `otelgrpc.NewServerHandler(...)`. |
| **`metricExporter`** (`stdoutmetric.New`) | Lo **stampante delle metriche**: scrive i numeri aggregati in JSON in `metrics.log`. | Dentro `NewPeriodicReader`. |
| **`NewPeriodicReader`** | La **sveglia** che ogni `5s` legge i contatori/istogrammi e li passa a `metricExporter`. | Dentro `MeterProvider` (`WithReader`). |
| **`MeterProvider`** (`sdkmetric.NewMeterProvider`) | Il **motore delle metriche**: aggrega durate, byte e chiamate completate. | In `Telemetry.MeterProvider`, `otel.SetMeterProvider(...)` e `otelgrpc.NewServerHandler(...)`. |
| **`TraceContext` / `Baggage`** | Il **passacarte di rete**: propaga l'header `traceparent` su HTTP/2 tra più servizi. | Nella centralina globale `otel.SetTextMapPropagator(...)`. |
| **`GRPCStatsHandler()`** (`otelgrpc.NewServerHandler`) | Il **sensore automatico gRPC**: misura ogni RPC e alimenta sia `TracerProvider` sia `MeterProvider`. | Passato al server gRPC con `grpc.StatsHandler(...)`. |

---

## 3. Esempi reali di cosa finisce nei 3 file (`observability/output/`)

### A. `server.log` — Log strutturato JSON (`log/slog`, 1 riga per RPC)
```json
{
  "time": "2026-11-21T14:30:10.123Z",
  "level": "INFO",
  "msg": "rpc completed",
  "service": "proglog-server",
  "grpc.method": "/log.v1.Log/Produce",
  "subject": "root",
  "grpc.code": "OK",
  "grpc.time_ns": 412300
}
```

### B. `traces.log` — Span OpenTelemetry (`stdouttrace`, 1 blocco per RPC)
```json
{
  "Name": "log.v1.Log/Produce",
  "SpanContext": {
    "TraceID": "3e3343b74193e6a807cac515e82fb3b3",
    "SpanID": "045493d1be3f7188"
  },
  "SpanKind": 2,
  "StartTime": "2026-11-21T14:30:10.1228Z",
  "EndTime": "2026-11-21T14:30:10.1232Z",
  "Attributes": [
    { "Key": "rpc.system", "Value": { "Type": "STRING", "Value": "grpc" } },
    { "Key": "rpc.service", "Value": { "Type": "STRING", "Value": "log.v1.Log" } },
    { "Key": "rpc.method", "Value": { "Type": "STRING", "Value": "Produce" } },
    { "Key": "rpc.grpc.status_code", "Value": { "Type": "INT64", "Value": 0 } }
  ]
}
```

### C. `metrics.log` — Snapshot aggregato ogni 5s (`stdoutmetric`)
```json
{
  "Name": "rpc.server.duration",
  "Description": "Measures the duration of inbound RPC.",
  "Unit": "ms",
  "Data": {
    "DataPoints": [
      {
        "Attributes": [
          { "Key": "rpc.service", "Value": { "Type": "STRING", "Value": "log.v1.Log" } },
          { "Key": "rpc.method", "Value": { "Type": "STRING", "Value": "Produce" } },
          { "Key": "rpc.grpc.status_code", "Value": { "Type": "INT64", "Value": 0 } }
        ],
        "Count": 3,
        "Sum": 1.42
      }
    ]
  }
}
```

---

## Postilla — In produzione: Prometheus, Jaeger ed Elasticsearch/Loki

Il vantaggio principale di **OpenTelemetry** è che il codice di `grpc/` e `log/` **non sa dove finiscono i dati**: l'unico pezzo che decide la destinazione sono le 2 righe dell'**Exporter** dentro [`telemetry.go`](telemetry.go).

- **In locale (adesso)**: usiamo `stdoutmetric` e `stdouttrace` su file (`metrics.log`, `traces.log`) così non serve avviare container Docker o servizi esterni.
- **In produzione**: ti basta sostituire solo i 2 Exporter (zero modifiche al resto del codice!):
  - **Prometheus** (+ **Grafana**) al posto di `stdoutmetric` → raccoglie le metriche numeriche, disegna dashboard in tempo reale e fa scattare allarmi automatici se aumentano latenze o errori.
  - **Jaeger** (via exporter OTLP) al posto di `stdouttrace` → interfaccia web dove cerchi un `TraceID` e vedi la timeline grafica a barre di ogni tappa della richiesta tra tutti i microservizi.
  - **Elasticsearch / Grafana Loki** al posto di `server.log` su disco → indicizza i log JSON di tutti i nodi per cercare un errore o un utente (`"subject": "reader"`) in pochi millisecondi.
