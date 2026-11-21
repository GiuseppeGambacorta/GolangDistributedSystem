# Modulo `grpc/` — Flusso Server e Client

Documentazione del layer gRPC del progetto, basata su [`server.go`](server.go) e [`main.go`](main.go) (entrambi nello stesso `package main`).

---

## Struttura dei file

| File | Ruolo |
| :--- | :--- |
| [`server.go`](server.go) | Implementazione del servizio gRPC `api.LogServer` (`grpcServer`), disaccoppiata dal motore di storage tramite l'interfaccia `CommitLog`, con broadcast `notifyCh` per lo streaming in tempo reale. |
| [`main.go`](main.go) | Entrypoint unico eseguibile sia in modalità **server** (`-server`) sia come **client CLI** per le 4 RPC (`produce`, `consume`, `produce-stream`, `consume-stream`). |

---

## 1. Flusso di Avvio

### Avvio del Server (`-server`)

```text
main() [-server=true]
  │
  ▼
runServer(address)
  ├──► os.MkdirAll("data", 0755)                         # Crea/apre la cartella dati locale
  ├──► commitlog.NewLog("data", Config{})                # Apre i segmenti su disco (defer Close)
  ├──► security.BuildTLSConfig(ServerPublic/PrivateKey + CA, IsServer: true) # mTLS
  │      └──► credentials.NewTLS(...)                    # Adattatore credenziali gRPC
  ├──► security.NewAuthorizer(ACLModelPath, ACLPolicyPath) # Carica Casbin ACL (root/reader)
  ├──► NewGRPCServer(&Config{CommitLog, Authorizer}, grpc.Creds(...))
  │      ├──► ChainUnaryInterceptor / ChainStreamInterceptor (estrae CN dal certificato mTLS)
  │      └──► api.RegisterLogServer(...)                 # Registra l'implementazione su gRPC
  ├──► net.Listen("tcp", address)                        # Apre il socket TCP (default 127.0.0.1:8080)
  └──► grpcServer.Serve(listener)                        # Loop di accettazione richieste gRPC
```

Dettagli chiave in [`server.go`](server.go):
- **Disaccoppiamento**: `grpcServer` dipende dalle interfacce `CommitLog` (`Append` / `Read`) e `Authorizer` (`Authorize`).
- **Verifica a compile-time**: `var _ api.LogServer = (*grpcServer)(nil)` garantisce che `grpcServer` implementi tutti i metodi generati da [`api/v1/log.proto`](../api/v1/log.proto).
- **Sicurezza completa (mTLS + ACL)**: è attivo **mTLS a 2 vie** + **autorizzazione ACL con Casbin** tramite interceptor nativi gRPC (`unaryAuthInterceptor` / `streamAuthInterceptor`). Vedi [`../security/security grafico 02.md`](../security/security%20grafico%2002.md).

### Avvio del Client (`-server=false`, default)

```text
main() [-server=false]
  │
  ├──► -insecure (test):         insecure.NewCredentials()
  │    -no-client-cert (test):   security.BuildTLSConfig(CAPublicCertPath)  [TLS 1-way senza cert client]
  │    default (mTLS):           security.BuildTLSConfig(Root/Reader cert + key + CAPublicCertPath, in base a -user)
  ├──► grpc.NewClient(address, grpc.WithTransportCredentials(clientCreds))
  ├──► api.NewLogClient(conn)                     # Stub client generato da protoc-gen-go-grpc
  ├──► signal.NotifyContext(..., SIGINT, SIGTERM) # Contesto cancellabile con Ctrl+C
  └──► switch *op
         ├── "produce"        ──► runProduce()
         ├── "consume"        ──► runConsume()
         ├── "produce-stream" ──► runProduceStream()
         └── "consume-stream" ──► runConsumeStream()
```

---

## 2. Flusso delle 4 RPC (Client ↔ Server ↔ `CommitLog`)

### 2.1 `Produce` (Unary: 1 richiesta → 1 risposta)

1. **Client (`runProduce`)**: crea un context derivato con timeout di **5 secondi** (`context.WithTimeout`) e invia `ProduceRequest{Record: &api.Record{Value: ...}}`.
2. **Server (`Produce`)**:
   - chiama `server.CommitLog.Append(req.Record)` che scrive su disco e assegna l'`offset` progressivo;
   - chiama `server.notifyConsumers()` per svegliare eventuali `ConsumeStream` in attesa;
   - restituisce `ProduceResponse{Offset: offset}`.
3. **Client**: stampa l'offset assegnato e termina.

---

### 2.2 `Consume` (Unary: 1 richiesta → 1 risposta)

1. **Client (`runConsume`)**: imposta un timeout di **5 secondi** e invia `ConsumeRequest{Offset: offset}`.
2. **Server (`Consume`)**: chiama `server.CommitLog.Read(req.Offset)` e restituisce `ConsumeResponse{Record: record}`.
   - Se l'offset non esiste nel log, `CommitLog.Read` restituisce `api.ErrOffsetOutOfRange`; tramite il metodo `GRPCStatus()` definito in [`api/v1/error.go`](../api/v1/error.go), gRPC lo converte automaticamente nello status di rete `codes.OutOfRange`.
3. **Client**: stampa `offset` e `value` del record (oppure termina con errore se l'offset è fuori range).

---

### 2.3 `ProduceStream` (Bidirectional Streaming: N richieste ↔ N risposte)

Permette di inviare record in sequenza dalla tastiera ricevendo gli ACK in modo asincrono mentre si continua a scrivere.

```text
Client (main goroutine)                    Server (ProduceStream)               Client (goroutine Recv)
───────────────────────                    ──────────────────────               ───────────────────────
bufio.Scanner(os.Stdin)
  │
  ├─ legge riga ──► stream.Send(req) ───► stream.Recv()
  │                                          │
  │                                          ├─ server.Produce()
  │                                          │    ├─ CommitLog.Append()
  │                                          │    └─ notifyConsumers()
  │                                          │
  │                                          └─ stream.Send(res) ─────────────► stream.Recv()
  │                                                                               └─ stampa [Server ACK]
  ├─ "exit" o Ctrl+D
  ├─ stream.CloseSend() ────────────────► stream.Recv() == io.EOF ──► return nil
  └─ attende <-done ◄─────────────────────────────────────────────────────────── io.EOF ──► close(done)
```

Punti importanti:
- Sul lato **client**, la lettura delle conferme (`stream.Recv()`) gira su una **goroutine dedicata**, coordinata col thread principale tramite il canale `done`.
- Sul lato **server**, `ProduceStream` delega ogni messaggio ricevuto a `server.Produce(...)`, quindi anche le scritture in streaming notificano automaticamente tutti i consumer attivi su `ConsumeStream`.

---

### 2.4 `ConsumeStream` (Server-Side Streaming: 1 richiesta → flusso continuo)

Consente a uno o più consumer di restare in ascolto in tempo reale sui nuovi record a **0% CPU** quando non ci sono dati nuovi.

```text
  Producer                           grpcServer                              ConsumerStream
  ────────                           ──────────                              ──────────────
                                                                             1. waitCh := waitChannel()
                                                                                (prende snapshot di notifyCh
                                                                                 PRIMA di chiamare Consume)
                                                                             2. Consume(req.Offset)
                                                                                └─ ErrOffsetOutOfRange!
                                                                             3. select:
                                                                                  ├─ <-waitCh (bloccato, 0% CPU)
                                                                                  └─ <-stream.Context().Done()
  Produce / ProduceStream
    ├─ CommitLog.Append()
    └─ notifyConsumers()
         ├─ mu.Lock()
         ├─ close(notifyCh)  ─────────────────────────────────────────────►  4. <-waitCh si sblocca subito!
         └─ notifyCh = make(chan struct{})                                   5. continue: rilegge req.Offset,
            mu.Unlock()                                                         invia Record, req.Offset++
```

#### Perché il pattern `waitChannel()` + `close(notifyCh)` è fondamentale
1. **Broadcast reale**: in Go, `close(ch)` sblocca contemporaneamente **tutte** le goroutine ferme su `<-ch` (mentre inviare un valore su un canale ne sveglierebbe una sola). Subito dopo la chiusura, sotto mutex, viene creato un nuovo canale aperto `make(chan struct{})` per il ciclo successivo.
2. **Nessuna race condition (Lost Wakeup)**: `waitCh := server.waitChannel()` viene eseguito **prima** di `server.Consume(...)`. Se un `Produce` avviene esattamente nell'istante tra `Read()` (che ha visto l'offset mancante) e l'ingresso nel `select`, `waitCh` è già stato chiuso dal `Produce` e il `select` si sblocca immediatamente senza perdere la notifica.
3. **Chiusura pulita**: se l'utente preme `Ctrl+C` sul client, il contesto viene cancellato; il server esce dal `select` su `<-stream.Context().Done()` restituendo `nil`, e il client intercetta `codes.Canceled` terminando senza errori.

---

## 3. Flag CLI e Test Multi-Terminale

### Flag disponibili (`main.go`)

| Flag | Default | Descrizione |
| :--- | :--- | :--- |
| `-server` | `false` | Avvia il processo in modalità server gRPC invece che client. |
| `-addr` | `127.0.0.1:8080` | Indirizzo TCP su cui il server ascolta o a cui il client si connette. |
| `-op` | `produce` | Operazione client: `produce`, `consume`, `produce-stream`, `consume-stream`. |
| `-value` | `hello world` | Valore testuale del record per `-op produce`. |
| `-offset` | `0` | Offset di partenza per `-op consume` o `-op consume-stream`. |
| `-user` | `root` | Identità client (certificato mTLS): `root` (legge+scrive) o `reader` (solo lettura). |
| `-insecure` | `false` | Disattiva TLS sul client per verificare che il server rifiuti connessioni in chiaro. |
| `-no-client-cert` | `false` | Usa TLS a 1 via senza passaporto client per verificare che il server mTLS rifiuti client anonimi. |

> **Nota sul comando `go run`:** esegui sempre `go run ./grpc ...` dalla root del repository (oppure `go run . ...` da dentro `grpc/`). Non usare `go run main.go`, perché `server.go` appartiene allo stesso `package main`.

### Esempio pratico su 3 terminali

**Prerequisito (una sola volta, genera i certificati in `security/certs/`):**
```bash
make gencert
```

**Terminale 1 — Avvia il server (con TLS attivo):**
```bash
go run ./grpc -server
```

**Terminale 2 — Apri un consumer in streaming dall'offset 0 (con TLS attivo):**
```bash
go run ./grpc -op consume-stream -offset 0
```

**Terminale 3 — Invia record (singoli o in streaming) e osservali apparire in tempo reale nel Terminale 2:**
```bash
# Invio singolo
go run ./grpc -op produce -value "primo messaggio"

# Lettura singola
go run ./grpc -op consume -offset 0

# Oppure invio interattivo continuo ('exit' o Ctrl+D per terminare)
go run ./grpc -op produce-stream
```

**Prova del 9 — Verifica ACL (`reader` vs `root`) e rifiuti mTLS:**
```bash
# 1. reader può leggere (OK):
go run ./grpc -user reader -op consume -offset 0

# 2. reader NON può scrivere -> PermissionDenied:
go run ./grpc -user reader -op produce -value "non autorizzato"

# 3. Client senza certificato proprio (TLS 1-way) -> rifiutato dal server mTLS:
go run ./grpc -no-client-cert -op produce -value "senza passaporto"

# 4. Client senza TLS -> rifiutato dal server mTLS:
go run ./grpc -insecure -op produce -value "in chiaro"
```
