# 02 — Come Leggere i Log Reali (`server.log`, `traces.log`, `metrics.log`)

Guida campo per campo basata sui file reali generati in `observability/output/` durante la tua prima sessione con `go run .` (2 `Produce`, 1 `ProduceStream` interrotto, 1 `Consume`, tutti con utente `root`).

---

## 1. `server.log` — Log Strutturati JSON (`log/slog`)

Esempio reale dalle tue prime 2 righe:
```json
{"time":"2026-11-21T14:51:24.047817+01:00","level":"INFO","msg":"finished unary call","service":"proglog-server","grpc.method":"/log.v1.Log/Produce","grpc.code":"OK","grpc.time_ns":1950125,"subject":"root","peer.address":"127.0.0.1:51975"}
{"time":"2026-11-21T14:52:23.52237+01:00","level":"WARN","msg":"finished streaming call","service":"proglog-server","grpc.method":"/log.v1.Log/ProduceStream","grpc.code":"Canceled","grpc.time_ns":749481833,"subject":"root","peer.address":"127.0.0.1:51975"}
```

| Campo | Valore di esempio | Cosa significa |
| :--- | :--- | :--- |
| `time` | `2026-11-21T14:51:24.047817+01:00` | Momento esatto in cui la RPC è terminata. |
| `level` | `INFO` / `WARN` | `INFO` se la chiamata è riuscita (`OK`), `WARN` se ha restituito un errore (es. `Canceled`, `PermissionDenied`, `OutOfRange`). |
| `msg` | `finished unary call` / `finished streaming call` | Indica se era una RPC singola (`Produce`/`Consume`) o uno stream (`ProduceStream`/`ConsumeStream`). |
| `service` | `proglog-server` | Nome del servizio configurato in `observability.Setup(...)`. |
| `grpc.method` | `/log.v1.Log/Produce` | Metodo gRPC invocato dal client. |
| `grpc.code` | `OK` / `Canceled` | Esito standard gRPC (`OK`, `Canceled`, `PermissionDenied`, `OutOfRange`, ecc.). |
| `grpc.time_ns` | `1950125` (~1.95 ms) / `749481833` (~749 ms) | Durata della chiamata in **nanosecondi** (dividi per `1.000.000` per avere i millisecondi). |
| `subject` | `root` | Identità letta dal certificato mTLS (`CN`) tramite `unaryAuthInterceptor` / `streamAuthInterceptor`. |
| `peer.address` | `127.0.0.1:51975` | IP e porta effimera del client connesso (nota: tutte e 4 le chiamate hanno la stessa porta `51975` perché HTTP/2 riusa **la stessa connessione TCP**!). |

---

## 2. `traces.log` — Tracce / Span OpenTelemetry (`stdouttrace`)

Ogni chiamata gRPC genera un blocco **Span** JSON completo. Ecco cosa significa ogni blocco del tuo `Produce`:

| Campo | Valore di esempio | Cosa significa |
| :--- | :--- | :--- |
| `Name` | `log.v1.Log/Produce` | Nome dell'operazione tracciata. |
| `SpanContext.TraceID` | `c3fb781dcf39fd606a4a008bdacfc4ee` | **"Numero di scontrino"** univoco dell'intera richiesta (se passasse per 5 microservizi, tutti avrebbero lo stesso `TraceID`). |
| `SpanContext.SpanID` | `47b4470469f9aa45` | ID di questa singola tappa (Span) dentro la traccia. |
| `SpanContext.TraceFlags` | `"01"` | Bit `1` = la traccia è stata campionata (grazie a `AlwaysSample()`). |
| `Parent.TraceID / SpanID` | `0000000000000000...` | Tutti zeri significa che **non c'è uno span padre**: questa chiamata è la radice della traccia (il client non ha inviato un `traceparent` HTTP/2). |
| `SpanKind` | `2` | Codice numerico OpenTelemetry: `2` = **Server** (`1` = Internal, `3` = Client). |
| `StartTime` / `EndTime` | `14:51:24.0452` → `14:51:24.0480` | Inizio e fine misurati dallo `StatsHandler` (~2.74 ms, leggermente più di `1.95 ms` in `server.log` perché include anche l'overhead del trasporto gRPC prima/dopo gli interceptor). |
| `Attributes` | `rpc.method`, `rpc.system.name="grpc"`, `server.address="127.0.0.1"`, `server.port=8080`, `rpc.response.status_code="OK"` | Metadati strutturati raccolti automaticamente da `otelgrpc` (su `ProduceStream` trovi `CANCELLED`). |
| `Status.Code` | `"Unset"` | Stato dello span (in gRPC solo errori server `Internal`/`Unknown` marcano lo span come `Error`; `OK` e `Canceled` lato client restano `Unset`). |
| `Resource` | `service.name="proglog-server"` | Cartellino identificativo del servizio che ha prodotto lo span. |
| `InstrumentationScope` | `otelgrpc` `0.72.0` | Quale libreria ha generato automaticamente questo span. |

---

## 3. `metrics.log` — Metriche Aggregate OpenTelemetry (`stdoutmetric`)

### Perché all'inizio ci sono tanti blocchi con `"ScopeMetrics": []`?
Perché in `telemetry.go` la "sveglia" (`PeriodicReader`) scatta **ogni 5 secondi**: finché il server è acceso ma non ha ancora ricevuto nessuna chiamata, salva uno snapshot vuoto (`"ScopeMetrics": []`). Appena arriva una RPC, compaiono i dati veri!

### Come leggere la metrica `rpc.server.call.duration`
`otelgrpc` raccoglie un **Istogramma** chiamato `rpc.server.call.duration` con `Unit: "s"` (**secondi**), diviso in un `DataPoint` per ogni combinazione `(rpc.method, rpc.response.status_code)`. Nel tuo test ci sono 3 `DataPoints`:
1. `Produce` (`OK`): `Count: 2`, `Min: 0.00164s` (~1.6 ms), `Max: 0.00270s` (~2.7 ms), `Sum: 0.00435s`
2. `Consume` (`OK`): `Count: 1`, `Min/Max: 0.00171s` (~1.7 ms)
3. `ProduceStream` (`CANCELLED`): `Count: 1`, `Min/Max: 0.7496s` (~750 ms)

| Campo | Cosa significa |
| :--- | :--- |
| `StartTime` / `Time` | Finestra temporale della misura: dall'avvio del server (`StartTime`) fino a questo snapshot (`Time`). |
| `Temporality: "CumulativeTemporality"` | I numeri sono **cumulativi** dall'avvio del server (non si azzerano ogni 5 secondi). |
| `Count`, `Min`, `Max`, `Sum` | Numero totale di chiamate, tempo minimo, massimo e somma totale in secondi (`Sum / Count` = durata media). |
| `Bounds` + `BucketCounts` | I **"secchi" dell'istogramma** in secondi (`0.005`, `0.01`, `0.025`, ..., `0.5`, `0.75`, `1`, ..., `10`): servono a calcolare i percentili (p50, p95, p99). Es: i tuoi 2 `Produce` (~1.6–2.7 ms) finiscono entrambi nel primo secchio `<= 0.005s` (`BucketCounts[0] = 2`), mentre `ProduceStream` (`0.7496s`) finisce nel secchio `0.5s – 0.75s` (`BucketCounts[8] = 1`). |
| `Exemplars` | **Ponte tra Metriche e Tracce!** Allega un esempio reale (`Value`, `Time`, `TraceID`, `SpanID` in base64) così su Grafana/Prometheus puoi cliccare su un picco del grafico e saltare direttamente alla traccia specifica su Jaeger! |
