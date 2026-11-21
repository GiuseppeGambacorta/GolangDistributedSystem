# Golang Distributed System — Guida agli Step e Indice Documentazione

Panoramica del progetto (Commit Log distribuito in Go con Protocol Buffers, gRPC e TLS/Sicurezza), ordinata secondo gli step di sviluppo.

---

## Indice dei file `.md` del progetto

| # | File | Step / Area | Contenuto |
| :--- | :--- | :--- | :--- |
| 1 | [`log/store e index.md`](log/store%20e%20index.md) | Step 1 — `log/` | Schema visivo del formato su disco: file `.index` (entry fisse da 12 B) che punta ai record nel file `.store` (8 B lunghezza + dati). |
| 2 | [`log/segment.md`](log/segment.md) | Step 1 — `log/` | Schema visivo del funzionamento di un `segment`: traduzione da offset globale a offset relativo (`baseOffset`), lookup nell'indice e deserializzazione Protobuf. |
| 3 | [`api/v1/proto.md`](api/v1/proto.md) | Step 2 — `api/v1/` | Guida completa a `log.proto`, RPC Unary e Streaming, gestione errori gRPC (`error.go`), dipendenze e comandi `protoc`. |
| 4 | [`grpc/grpc client server.md`](grpc/grpc%20client%20server.md) | Step 3 — `grpc/` | Flusso completo di avvio server/client, funzionamento delle 4 RPC (Unary e Streaming), meccanismo `notifyCh` a 0% CPU e test multi-terminale. |
| 5 | [`security/security.md`](security/security.md) | Step 4 — `security/` | Guida a CA privata con CFSSL, configurazioni CSR (CA, server, client), `cert_paths.go`, `BuildTLSConfig()` in `tls.go` e generazione certificati in `security/certs/`. |
| 6 | [`security/security grafico 00.md`](security/security%20grafico%2000.md) | Step 4 — `security/` | Schema visivo 00: creazione certificati base e handshake TLS a 1 via (solo il client verifica il server). |
| 7 | [`security/security grafico 01.md`](security/security%20grafico%2001.md) | Step 4 — `security/` | Schema visivo 01: creazione certificato client e handshake **mTLS a 2 vie** (anche il server autentica il client). |
| 8 | [`appuntiGolang.md`](appuntiGolang.md) | Appunti trasversali | Approfondimenti Go usati nel progetto: Array vs Slice, operatore `[:]` (Zero-Allocation pattern) e controllo interfacce a compile-time. |

---

## Percorso e Step del Progetto

L'ordine logico seguito nello sviluppo (confermato anche dalla cronologia dei commit) è:

```text
1. log/ (Storage & Commit Log)  ──►  2. api/v1/ (Protobuf & Errori)  ──►  3. grpc/ (Server & Client)  ──►  4. security/ (CA, Certificati & TLS)
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

- **Documentazione interna:** [`grpc/grpc client server.md`](grpc/grpc%20client%20server.md).

---

### Step 4 — Sicurezza: CA, Certificati e Configurazione TLS (`security/`)

Creazione dell'infrastruttura PKI locale e della configurazione TLS in una cartella dedicata alla radice del progetto:

- **`security/config/`**: configurazioni JSON per CFSSL (`01-ca-identita.json`, `02-ca-regole-firma.json`, `03-server-richiesta.json`, `04-client-richiesta.json`).
- **`security/certs/`**: directory di output per i 6 file generati da `make gencert` (`ca-public-cert.pem`, `ca-private-key.pem`, `server-public-cert.pem`, `server-private-key.pem`, `client-public-cert.pem`, `client-private-key.pem`), esclusa dal controllo versione in `.gitignore`.
- **`security/cert_paths.go`**: espone i percorsi ai certificati (`CAPublicCertPath`, `ServerPublicCertPath`, `ServerPrivateKeyPath`, `ClientPublicCertPath`, `ClientPrivateKeyPath`), risolvendoli automaticamente rispetto a `security/certs/` (o `CERTS_DIR` se impostata).
- **`security/tls.go`**: definisce `TLSOptions` e `BuildTLSConfig()` per costruire `*tls.Config` in modalità client o server (TLS e mTLS).

*(Stato attuale: **mTLS a 2 vie** già integrato in `grpc/server.go` e `grpc/main.go`. Prossimo sotto-step: **ACL con Casbin** per i permessi di `produce`/`consume`.)*

- **Documentazione interna:** [`security/security.md`](security/security.md), [`security/security grafico 00.md`](security/security%20grafico%2000.md), [`security/security grafico 01.md`](security/security%20grafico%2001.md).

---

### Appunti trasversali su Go

- **[`appuntiGolang.md`](appuntiGolang.md)**: note di linguaggio utili per leggere il codice di `log/` e `grpc/` (Array vs Slice, slicing `[:]`, Zero-Allocation pattern su stack e `var _ api.LogServer = (*grpcServer)(nil)`).

---

## Comandi Rapidi

```bash
# Crea security/certs/ e genera CA + certificati server con CFSSL
make init
make gencert

# Compila i file .proto in api/v1/
make compile

# Esegue i test con race detector
make test

# Avvia il server gRPC con mTLS attivo (prerequisito: make gencert)
go run ./grpc -server -addr 127.0.0.1:8080

# Esempi client CLI con mTLS (in un altro terminale)
go run ./grpc -op produce -value "ciao"
go run ./grpc -op consume -offset 0
go run ./grpc -op consume-stream -offset 0
go run ./grpc -op produce-stream

# Verifica rifiuto client senza certificato proprio (TLS 1-way) e senza TLS
go run ./grpc -no-client-cert -op produce -value "senza passaporto"
go run ./grpc -insecure -op produce -value "in chiaro"
```

---

## Librerie e Tool Utilizzati

Elenco aggiornato delle dipendenze presenti in [`go.mod`](go.mod), dei principali package della Standard Library e dei tool CLI esterni usati nel progetto.

### Dipendenze Go dirette (`go.mod`)

| Libreria | Versione | Dove viene usata | A cosa serve |
| :--- | :--- | :--- | :--- |
| `google.golang.org/protobuf` | `v1.36.12` | `log/segment.go`, `api/v1/log.pb.go` | Serializzazione e deserializzazione binaria dei messaggi Protobuf (`proto.Marshal` / `proto.Unmarshal`). |
| `google.golang.org/grpc` | `v1.85.0` | `grpc/`, `api/v1/log_grpc.pb.go`, `api/v1/error.go` | Server e client gRPC (Unary e Streaming), codici/stati di errore (`codes`, `status`) e trasporto sicuro TLS/mTLS (`credentials.NewTLS`, `credentials/insecure`). |
| `google.golang.org/genproto/googleapis/rpc` | `v0.0.0-20261110000943-09e21b237f2c` | `api/v1/error.go` | Tipi standard per arricchire gli errori gRPC con metadati strutturati (`errdetails.LocalizedMessage`). |

> **Dipendenze indirette (`// indirect` in `go.mod`):** `golang.org/x/net`, `golang.org/x/sys` e `golang.org/x/text` vengono incluse automaticamente da `google.golang.org/grpc` per il trasporto HTTP/2, le chiamate di sistema e la gestione del testo.

### Principali package della Standard Library Go

| Package | Dove viene usato | A cosa serve |
| :--- | :--- | :--- |
| `crypto/tls`, `crypto/x509` | `security/tls.go` | Configurazione handshake TLS/mTLS (`tls.Config`), caricamento coppie certificato/chiave e pool di CA fidate (`x509.CertPool`). |
| `encoding/binary`, `bufio`, `os`, `sync` | `log/` | Formato binario su disco (`BigEndian`), scrittura bufferizzata su `.store`, I/O file e sincronizzazione concorrente (`sync.Mutex`, `sync.RWMutex`). |
| `maps`, `slices`, `errors`, `context`, `os/signal` | `log/`, `grpc/` | Idiomi Go moderni (`slices.Sorted(maps.Keys(...))`, `errors.AsType`), timeout RPC e chiusura pulita su `Ctrl+C` (`signal.NotifyContext`). |

### Tool CLI esterni (`Makefile`)

| Tool | Target `Makefile` | A cosa serve |
| :--- | :--- | :--- |
| `protoc`, `protoc-gen-go`, `protoc-gen-go-grpc` | `make compile` | Compila `api/v1/log.proto` generando le struct Go (`.pb.go`) e le interfacce client/server gRPC (`_grpc.pb.go`). |
| `cfssl`, `cfssljson` | `make gencert` | Crea la CA privata e firma i certificati TLS/mTLS salvandoli in `security/certs/`. |
| `openssl` | *(verifica manuale)* | Verifica la catena di firma (`openssl verify`) e ispeziona campi/SAN dei file `.pem` (`openssl x509`). |

