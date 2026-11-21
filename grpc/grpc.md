# Guida a gRPC — Cos'è e Come Funziona

Questo documento spiega da zero cos'è **gRPC**, perché viene scelto nei sistemi distribuiti (come il nostro commit log) rispetto a REST/JSON e quali concetti chiave usiamo nel progetto.

> Per il flusso specifico del nostro server e client Go vedi [`grpc client server.md`](grpc%20client%20server.md). Per la sintassi dei file `.proto` vedi [`../api/v1/proto.md`](../api/v1/proto.md).

---

## 1. Cos'è gRPC (spiegato semplice)

**gRPC** è un framework open-source creato da Google per far comunicare programmi diversi attraverso la rete come se si stessero chiamando **normali funzioni locali**.

- **RPC (*Remote Procedure Call*)**: nel codice del client scrivi `client.Produce(ctx, req)` proprio come chiameresti una funzione normale in Go. Sotto il cofano, gRPC si occupa di impacchettare i dati, spedirli via rete al server, eseguire il metodo `Produce` sul server e riportarti indietro la risposta (o l'errore).
- **Due ingredienti fondamentali**:
  1. **Protocol Buffers (Protobuf)** per definire il contratto (dati e metodi) e serializzare i messaggi in formato **binario**.
  2. **HTTP/2** come protocollo di trasporto sulla rete.

```text
  CLIENT (Go)                                                 SERVER (Go)
  ───────────                                                 ───────────
  client.Produce(ctx, req)                                    func (s *grpcServer) Produce(...)
       │                                                               ▲
       ▼ (Stub generato da protoc)                                     │ (Dispatcher gRPC)
  Serializza in binario Protobuf ═════ HTTP/2 (su TLS/mTLS) ═════► Deserializza Protobuf
```

---

## 2. gRPC vs REST/JSON: perché usarlo qui?

| Caratteristica | REST / JSON | gRPC / Protobuf |
| :--- | :--- | :--- |
| **Formato dati** | Testuale (JSON, più pesante da leggere e parsare) | **Binario** (compatto e molto più veloce) |
| **Protocollo** | Tipicamente HTTP/1.1 | **HTTP/2** (multiplexing su una sola connessione TCP) |
| **Contratto API** | Convenzioni URL + documentazione separata (OpenAPI) | **Contratto forte** nel file `.proto` con codice client/server generato automaticamente |
| **Streaming** | Non nativo (servono WebSocket o SSE separati) | **Nativo** (server-side, client-side e bidirezionale) |
| **Uso ideale** | API pubbliche chiamate direttamente dai browser | **Microservizi interni**, commit log, sistemi distribuiti ad alte prestazioni |

---

## 3. I 4 pilastri di gRPC

### A. Il contratto `.proto` e la generazione del codice
Tutto parte da [`api/v1/log.proto`](../api/v1/log.proto). Eseguendo `make compile`, `protoc` genera:
- `log.pb.go`: le `struct` Go dei messaggi (`Record`, `ProduceRequest`, ecc.).
- `log_grpc.pb.go`:
  - lo **stub client** (`api.NewLogClient(conn)`), che espone i metodi pronti da chiamare;
  - l'**interfaccia server** (`api.LogServer`), che implementiamo in [`server.go`](server.go) e registriamo con `api.RegisterLogServer(...)`.

### B. HTTP/2, Porte Effimere e Multiplexing

Ci sono **due meccanismi distinti** (uno di TCP, uno di HTTP/2) che lavorano insieme quando i client parlano col server su `127.0.0.1:8080`:

1. **Più client sulla stessa porta del server (`8080`) — grazie a TCP**:
   Il server ascolta su **una sola porta** (`8080`), ma può gestire migliaia di client contemporaneamente. Il sistema operativo assegna a ogni client una **porta effimera** diversa (es. `51975`, `52010`, `52044`), e TCP distingue ogni connessione tramite la quaterna `(IP client, porta client, IP server, porta server)`:
   ```text
   Terminale Client A  (127.0.0.1:51975) ──┐
   Terminale Client B  (127.0.0.1:52010) ──┼──►  SERVER gRPC (127.0.0.1:8080)
   Terminale Client C  (127.0.0.1:52044) ──┘
   ```

2. **Tante RPC dello stesso client su UNA sola connessione — grazie a HTTP/2 Multiplexing**:
   Quando un client apre una connessione con `grpc.NewClient(...)`, viene fatto **un solo handshake TCP + mTLS** su quella porta effimera (es. `127.0.0.1:51975`). Da quel momento, **HTTP/2** divide la singola connessione TCP in tanti **stream binari indipendenti** (identificati da uno Stream ID):
   ```text
   Client A (127.0.0.1:51975) ═══ 1 sola connessione TCP + mTLS ═══► Server (127.0.0.1:8080)
                                    ├── Stream #1: Produce("ciao")
                                    ├── Stream #3: ProduceStream(...)
                                    ├── Stream #5: Produce("test")
                                    └── Stream #7: Consume(0)
   ```
   - **Ecco perché nel tuo `server.log` tutte e 4 le chiamate avevano lo stesso `"peer.address": "127.0.0.1:51975"`**: nella CLI (`cli/cli.go`) la connessione resta aperta per tutto il menu e HTTP/2 ci fa passare dentro tutte le chiamate senza riaprire socket né rifare mTLS ogni volta!
   - **Confronto diretto con HTTP/1.1**:
     - **HTTP/1.1 ("coda al supermercato" — 1 richiesta alla volta)**: anche con `Keep-Alive`, su una singola connessione TCP una richiesta deve aspettare la risposta prima di inviare la successiva (*Head-of-Line blocking*); per lavorare in parallelo il client deve aprire più connessioni TCP (es. 6 porte effimere + 6 handshake TLS).
       ```text
       [Richiesta 1] ──► (aspetta...) ──► [Risposta 1] ──► [Richiesta 2] ──► [Risposta 2]
       ```
     - **HTTP/2 / gRPC ("autostrada a più corsie" — tutto in parallelo su 1 connessione)**: i pacchetti sono divisi in frame binari etichettati (`Stream #1`, `Stream #3`, `Stream #5`...), quindi RPC singole e stream bidirezionali viaggiano insieme senza bloccarsi a vicenda.

### C. `context.Context` distribuito (Timeout e Cancellazione)
In gRPC ogni chiamata riceve un `context.Context` che **viaggia dal client al server**:
- Se sul client imposti un timeout di 5 secondi (`context.WithTimeout` in `runProduce`), e il server ci mette troppo, la chiamata viene annullata su entrambi i lati (`codes.DeadlineExceeded`).
- Se premi `Ctrl+C` sul client durante `ConsumeStream`, il `stream.Context().Done()` si chiude immediatamente anche sul server, liberando subito la goroutine.

### D. Sicurezza integrata (TLS / mTLS)
gRPC è pensato fin dall'inizio per girare su **TLS/mTLS** tramite `credentials.NewTLS(...)` (lato server con `grpc.Creds(...)`, lato client con `grpc.WithTransportCredentials(...)`). Senza TLS esplicito, il client deve dichiarare apposta `insecure.NewCredentials()`.

---

## 4. I 4 tipi di RPC in gRPC

| Tipo | Schema | Esempio nel nostro progetto |
| :--- | :--- | :--- |
| **1. Unary** | 1 richiesta → 1 risposta | `Produce` (invii 1 record, ricevi l'offset) e `Consume` (chiedi 1 offset, ricevi il record). |
| **2. Server-Side Streaming** | 1 richiesta → N risposte nel tempo | `ConsumeStream` (chiedi un offset iniziale e il server ti spedisce i record in tempo reale). |
| **3. Client-Side Streaming** | N richieste → 1 risposta finale | *(Non usato nel progetto; es. upload di un file grande a blocchi con conferma finale.)* |
| **4. Bidirectional Streaming** | N richieste ↔ N risposte in parallelo | `ProduceStream` (scrivi continuamente messaggi e ricevi gli ACK degli offset in parallelo). |

---

## 5. Interceptor (Middleware) e Codici di Stato

### Interceptor
Gli **interceptor** sono funzioni che gRPC esegue automaticamente **prima** (e volendo dopo) ogni RPC, proprio come i middleware in un server HTTP:
- **`grpc.ChainUnaryInterceptor(...)`**: gira prima delle chiamate Unary (`Produce`, `Consume`).
- **`grpc.ChainStreamInterceptor(...)`**: gira all'apertura degli stream (`ProduceStream`, `ConsumeStream`).

Nel nostro progetto li usiamo in [`server.go`](server.go) per leggere il `CommonName` (`"root"` / `"reader"`) dal certificato mTLS prima di entrare nei metodi del servizio (vedi [`../security/security grafico 03.md`](../security/security%20grafico%2003.md)).

### Codici di Stato (`google.golang.org/grpc/codes`)
Al posto dei codici HTTP (`200`, `403`, `404`), gRPC usa una lista standard di **status code**:

| Codice gRPC | Significato | Quando lo vedi nel nostro progetto |
| :--- | :--- | :--- |
| `codes.OK` | Tutto riuscito | Ogni chiamata andata a buon fine. |
| `codes.OutOfRange` | Indice fuori intervallo | `Consume` su un offset che non esiste ancora (`api.ErrOffsetOutOfRange`). |
| `codes.PermissionDenied` | Autenticato, ma senza permesso | Utente `reader` che prova a fare `produce` (bloccato da Casbin ACL). |
| `codes.Unauthenticated` | Identità mancante o non verificata | Certificato client mancante o non valido nell'interceptor. |
| `codes.Canceled` | Operazione annullata dal client | Premi `Ctrl+C` durante `consume-stream`. |
| `codes.Unavailable` | Server irraggiungibile o handshake fallito | Server spento, oppure client `-insecure` / `-no-client-cert` rifiutato da mTLS. |
| `codes.InvalidArgument` | Argomento non valido | Configurazione del server incompleta (`CommitLog` o `Authorizer` mancante). |
