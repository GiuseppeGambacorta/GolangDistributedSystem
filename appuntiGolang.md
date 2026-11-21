# Appunti di Go — Pattern e Best Practice del Progetto

Raccolta di concetti e idiomi Go applicati in questo repository.

## Indice

1. [Array vs Slice, operatore `[:]` e Zero-Allocation Pattern](#1-array-vs-slice-operatore--e-zero-allocation-pattern)
2. [Controllo di implementazione interfaccia a compile-time](#2-controllo-di-implementazione-interfaccia-a-compile-time)
3. [Struct Embedding, "Override" e Decorator Pattern](#3-struct-embedding-override-e-decorator-pattern)

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
