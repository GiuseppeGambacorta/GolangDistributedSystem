# Golang Distributed System — Guida agli Step e Indice Documentazione

Panoramica del progetto (Commit Log distribuito in Go con Protocol Buffers e gRPC), ordinata secondo gli step di sviluppo.

---

## Indice dei file `.md` del progetto

| # | File | Step / Area | Contenuto |
| :--- | :--- | :--- | :--- |
| 1 | [`log/store e index.md`](log/store%20e%20index.md) | Step 1 — `log/` | Schema visivo del formato su disco: file `.index` (entry fisse da 12 B) che punta ai record nel file `.store` (8 B lunghezza + dati). |
| 2 | [`log/segment.md`](log/segment.md) | Step 1 — `log/` | Schema visivo del funzionamento di un `segment`: traduzione da offset globale a offset relativo (`baseOffset`), lookup nell'indice e deserializzazione Protobuf. |
| 3 | [`api/v1/proto.md`](api/v1/proto.md) | Step 2 — `api/v1/` | Guida completa a `log.proto`, RPC Unary e Streaming, gestione errori gRPC (`error.go`), dipendenze e comandi `protoc`. |
| 4 | [`grpc/README.md`](grpc/README.md) | Step 3 — `grpc/` | Flusso completo di avvio server/client, funzionamento delle 4 RPC (Unary e Streaming), meccanismo `notifyCh` a 0% CPU e test multi-terminale. |
| 5 | [`appuntiGolang.md`](appuntiGolang.md) | Appunti trasversali | Approfondimenti Go usati nel progetto: Array vs Slice, operatore `[:]` (Zero-Allocation pattern) e controllo interfacce a compile-time. |

---

## Percorso e Step del Progetto

L'ordine logico seguito nello sviluppo (confermato anche dalla cronologia dei commit) è:

```text
1. log/ (Storage & Commit Log)  ──►  2. api/v1/ (Protobuf & Errori)  ──►  3. grpc/ (Server & Client)
```

*(Nota tecnica: nel codice finale `log/segment.go` e `log/log.go` importano `api/v1` per serializzare `api.Record` e restituire `api.ErrOffsetOutOfRange`; concettualmente si parte però dalla progettazione del log su disco.)*

---

### Step 1 — Commit Log su disco (`log/`)

Costruzione del motore di persistenza locale, dal livello più basso al livello più alto:

1. **Store (`log/store.go`)**: file `.store` append-only. Ogni record viene salvato preceduto da 8 byte (`BigEndian`) che ne indicano la lunghezza, usando `bufio.Writer` e array fisso sullo stack (`appuntiGolang.md`).
2. **Index (`log/index.go`)**: file `.index` a righe di dimensione fissa (**12 byte**: `4 B` offset relativo + `8 B` posizione nel file `.store`) per accesso diretto `O(1)`.
3. **Segment (`log/segment.go`)**: unisce una coppia `<baseOffset>.store` e `<baseOffset>.index`, gestisce il mapping tra offset globale e relativo e serializza/deserializza `Record` tramite Protobuf.
4. **Log & Config (`log/log.go`, `log/config.go`)**: coordina l'insieme dei segmenti su una directory, gestisce la rotazione automatica al raggiungimento dei limiti (`IsMaxed()`), la concorrenza con `sync.RWMutex`, la lettura/scrittura (`Append`, `Read`), il `Truncate` e il `Reader()` sequenziale.

- **Documentazione interna:** [`log/store e index.md`](log/store%20e%20index.md), [`log/segment.md`](log/segment.md).

---

### Step 2 — Contratto API con Protocol Buffers (`api/v1/`)

Definizione del modello dati e delle RPC esposte sulla rete:

1. **Definizione (`api/v1/log.proto`)**: definisce il messaggio `Record` e il servizio `Log` con 4 metodi:
   - `Produce` (Unary)
   - `Consume` (Unary)
   - `ConsumeStream` (Server-Side Streaming)
   - `ProduceStream` (Bidirectional Streaming)
2. **Generazione codice (`api/v1/log.pb.go`, `api/v1/log_grpc.pb.go`)**: generati tramite `make compile` (o comando `protoc` diretto).
3. **Errori gRPC (`api/v1/error.go`)**: definisce `ErrOffsetOutOfRange` con metodo `GRPCStatus()` che restituisce `codes.OutOfRange` arricchito da `errdetails.LocalizedMessage`.

- **Documentazione interna:** [`api/v1/proto.md`](api/v1/proto.md).

---

### Step 3 — Server e Client gRPC (`grpc/`)

Esposizione del Commit Log via rete gRPC:

- **`grpc/server.go`**: implementa `api.LogServer` collegandolo all'interfaccia `CommitLog`. Include un meccanismo di notifica broadcast (`notifyCh`) per svegliare i client in ascolto su `ConsumeStream` senza polling attivo.
- **`grpc/main.go`**: entrypoint eseguibile sia in modalità server (`-server`) sia come client CLI per testare `produce`, `consume`, `produce-stream` e `consume-stream`.

*(Prossimo step pianificato: aggiunta della sicurezza mTLS e autorizzazione ACL.)*

- **Documentazione interna:** [`grpc/README.md`](grpc/README.md).

---

### Appunti trasversali su Go

- **[`appuntiGolang.md`](appuntiGolang.md)**: note di linguaggio utili per leggere il codice di `log/` e `grpc/` (Array vs Slice, slicing `[:]`, Zero-Allocation pattern su stack e `var _ api.LogServer = (*grpcServer)(nil)`).

---

## Comandi Rapidi

```bash
# Compila i file .proto in api/v1/
make compile

# Esegue i test con race detector
make test

# Avvia il server gRPC (da grpc/)
go run ./grpc -server -addr 127.0.0.1:8080

# Esempi client CLI (in un altro terminale)
go run ./grpc -op produce -value "ciao"
go run ./grpc -op consume -offset 0
go run ./grpc -op consume-stream -offset 0
go run ./grpc -op produce-stream
```
