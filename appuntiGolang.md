# Appunti di Go — Pattern e Best Practice del Progetto

Raccolta di concetti e idiomi Go applicati in questo repository.

## Indice

1. [Array vs Slice, operatore `[:]` e Zero-Allocation Pattern](#1-array-vs-slice-operatore--e-zero-allocation-pattern)
2. [Controllo di implementazione interfaccia a compile-time](#2-controllo-di-implementazione-interfaccia-a-compile-time)
3. [Struct Embedding, "Override" e Decorator Pattern](#3-struct-embedding-override-e-decorator-pattern)
4. ["Accept interfaces, return structs" (Interfacce lato consumer)](#4-accept-interfaces-return-structs-interfacce-lato-consumer)
5. [Broadcast a 0% CPU con `close(chan struct{})` e prevenzione Lost Wakeup](#5-broadcast-a-0-cpu-con-closechan-struct-e-prevenzione-lost-wakeup)
6. [`context.WithValue` con chiave `struct{}` privata e `signal.NotifyContext`](#6-contextwithvalue-con-chiave-struct-privata-e-signalnotifycontext)
7. [Gestione moderna degli errori (`%w`, `errors.Is`, `errors.AsType`, `errors.Join`)](#7-gestione-moderna-degli-errori-w-errorsis-errorsastype-errorsjoin)
8. [`sync.RWMutex` vs `sync.Mutex`](#8-syncrwmutex-vs-syncmutex)
9. [Prevenzione leak di risorse / file descriptor su errore parziale](#9-prevenzione-leak-di-risorse--file-descriptor-su-errore-parziale)

---

## 1. Array vs Slice, operatore `[:]` e Zero-Allocation Pattern

In Go, **array** (`[N]T`) e **slice** (`[]T`) sono due tipi di dato fondamentalmente diversi per rappresentazione in memoria, semantica di passaggio e comportamento del compilatore.

### 1.1 Differenze strutturali tra Array e Slice

| Caratteristica | Array (`[8]byte`) | Slice (`[]byte`) |
| :--- | :--- | :--- |
| **Dimensione** | Fissa e parte integrante del tipo (`[8]byte` ≠ `[16]byte`) | Dinamica |
| **Semantica di passaggio** | **Per valore** (viene copiato l'intero contenuto) | **Per descrittore** (viene copiato solo l'header di 24 byte) |
| **Allocazione tipica** | Stack (se non "scappa" dalla funzione) | Heap (spesso, se passata a interfacce o di dimensione dinamica) |
| **Compatibilità con stdlib** | Rara (quasi nessuna funzione accetta `[N]byte`) | Standard (`io.Reader`, `io.Writer`, `encoding/binary`, ecc.) |

#### Rappresentazione in memoria

* **Array `[8]byte`**: È una sequenza contigua di 8 byte puri in memoria. Non ha metadati aggiuntivi.
* **Slice `[]byte`**: È una struttura (*slice header*) composta da 3 word (24 byte su architetture a 64-bit):
  1. `Data`: puntatore al primo elemento dell'array sottostante.
  2. `Len`: numero di elementi attualmente accessibili.
  3. `Cap`: capacità massima prima di dover riallocare.

### 1.2 Come funziona l'operatore di slicing `[:]`

In Go esiste l'operatore di **slicing** con sintassi generale:

```go
a[low:high]
```

Questa espressione estrae una **vista (slice)** degli elementi di `a` partendo dall'indice `low` (incluso) fino all'indice `high` (escluso):
* Lunghezza risultante (`Len`): `high - low`
* Capacità risultante (`Cap`): `cap(a) - low`

#### I valori di default (omissione degli indici)

| Sintassi | Equivalente esteso | Significato |
| :--- | :--- | :--- |
| `a[low:high]` | `a[low:high]` | Dall'indice `low` fino a `high - 1` |
| `a[:high]` | `a[0:high]` | Dall'inizio (`0`) fino a `high - 1` |
| `a[low:]` | `a[low:len(a)]` | Dall'indice `low` fino alla fine |
| **`a[:]`** | **`a[0:len(a)]`** | **L'intera sequenza, dall'inizio alla fine** |

#### Cosa succede sotto il cofano con `lengthBytes[:]`
Quando applichi `[:]` a un array `[8]byte`:

1. **Zero copie:** Non vengono duplicati gli 8 byte in memoria.
2. **Zero allocazioni heap:** Viene costruito solo lo *slice header* sullo stack con:
   * `Data = &lengthBytes[0]` (puntatore al primo byte dell'array)
   * `Len = 8`
   * `Cap = 8`
3. **Condivisione della memoria:** Qualsiasi modifica fatta tramite la slice (`lengthBytes[:]`) scrive direttamente dentro i byte dell'array originale `lengthBytes`.

```text
Stack Frame:
┌───────────────────────────┐
│ lengthBytes [8]byte       │ ◄───┐
│ [ 0x00 | 0x00 | ... | 42 ]│     │
└───────────────────────────┘     │
                                  │
┌───────────────────────────┐     │
│ Slice Header ([]byte)     │     │
│ Data ─────────────────────┼─────┘
│ Len: 8                    │
│ Cap: 8                    │
└───────────────────────────┘
```

### 1.3 Perché non si può passare direttamente un array (`lengthBytes`)

In Go non esiste alcuna conversione implicita tra `[8]byte` e `[]byte`.

```go
var lengthBytes [8]byte
binary.BigEndian.PutUint64(lengthBytes, 42) // ERRORE DI COMPILAZIONE
```

Il compilatore restituisce:
```text
cannot use lengthBytes (variable of type [8]byte) as []byte value in argument
```

I motivi sono due:
1. **Type Safety:** `[8]byte` e `[]byte` sono tipi distinti nel type system di Go.
2. **Semantica di mutazione:** Se Go copiasse `[8]byte` per valore dentro `PutUint64`, la funzione modificherebbe una copia locale e al ritorno `lengthBytes` risulterebbe ancora vuoto (`[0 0 0 0 0 0 0 0]`). Per modificare i dati originali serve un puntatore, che è esattamente ciò che contiene lo *slice header* generato da `[:]`.

### 1.4 Vantaggio prestazionale: Zero-Allocation Pattern

Quando si scrivono sistemi ad alto throughput (come Commit Log, WAL o parser di rete), evitare allocazioni sull'heap riduce drasticamente il carico sul **Garbage Collector (GC)**.

#### Approccio con `make` (possibile allocazione Heap)
```go
lengthBytes := make([]byte, 8)
buf.Write(lengthBytes)
```
Poiché `buf.Write` accetta una slice e può potenzialmente conservarne il riferimento (*escape analysis*), il compilatore Go spesso sposta `lengthBytes` sull'**heap**.

#### Approccio con Array fisso + `[:]` (Stack Allocation)
```go
var lengthBytes [8]byte
encoding.PutUint64(lengthBytes[:], uint64(len(record)))
buf.Write(lengthBytes[:])
```
In questo caso:
1. `var lengthBytes [8]byte` riserva 8 byte direttamente nello **stack frame** della funzione.
2. `lengthBytes[:]` crea una vista `[]byte` su quei 8 byte a costo zero.
3. Quando la funzione termina, lo stack frame viene semplicemente rilasciato senza alcun intervento del Garbage Collector (**0 allocazioni heap**).

---

## 2. Controllo di implementazione interfaccia a compile-time

In Go le interfacce sono **implicite** (non esiste `implements`). Per forzare il compilatore a verificare subito che una struct implementi tutti i metodi di un'interfaccia si usa questa riga:

```go
var _ api.LogServer = (*grpcServer)(nil)
```

Come funziona:
- `(*grpcServer)(nil)` crea un puntatore `nil` tipizzato a `grpcServer` (**0 allocazioni in memoria**).
- Assegnandolo alla variabile anonima `_` di tipo `api.LogServer`, se manca un metodo o una firma cambia nel `.proto`, il codice **non compila** invece di fallire a runtime.

---

## 3. Struct Embedding, "Override" e Decorator Pattern

In Go non esiste l'ereditarietà con `extends` (come in Java/C#), ma esiste lo **struct embedding**: se inserisci un tipo (una `struct` o un'interfaccia) dentro un'altra `struct` **senza dargli un nome di campo**, i suoi campi e metodi vengono **promossi automaticamente** alla struct esterna.

### 3.1 Esempio intuitivo: `Macchina` e `Motore`

```go
type Motore struct {
	Cavalli int
}

func (m Motore) TurnOn() {
	fmt.Println("Motore acceso!")
}

type Macchina struct {
	Motore  // <--- embedding (nessun nome di campo!)
	Modello string
}
```

Grazie all'embedding puoi scrivere direttamente:

```go
// Forma esplicita (funziona in tutte le versioni di Go):
auto := Macchina{Motore: Motore{Cavalli: 150}, Modello: "Golf"}

// Da Go 1.27+ (Relaxed Field Selectors in Struct Literals):
// puoi inizializzare i campi promossi direttamente senza nominare Motore!
auto2 := Macchina{Cavalli: 150, Modello: "Golf"}

auto.TurnOn()             // promosso da Motore!
fmt.Println(auto.Cavalli) // anche i campi vengono promossi!
```

### 3.2 Fare una sorta di "override" (e chiamare `super`)

Se definisci un metodo con lo **stesso nome** sulla struct esterna (`Macchina`), questo **sostituisce (shadowing/override)** quello promosso, e dentro puoi comunque chiamare l'originale passando dal nome del tipo incorporato (come `super` in Java):

```go
func (m Macchina) TurnOn() {
	fmt.Println("Controllo cinture...")
	m.Motore.TurnOn() // chiama il metodo originale di Motore ("super")
}
```

> **Attenzione alla leggibilità:** in Go **non esiste** una keyword `@Override` come in Java, quindi abusare dell'embedding su struct normali può rendere poco chiaro da dove arrivi un campo o se un metodo stia facendo override. Regola pratica:
> - **Sì all'embedding** per il **Decorator Pattern su interfacce** (es. `loggingCreds` o `authenticatedServerStream`), dove evita boilerplate inutile di metodi pass-through.
> - **Meglio un campo con nome esplicito** (`motore Motore` → `auto.motore.TurnOn()`) nelle normali struct di dominio, quando vuoi massima chiarezza a colpo d'occhio.

### 3.3 Dove lo usiamo in questo progetto (Decorator Pattern)

1. **`loggingCreds`** (`grpc/server.go`): incorpora `credentials.TransportCredentials`, eredita tutti i metodi TLS di gRPC e fa l'override solo di `ServerHandshake()` per loggare gli handshake TLS/mTLS falliti su `server.log`, richiamando `c.TransportCredentials.ServerHandshake(rawConn)`.
2. **`authenticatedServerStream`** (`grpc/server.go`): incorpora `grpc.ServerStream`, eredita `Send`/`Recv` e fa l'override solo di `Context()` per restituire il context con dentro `"root"` / `"reader"`.
3. **`store`** (`log/store.go`): incorpora `*os.File`, ereditando direttamente metodi come `Name()`.

---

## 4. "Accept interfaces, return structs" (Interfacce lato consumer)

Uno dei principi cardine di Go è: **i costruttori restituiscono struct concrete, mentre chi usa un servizio (il *consumer*) definisce l'interfaccia minima di cui ha bisogno**.

### Differenza con Java
- **In Java** (interfacce esplicite con `implements`): l'interfaccia la scrive il **produttore** (es. `interface Database` con 10 metodi), e chi la usa deve portarseli dietro tutti.
- **In Go** (interfacce implicite, niente `implements`): il **produttore** esporta solo la struct concreta, mentre ogni **consumer** dichiara la propria mini-interfaccia solo coi metodi che chiama davvero.

### Esempio completo (indipendente da qualsiasi progetto)

```text
  [package db] (produttore)              [package checkout] (consumer)
  ─────────────────────────              ─────────────────────────────
  struct PostgresDB                      interface OrderSaver        ◄── UNICA interfaccia (minimale!)
   • SaveOrder()  ◄──────────────────────── • SaveOrder()
   • FindOrder()                         struct CheckoutService
   • DeleteOrder()                          • saver OrderSaver       ◄── accetta l'interfaccia
   • Migrate()
   • Close()

                          [main.go]
                          ─────────
        pg := NewPostgresDB(...)          // 1. crea la struct concreta
        s  := NewCheckoutService(pg)      // 2. la passa: Go vede SaveOrder() -> OK!
        s.Buy("ORD-123")                  // 3. chiama pg.SaveOrder() attraverso l'interfaccia
```

```go
// =========================================================
// 1. PACKAGE PRODUTTORE (es. package db)
//    Nessuna interfaccia! Solo struct concreta con tanti metodi.
// =========================================================
type PostgresDB struct {
	dsn string
}

// Restituisce una STRUCT concreta (*PostgresDB), NON un'interfaccia!
func NewPostgresDB(dsn string) *PostgresDB {
	return &PostgresDB{dsn: dsn}
}

func (db *PostgresDB) SaveOrder(id string) error   { /* SQL INSERT */ return nil }
func (db *PostgresDB) FindOrder(id string) string  { /* SQL SELECT */ return id }
func (db *PostgresDB) DeleteOrder(id string) error { /* SQL DELETE */ return nil }
func (db *PostgresDB) Migrate() error              { /* migrazioni */ return nil }
func (db *PostgresDB) Close() error                { /* chiude pool */ return nil }

// =========================================================
// 2. PACKAGE CONSUMER (es. package checkout)
//    Usa SOLO SaveOrder -> definisce un'interfaccia da 1 solo metodo!
// =========================================================
type OrderSaver interface {
	SaveOrder(id string) error
}

type CheckoutService struct {
	saver OrderSaver // Accetta un'INTERFACCIA minima!
}

func NewCheckoutService(saver OrderSaver) *CheckoutService {
	return &CheckoutService{saver: saver}
}

func (s *CheckoutService) Buy(orderID string) error {
	return s.saver.SaveOrder(orderID)
}

// =========================================================
// 3. UTILIZZO (in main.go e nei test)
// =========================================================
// In produzione: *PostgresDB implementa OrderSaver AUTOMATICAMENTE (senza "implements")!
pg := NewPostgresDB("postgres://localhost/shop")
checkout := NewCheckoutService(pg)
_ = checkout.Buy("ORD-123")

// Nei test: ti basta un finto DB con 1 solo metodo (non devi finti-implementare Migrate/Close/Delete!):
type FakeDB struct{ saved []string }

func (f *FakeDB) SaveOrder(id string) error {
	f.saved = append(f.saved, id)
	return nil
}

testCheckout := NewCheckoutService(&FakeDB{})
_ = testCheckout.Buy("ORD-TEST")
```

**Perché funziona così bene:**
- Il package `db` non deve importare `checkout` (zero dipendenze circolari).
- Se domani cambi `PostgresDB` con `MongoDB` o `RedisDB`, ti basta che abbia `SaveOrder(id string) error` e `CheckoutService` non cambia di una virgola.

---

## 5. Broadcast a 0% CPU con `close(chan struct{})` e prevenzione Lost Wakeup

In Go, inviare un valore su un canale (`ch <- struct{}{}`) sveglia **una sola** goroutine in ascolto; invece **`close(ch)`** sblocca istantaneamente **tutte** le goroutine ferme su `<-ch`.

In `grpc/server.go` lo usiamo per svegliare in broadcast tutti i `ConsumeStream` in attesa di nuovi record:

```go
func (server *grpcServer) waitChannel() <-chan struct{} {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.notifyCh
}

func (server *grpcServer) notifyConsumers() {
	server.mu.Lock()
	defer server.mu.Unlock()
	close(server.notifyCh)                // sveglia TUTTI i consumer in attesa!
	server.notifyCh = make(chan struct{}) // nuovo canale aperto per il prossimo ciclo
}
```

### Evitare la race condition (*Lost Wakeup*) — Sbagliato vs Corretto

**SBAGLIATO** (prende il canale *dopo* `Consume`):
```go
response, err := server.Consume(stream.Context(), req)
if _, ok := errors.AsType[api.ErrOffsetOutOfRange](err); ok {
	// BUG! Se un Produce avviene PROPRIO QUI (tra Consume e waitChannel),
	// chiude il VECCHIO notifyCh e ne crea uno NUOVO aperto:
	// io prendo il nuovo canale e resto bloccato per sempre finché non arriva un altro Produce!
	waitCh := server.waitChannel()
	<-waitCh
}
```

**CORRETTO** (snippet reale di `grpc/server.go` — prende lo snapshot *prima* di `Consume`):
```go
for {
	// 1. Prendo il canale PRIMA di leggere dal log
	waitCh := server.waitChannel()

	// 2. Provo a leggere
	response, err := server.Consume(stream.Context(), req)
	if err != nil {
		if _, ok := errors.AsType[api.ErrOffsetOutOfRange](err); ok {
			// Se un Produce è avvenuto tra passo 1 e passo 2, waitCh è GIÀ stato chiuso
			// quindi <-waitCh si sblocca all'istante senza perdere la notifica!
			// Altrimenti la goroutine dorme qui a 0% CPU.
			select {
			case <-waitCh:
				continue
			case <-stream.Context().Done():
				return nil
			}
		}
		return err
	}
	// ... invia response e incrementa req.Offset
}
```

---

## 6. `context.WithValue` con chiave `struct{}` privata e `signal.NotifyContext`

### Perché NON usare una `string` come chiave in `context.WithValue`

Se due package diversi usano entrambi la stringa `"user"`, **si sovrascrivono a vicenda** sullo stesso `context`:

```go
// SBAGLIATO: chiave stringa condivisa!
// Package auth (il tuo interceptor):
ctx = context.WithValue(ctx, "user", "root")

// Package esterno di logging/telemetry che passa dopo sulla stessa richiesta:
ctx = context.WithValue(ctx, "user", 12345) // <-- SOVRASCRIVE "root" con un int!

// Dentro Produce():
u, ok := ctx.Value("user").(string) // ok == false! Hai perso "root" (o panic senza ", ok")!
```

### Soluzione corretta: chiave `struct{}` privata (0 byte, zero collisioni)
In Go `ctx.Value(k)` confronta **sia il tipo sia il valore** della chiave. Una `struct{}` non esportata ha un tipo unico nel proprio package: nessun altro package può ricrearla, quindi **non può mai collidere**:

```go
type subjectContextKey struct{}

// Scrittura (in authenticate):
ctx = context.WithValue(ctx, subjectContextKey{}, "root")

// Lettura (in subject):
user, _ := ctx.Value(subjectContextKey{}).(string) // sempre "root", intoccabile da altri package!
```

### Shutdown pulito con `signal.NotifyContext`
Per intercettare `Ctrl+C` (`os.Interrupt`) e `SIGTERM` senza gestire manualmente canali di segnali:

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()
```
Quando premi `Ctrl+C`, `ctx.Done()` si chiude automaticamente: nel server avvia `grpcServer.GracefulStop()`, nello stream chiude `ConsumeStream` in modo pulito (`codes.Canceled`).

---

## 7. Gestione moderna degli errori (`%w`, `errors.Is`, `errors.AsType`, `errors.Join`)

In Go moderno **non** si confrontano gli errori con `err == ErrNotFound` o `switch err.(type)` perché si rompono appena qualcuno aggiunge contesto con `fmt.Errorf("...: %w", err)`.

### Casistiche d'uso a colpo d'occhio

| Strumento | Quando usarlo |
| :--- | :--- |
| **`fmt.Errorf("contesto: %w", err)`** | Quando rilanci un errore verso il chiamante e vuoi aggiungere contesto *senza perdere* l'errore originale (a differenza di `%v`, che lo trasforma in semplice testo!). |
| **`errors.Is(err, ErrSentinella)`** | Quando controlli un errore **sentinella** (una variabile fissa come `io.EOF` o `var ErrNotFound = errors.New("not found")`), anche se è stato wrappato più volte. |
| **`errors.AsType[MioErrore](err)`** *(Go 1.26+)* | Quando controlli un errore **struct custom con campi extra** (es. `Field`, `Offset`, `StatusCode`) e vuoi leggere quei campi. Sostituisce il vecchio `var target MioErrore; errors.As(err, &target)`. |
| **`errors.Join(err1, err2, ...)`** | Quando esegui più operazioni indipendenti (es. chiudi 3 file/connessioni o validi 5 campi di un form) e vuoi restituire **tutti** gli errori insieme senza scartarne nessuno (i `nil` vengono ignorati automaticamente). |

### Esempi completi

#### 7.1 Wrapping (`%w`) + `errors.Is` (errori sentinella)
```go
var ErrNotFound = errors.New("record not found")

func loadFromDB(id int) error {
	return ErrNotFound
}

func getUser(id int) error {
	if err := loadFromDB(id); err != nil {
		// %w incapsula ErrNotFound dentro un nuovo errore con più contesto
		return fmt.Errorf("getUser(%d): %w", id, err)
	}
	return nil
}

// Dal chiamante:
err := getUser(42)
// err == ErrNotFound          -> FALSE! (perché è stato wrappato)
// errors.Is(err, ErrNotFound) -> TRUE!  (scava tutta la catena %w)
if errors.Is(err, ErrNotFound) {
	fmt.Println("Rispondi 404 Not Found")
}
```

#### 7.2 `errors.AsType[T]` (Go 1.26+, errori struct con dati extra)
```go
type ValidationError struct {
	Field  string
	Reason string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("campo %s non valido: %s", e.Field, e.Reason)
}

func createUser(email string) error {
	return fmt.Errorf("createUser: %w", ValidationError{Field: "email", Reason: "manca @"})
}

// Dal chiamante: estrae direttamente la struct ValidationError (anche se wrappata!)
err := createUser("pippo")
if valErr, ok := errors.AsType[ValidationError](err); ok {
	fmt.Printf("Errore sul campo %s: %s\n", valErr.Field, valErr.Reason)
}
```

#### 7.3 `errors.Join` (aggregare più errori senza perderli)
```go
func closeResources(f1, f2, f3 *os.File) error {
	// Anche se f1.Close() fallisce, provo comunque a chiudere f2 e f3!
	// errors.Join ignora i nil; se tutti sono nil, restituisce nil.
	return errors.Join(f1.Close(), f2.Close(), f3.Close())
}
```

---

## 8. `sync.RWMutex` vs `sync.Mutex`

| Tipo | Comportamento | Quando usarlo |
| :--- | :--- | :--- |
| **`sync.Mutex`** | **1 sola goroutine alla volta** (sia letture sia scritture). | Quando le letture sono poche, molto veloci, oppure quando anche la "lettura" modifica stato interno (es. svuota un buffer con `buf.Flush()`). |
| **`sync.RWMutex`** | **Tanti lettori insieme** (`RLock`/`RUnlock`), ma **1 solo scrittore esclusivo** (`Lock`/`Unlock`). | Quando hai molte letture concorrenti e poche scritture (es. cache in memoria, lista segmenti di un log). |

```go
type Cache struct {
	mu   sync.RWMutex
	data map[string]string
}

func (c *Cache) Get(key string) string {
	c.mu.RLock() // Più goroutine possono entrare qui CONTEMPORANEAMENTE
	defer c.mu.RUnlock()
	return c.data[key]
}

func (c *Cache) Set(key, val string) {
	c.mu.Lock() // Esclusivo: aspetta che escano tutti i lettori e blocca i nuovi
	defer c.mu.Unlock()
	c.data[key] = val
}
```

---

## 9. Prevenzione leak di risorse / file descriptor su errore parziale

Quando una funzione apre **più risorse in sequenza** (es. 2 file, o un file + una connessione di rete), se la seconda operazione fallisce e fai subito `return nil, err`, **la prima risorsa resta aperta in memoria** (*resource / file descriptor leak*) fino alla chiusura del processo!

### SBAGLIATO (leak se fallisce `f2`)
```go
func openPair(path1, path2 string) (*os.File, *os.File, error) {
	f1, err := os.Open(path1)
	if err != nil {
		return nil, nil, err
	}
	f2, err := os.Open(path2)
	if err != nil {
		return nil, nil, err // BUG: f1 resta aperto per sempre!
	}
	return f1, f2, nil
}
```

### CORRETTO — Opzione A (chiusura esplicita sull'errore)
```go
func openPair(path1, path2 string) (*os.File, *os.File, error) {
	f1, err := os.Open(path1)
	if err != nil {
		return nil, nil, err
	}
	f2, err := os.Open(path2)
	if err != nil {
		_ = f1.Close() // Chiude f1 prima di uscire!
		return nil, nil, err
	}
	return f1, f2, nil
}
```

### CORRETTO — Opzione B (pattern `defer` condizionale, ideale con 3+ risorse)
```go
func openPair(path1, path2 string) (*os.File, *os.File, error) {
	ok := false

	f1, err := os.Open(path1)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if !ok {
			_ = f1.Close() // Scatta SOLO se la funzione esce con errore!
		}
	}()

	f2, err := os.Open(path2)
	if err != nil {
		return nil, nil, err // f1 viene chiuso automaticamente dal defer!
	}

	ok = true // Tutto riuscito: disattiva la chiusura di emergenza
	return f1, f2, nil
}
```


