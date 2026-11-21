# Array vs Slice in Go: L'operatore `[:]` e l'ottimizzazione dello Stack

In Go, **array** (`[N]T`) e **slice** (`[]T`) sono due tipi di dato fondamentalmente diversi per rappresentazione in memoria, semantica di passaggio e comportamento del compilatore.

---

## 1. Differenze strutturali tra Array e Slice

| Caratteristica | Array (`[8]byte`) | Slice (`[]byte`) |
| :--- | :--- | :--- |
| **Dimensione** | Fissa e parte integrante del tipo (`[8]byte` ≠ `[16]byte`) | Dinamica |
| **Semantica di passaggio** | **Per valore** (viene copiato l'intero contenuto) | **Per descrittore** (viene copiato solo l'header di 24 byte) |
| **Allocazione tipica** | Stack (se non "scappa" dalla funzione) | Heap (spesso, se passata a interfacce o di dimensione dinamica) |
| **Compatibilità con stdlib** | Rara (quasi nessuna funzione accetta `[N]byte`) | Standard (`io.Reader`, `io.Writer`, `encoding/binary`, ecc.) |

### Rappresentazione in memoria

* **Array `[8]byte`**: È una sequenza contigua di 8 byte puri in memoria. Non ha metadati aggiuntivi.
* **Slice `[]byte`**: È una struttura (*slice header*) composta da 3 word (24 byte su architetture a 64-bit):
  1. `Data`: puntatore al primo elemento dell'array sottostante.
  2. `Len`: numero di elementi attualmente accessibili.
  3. `Cap`: capacità massima prima di dover riallocare.

---

## 2. Come funziona l'operatore di slicing `[:]`

In Go esiste l'operatore di **slicing** con sintassi generale:

```go
a[low:high]
```

Questa espressione estrae una **vista (slice)** degli elementi di `a` partendo dall'indice `low` (incluso) fino all'indice `high` (escluso):
* Lunghezza risultante (`Len`): `high - low`
* Capacità risultante (`Cap`): `cap(a) - low`

### I valori di default (omissione degli indici)
Go permette di omettere `low`, `high`, o entrambi:

| Sintassi | Equivalente esteso | Significato |
| :--- | :--- | :--- |
| `a[low:high]` | `a[low:high]` | Dall'indice `low` fino a `high - 1` |
| `a[:high]` | `a[0:high]` | Dall'inizio (`0`) fino a `high - 1` |
| `a[low:]` | `a[low:len(a)]` | Dall'indice `low` fino alla fine |
| **`a[:]`** | **`a[0:len(a)]`** | **L'intera sequenza, dall'inizio alla fine** |

### Cosa succede sotto il cofano con `lengthBytes[:]`
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

---

## 3. Perché non si può passare direttamente un array (`lengthBytes`)

In Go non esiste alcuna conversione implicita tra `[8]byte` e `[]byte`.

Se provi a scrivere:
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

---

## 4. Vantaggio prestazionale: Zero-Allocation Pattern

Quando si scrivono sistemi ad alto throughput (come Commit Log, WAL o parser di rete), evitare allocazioni sull'heap riduce drasticamente il carico sul **Garbage Collector (GC)**.

### Approccio con `make` (possibile allocazione Heap)
```go
lengthBytes := make([]byte, 8)
buf.Write(lengthBytes)
```
Poiché `buf.Write` accetta una slice e può potenzialmente conservarne il riferimento (*escape analysis*), il compilatore Go spesso sposta `lengthBytes` sull'**heap**.

### Approccio con Array fisso + `[:]` (Stack Allocation)
```go
var lengthBytes [8]byte
encoding.PutUint64(lengthBytes[:], uint64(len(record)))
buf.Write(lengthBytes[:])
```
In questo caso:
1. `var lengthBytes [8]byte` riserva 8 byte direttamente nello **stack frame** della funzione.
2. `lengthBytes[:]` crea una vista `[]byte` su quei 8 byte a costo zero.
3. Quando la funzione termina, lo stack frame viene semplicemente rilasciato senza alcun intervento del Garbage Collector (**0 allocazioni heap**).


----

## Forzare compilatore a controllare se una struttura implementa completamente una interfaccia

var _ api.LogServer = (*grpcServer)(nil)