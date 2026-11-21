# Modulo `grpc/` — Flusso Server e Client

Documentazione del layer gRPC del progetto, basata su [`server.go`](server.go) e [`client.go`](client.go) (`package grpcserver`), controllati dal menu interattivo in [`../cli/cli.go`](../cli/cli.go) (avviato da [`../main.go`](../main.go)).

---

## Struttura dei file

| File | Ruolo |
| :--- | :--- |
| [`server.go`](server.go) | Implementazione del servizio gRPC `api.LogServer` (`grpcServer`), disaccoppiata tramite le interfacce `CommitLog` e `Authorizer`, con interceptor mTLS nativi, `RunServer()` e broadcast `notifyCh`. |
| [`client.go`](client.go) | Creazione della connessione gRPC (`NewClient` con profili `root`, `reader`, `no-client-cert`, `insecure`) e le 4 operazioni `Produce`, `Consume`, `ProduceStream`, `ConsumeStream`. |
| [`../cli/cli.go`](../cli/cli.go) + [`../main.go`](../main.go) | CLI interattiva a frecce (`charmbracelet/huh`): lanci `go run .` dalla root senza nessun flag e gestisci sia il server sia il client. |

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

## 3. Avvio tramite CLI Interattiva (`go run .`)

Non servono più flag da riga di comando: dalla root del repository esegui semplicemente:

```bash
# Prerequisito (una sola volta, genera i certificati in security/certs/):
make gencert

# Avvia la CLI interattiva (apri 2 o 3 terminali: uno per il server, gli altri per i client)
go run .
```

Dal menu a frecce puoi scegliere:
1. **Avvia Server gRPC (mTLS + ACL)** (default `127.0.0.1:8080`, `Ctrl+C` arresta in modo pulito).
2. **Avvia Client interattivo** → scegli il profilo di sicurezza:
   - `root` *(mTLS — legge e scrive)*
   - `reader` *(mTLS — sola lettura; su `Produce` riceve `PermissionDenied` senza uscire dal menu)*
   - `no-client-cert` *(TLS 1-way — test rifiuto mTLS)*
   - `insecure` *(senza TLS — test rifiuto in chiaro)*
3. Poi esegui `Produce`, `Consume`, `ProduceStream`, `ConsumeStream` o cambi utente al volo restando sempre nel menu!
