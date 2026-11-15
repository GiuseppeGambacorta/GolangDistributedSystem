# Guida a Protocol Buffers (`.proto`) e gRPC

Questo documento spiega da zero la struttura e il funzionamento di un file **Protocol Buffers** (`.proto`) utilizzato per definire le API di un sistema di log distribuito basato su **gRPC**, inclusa la gestione degli errori in Go e i comandi per la compilazione.

---

## Il File `.proto` Completo

```protobuf
syntax = "proto3";

package log.v1;

option go_package = "distribuiti/api/v1;logv1";

message Record {
  uint64 offset = 1;
  bytes value = 2;
}

service Log {
  rpc Produce(ProduceRequest) returns (ProduceResponse) {}
  rpc Consume(ConsumeRequest) returns (ConsumeResponse) {}
  rpc ConsumeStream(ConsumeRequest) returns (stream ConsumeResponse) {}
  rpc ProduceStream(stream ProduceRequest) returns (stream ProduceResponse) {}
}

message ProduceRequest {
  bytes value = 1;
}

message ProduceResponse {
  uint64 offset = 1;
}

message ConsumeRequest {
  uint64 offset = 1;
}

message ConsumeResponse {
  Record record = 1;
}
```

---

## 1. Cos'è Protobuf?

**Protocol Buffers** (Protobuf) è un linguaggio di definizione dell'interfaccia (IDL) creato da Google. Serve a definire la struttura dei dati e i metodi di comunicazione tra client e server.

* **Contratto condiviso:** Il file `.proto` viene dato in pasto a un compilatore (`protoc`) che genera automaticamente il codice sorgente (in Go, Java, Python, C++, ecc.) per inviare e ricevere i dati.
* **Formato binario:** A differenza di JSON o XML (testuali), Protobuf serializza i dati in **binario**. È molto più veloce da elaborare e occupa pochissima banda sulla rete.

---

## 2. Intestazione e Configurazione

```protobuf
syntax = "proto3";
package log.v1;
option go_package = "distribuiti/api/v1;logv1";
```

* **`syntax = "proto3";`**: Specifica la versione del linguaggio Protobuf. `proto3` è lo standard attuale e deve sempre trovarsi alla prima riga.
* **`package log.v1;`**: Definisce un *namespace* (spazio dei nomi) per evitare conflitti di nomi con altri file `.proto` nel progetto (es. se esistono più messaggi chiamati `Record`).
* **`option go_package = "...";`**: È un'opzione specifica per **Go**. Indica al compilatore in quale cartella generare i file `.pb.go` (`distribuiti/api/v1`) e quale nome dare al package Go (`logv1`). Altri linguaggi ignorano questa riga.

---

## 3. Regole di Sintassi dei `message`

In Protobuf le strutture dati (equivalenti a `struct`, classi o oggetti JSON) si chiamano **`message`**. Ogni campo interno segue sempre la sintassi:

```protobuf
<tipo> <nome_campo> = <field_number>;
```

Ad esempio:

```protobuf
uint64 offset = 1;
```

1. **`uint64` (Tipo):** Il tipo di dato. Può essere primitivo (`uint64`, `bytes`, `string`, `bool`, `int32`) oppure un altro `message` definito nel file.
2. **`offset` (Nome):** Il nome della variabile nel codice generato (es. `record.Offset` in Go).
3. **`= 1` (Field Number / Tag):** **Non è un valore di default!** È un identificativo numerico univoco per quel campo.
   * **Perché esiste:** Sulla rete Protobuf non invia la stringa `"offset"`, ma solo il numero `1` per risparmiare byte.
   * **Scope locale:** I numeri sono **locali al singolo `message`**. Il numero `1` in `ProduceRequest` non entra in conflitto con il numero `1` in `ConsumeRequest`. L'importante è non ripetere lo stesso numero dentro le stesse parentesi graffe `{ ... }`.

---

## 4. Il Modello Dati Principale: `Record`

```protobuf
message Record {
  uint64 offset = 1;
  bytes value = 2;
}
```

Rappresenta una singola voce salvata all'interno di un **commit log** (registro sequenziale di eventi, simile ad Apache Kafka o a un Write-Ahead Log).

* **`offset`**: L'indice numerico progressivo (intero positivo a 64 bit) che identifica la posizione esatta del record nel log (`0`, `1`, `2`, ...).
* **`value`**: Il payload vero e proprio, espresso come array grezzo di `bytes` (può contenere testo, JSON, dati binari, ecc.).

---

## 5. Il Servizio gRPC (`service`)

```protobuf
service Log {
  rpc Produce(ProduceRequest) returns (ProduceResponse) {}
  rpc Consume(ConsumeRequest) returns (ConsumeResponse) {}
  rpc ConsumeStream(ConsumeRequest) returns (stream ConsumeResponse) {}
  rpc ProduceStream(stream ProduceRequest) returns (stream ProduceResponse) {}
}
```

Un blocco `service` definisce le **RPC** (*Remote Procedure Call*), cioè le funzioni che il server espone e che il client può invocare via rete.

* **Nomi dei metodi:** `Produce` e `Consume` non sono parole chiave obbligatorie di Protobuf, ma sono la **convenzione standard** nei sistemi distribuiti per indicare rispettivamente la scrittura e la lettura da un log.
* **La parola chiave `stream`:** A differenza dei nomi dei metodi, `stream` **è sintassi ufficiale di Protobuf/gRPC**. Indica che invece di un singolo pacchetto, viene aperto un flusso continuo di messaggi.

### Le 4 Modalità di Comunicazione Definite

| Metodo | Tipo di RPC | Funzionamento |
| :--- | :--- | :--- |
| **`Produce`** | **Unary** (1 a 1) | Il client invia 1 dato da scrivere $\rightarrow$ il server risponde con 1 conferma (l'offset assegnato). |
| **`Consume`** | **Unary** (1 a 1) | Il client chiede 1 record specifico tramite offset $\rightarrow$ il server restituisce quel singolo `Record`. |
| **`ConsumeStream`** | **Server-Side Streaming** (1 a N) | Il client invia 1 richiesta con un offset di partenza $\rightarrow$ il server tiene aperta la connessione e invia continuamente tutti i record successivi man mano che vengono scritti. |
| **`ProduceStream`** | **Bidirectional Streaming** (N a N) | Client e server aprono un canale bidirezionale: il client invia un flusso continuo di dati da scrivere e il server risponde in tempo reale con un flusso continuo di offset assegnati. |

---

## 6. I Messaggi di Richiesta e Risposta

In gRPC, ogni funzione `rpc` **deve** ricevere in input un `message` e restituire un `message` (non si possono passare tipi primitivi direttamente nella firma della funzione). Questo permette anche di aggiungere nuovi campi in futuro senza rompere la firma del metodo.

### 1. `ProduceRequest` (Richiesta di scrittura)
```protobuf
message ProduceRequest {
  bytes value = 1;
}
```
* **Chi lo manda:** Client $\rightarrow$ Server.
* **Ruolo:** Contiene solo i `bytes` da salvare. Non include l'offset perché è il server a decidere in quale posizione scrivere il dato.

### 2. `ProduceResponse` (Conferma di scrittura)
```protobuf
message ProduceResponse {
  uint64 offset = 1;
}
```
* **Chi lo manda:** Server $\rightarrow$ Client.
* **Ruolo:** È la ricevuta dell'operazione. Comunica al client a quale indice (`offset`) è stato salvato il dato appena inviato.

### 3. `ConsumeRequest` (Richiesta di lettura)
```protobuf
message ConsumeRequest {
  uint64 offset = 1;
}
```
* **Chi lo manda:** Client $\rightarrow$ Server.
* **Ruolo:** Indica al server l'indice (`offset`) da cui iniziare a leggere.

### 4. `ConsumeResponse` (Risposta di lettura)
```protobuf
message ConsumeResponse {
  Record record = 1;
}
```
* **Chi lo manda:** Server $\rightarrow$ Client.
* **Ruolo:** Messaggio contenitore che incapsula l'oggetto completo `Record` (quindi restituisce sia l'`offset` che il `value`).

---

## 7. Gestione degli Errori in gRPC (`error.go`)

In gRPC non basta restituire un normale errore testuale di Go (`errors.New(...)`), perché sulla rete viaggiano **codici di stato standardizzati** (simili ai codici HTTP) accompagnati da metadati strutturati.

```go
package logv1

import (
	"fmt"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ErrOffsetOutOfRange struct {
	Offset uint64
}

func (err ErrOffsetOutOfRange) GRPCStatus() *status.Status {
	grpcStatus := status.New(
		codes.OutOfRange,
		fmt.Sprintf("offset out of range: %d", err.Offset),
	)
	detail := &errdetails.LocalizedMessage{
		Locale:  "en-US",
		Message: fmt.Sprintf("The requested offset is outside the log's range: %d", err.Offset),
	}
	statusWithDetails, detailErr := grpcStatus.WithDetails(detail)
	if detailErr != nil {
		return grpcStatus
	}
	return statusWithDetails
}

func (err ErrOffsetOutOfRange) Error() string {
	return err.GRPCStatus().Err().Error()
}
```

### Come funziona questo pattern:

1. **La Struct (`ErrOffsetOutOfRange`):** Rappresenta l'errore specifico di quando un client richiede un `offset` che non esiste nel log. Salva l'indice richiesto per poterlo includere nei dettagli.
2. **Il Metodo `GRPCStatus() *status.Status`:** Implementa un'interfaccia interna della libreria gRPC. Quando il server restituisce questo errore, gRPC invoca automaticamente questo metodo per trasformarlo in un pacchetto di errore di rete (evitando un generico `codes.Unknown`):
   * **`status.New(codes.OutOfRange, ...)`**: Imposta il codice di stato standard gRPC `OutOfRange`.
   * **`errdetails.LocalizedMessage`**: Allega un messaggio Protobuf strutturato (ispezionabile dal client) con la descrizione leggibile e la lingua (`en-US`).
   * **`grpcStatus.WithDetails(detail)`**: Unisce lo status base e i dettagli extra.
3. **Il Metodo `Error() string`:** Implementa l'interfaccia nativa `error` di Go riutilizzando la descrizione generata da `GRPCStatus()`, garantendo coerenza sia nei log locali sia sulla rete.

---

## 8. Setup, Dipendenze e Comandi di Compilazione

### A. Plugin di Generazione Codice (da installare una sola volta)

Oltre al compilatore base `protoc`, servono i due plugin per generare rispettivamente le struct Protobuf e le interfacce client/server gRPC in Go:

```powershell
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

Verifica che `protoc` e i plugin siano nel `PATH` di sistema:

```powershell
protoc --version
protoc-gen-go --version
protoc-gen-go-grpc --version
```

### B. Dipendenze nel Progetto Go (`go.mod`)

Questi pacchetti servono al codice Go per compilare ed eseguire il programma:

```powershell
go get google.golang.org/protobuf
go get google.golang.org/grpc
go get google.golang.org/genproto/googleapis/rpc
```

* **`google.golang.org/protobuf/proto`**: Funzioni principali per serializzare e deserializzare i messaggi Protobuf.
* **`google.golang.org/grpc`**: Libreria core per creare server e client gRPC (include `codes` e `status`).
* **`google.golang.org/genproto/googleapis/rpc/errdetails`**: Strutture dati standard per arricchire gli errori gRPC con dettagli extra (es. `LocalizedMessage`).

### C. Comando di Compilazione (PowerShell)

Dalla cartella principale del progetto, esegui questo comando per generare i file `.pb.go` e `_grpc.pb.go`:

```powershell
protoc `
  --proto_path=. `
  --go_out=. `
  --go_opt=paths=source_relative `
  --go-grpc_out=. `
  --go-grpc_opt=paths=source_relative `
  api/v1/log.proto
```