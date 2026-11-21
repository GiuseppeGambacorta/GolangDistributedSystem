# Modulo `security/` — CA Privata, Certificati e TLS

Documentazione del layer di sicurezza del progetto, costruito seguendo il percorso progressivo di *Distributed Services with Go*:

1. **TLS a 1 via — Autenticazione del Server & Cifratura** *(completato)*: il traffico è cifrato e il client verifica l'identità del server controllando che il certificato sia firmato dalla nostra CA.
2. **mTLS (Mutual TLS) — Autenticazione del Client** *(step attuale)*: anche il client presenta un certificato firmato dalla CA (`client-public-cert.pem`), permettendo al server di verificarne l'autenticità e leggerne l'identità (`CN = "client"`).
3. **ACL (Access Control List) con Casbin — Autorizzazione** *(prossimo step)*: il server verifica quali operazioni (`produce`, `consume`) può eseguire ogni identità autenticata (`CN`).

---

## Spiegazione semplice: l'analogia del Comune e dei Passaporti

Per capire tutti questi file senza confondersi, immagina che il tuo progetto sia una piccola città con un **Comune (l'Anagrafe)** e dei cittadini (**Server** e **Client**):

- **CA (*Certificate Authority*)** = il **Comune / Anagrafe** di cui tutti si fidano.
- **`ca-private-key.pem`** = il **timbro segreto** del Comune. Lo usa solo il Comune per timbrare i passaporti e **non si mostra mai a nessuno**.
- **`ca-public-cert.pem`** = il **fac-simile pubblico del timbro** del Comune. Lo dai a tutti i client così possono controllare se un passaporto è vero o falso.
- **`server-public-cert.pem`** = il **passaporto del server**, timbrato dal Comune, che dice *"io sono davvero `127.0.0.1` / `localhost`"*.
- **`server-private-key.pem`** = la **chiave segreta del server**. Dimostra al client che quel passaporto appartiene davvero a lui (e non è stato rubato) e serve a cifrare i messaggi.
- **`client-public-cert.pem`** = il **passaporto del client**, timbrato dal Comune, che dice *"io sono l'utente `client`"*.
- **`client-private-key.pem`** = la **chiave segreta del client** per dimostrare al server che quel passaporto è davvero il suo.
- **`.csr` (file temporanei eliminati da `make gencert`)** = i **moduli cartacei di richiesta** consegnati allo sportello. Una volta stampati i passaporti `.pem`, non servono più e vengono rimossi automaticamente.

Quando il client si collega al server in **mTLS (2-way)**, entrambi controllano i passaporti:
1. Il server mostra `server-public-cert.pem`; il client lo verifica confrontandolo con `ca-public-cert.pem` e controlla `127.0.0.1` / `localhost`.
2. Il server chiede il passaporto al client (`IsServer: true` → `RequireAndVerifyClientCert`); il client mostra `client-public-cert.pem` e il server lo verifica con `ca-public-cert.pem`.
3. Da quel momento entrambi sanno chi è l'altro e parlano in un tunnel cifrato.

---

## Struttura della cartella `security/`

```text
security/
├── config/                        # Istruzioni JSON numerate per generare i certificati con CFSSL
│   ├── 01-ca-identita.json        # Identità della nostra CA (GolangDistributedSystem, Ravenna)
│   ├── 02-ca-regole-firma.json    # Regole e profili di firma (server, client)
│   ├── 03-server-richiesta.json   # Richiesta passaporto del server (localhost, 127.0.0.1, Ravenna)
│   └── 04-client-richiesta.json   # Richiesta passaporto del client (CN="client", Ravenna)
├── certs/                         # I 6 file finali generati (.pem) — ignorata da Git!
│   ├── ca-public-cert.pem         # Fac-simile pubblico del timbro della CA
│   ├── ca-private-key.pem         # Timbro segreto della CA
│   ├── server-public-cert.pem     # Passaporto pubblico del server
│   ├── server-private-key.pem     # Chiave segreta del server
│   ├── client-public-cert.pem     # Passaporto pubblico del client (CN="client")
│   └── client-private-key.pem     # Chiave segreta del client
├── cert_paths.go                  # Percorsi Go (CA, Server e Client)
├── tls.go                         # Struct TLSOptions e factory BuildTLSConfig()
├── security.md                    # Questa guida
├── security grafico 00.md         # Schema visivo 00: TLS 1-way (client verifica server)
└── security grafico 01.md         # Schema visivo 01: mTLS 2-way (anche server verifica client)
```

> **Schemi visivi:** vedi [`security grafico 00.md`](security%20grafico%2000.md) (TLS 1-way) e [`security grafico 01.md`](security%20grafico%2001.md) (mTLS 2-way).

> **Nota su `.gitignore`:** la cartella `security/certs/` è esclusa dal versionamento Git perché contiene le chiavi private (`ca-private-key.pem`, `server-private-key.pem`).

---

## 1. Come funziona la CA privata con CFSSL

Per servizi interni non è necessario rivolgersi a CA esterne: generiamo una **Certificate Authority (CA) privata** con **CFSSL** (Cloudflare), composto da due tool CLI:
- **`cfssl`**: genera chiavi private, CSR (*Certificate Signing Request*) e certificati firmati in formato JSON.
- **`cfssljson`**: riceve il JSON in pipe e lo salva su disco nei file `.pem` e `.csr`.

### I 4 file di configurazione (`security/config/`)

1. **[`config/01-ca-identita.json`](config/01-ca-identita.json)** — *Identità della CA*:
   - Imposta il `CN` (*Common Name*: `"GolangDistributedSystem CA"`), algoritmo/dimensione chiave (`rsa` 2048 bit) e i tuoi dati locali (`C: IT`, `ST: Emilia-Romagna`, `L: Ravenna`, `O: GolangDistributedSystem`, `OU: Certificate Authority`).
   - Usato da `cfssl gencert -initca` per generare la CA radice.
2. **[`config/02-ca-regole-firma.json`](config/02-ca-regole-firma.json)** — *Regole di firma della CA*:
   - Definisce i profili ammessi in fase di firma: `server` (`server auth`) e `client` (`client auth`), entrambi con scadenza `8760h` (1 anno) e usi `signing`, `key encipherment`.
3. **[`config/03-server-richiesta.json`](config/03-server-richiesta.json)** — *Certificato del server*:
   - Mantiene `CN: "127.0.0.1"` e `"hosts": ["localhost", "127.0.0.1"]` (SAN — *Subject Alternative Names*: necessari perché TLS accetti le connessioni locali), con metadati `names` personalizzati (`Ravenna`, `GolangDistributedSystem`, `Log Server`).
4. **[`config/04-client-richiesta.json`](config/04-client-richiesta.json)** — *Certificato del client*:
   - Imposta `CN: "client"` (che sarà l'**identità/username** del client usata nel prossimo step per l'autorizzazione ACL), `"hosts": [""]`, e metadati `Ravenna`, `GolangDistributedSystem`, `Log Clients`.

### Flusso di generazione (`make gencert`)

```text
config/01-ca-identita.json ────────(cfssl gencert -initca)────► ca.pem + ca-key.pem + ca.csr
                                                                      │
config/02-ca-regole-firma.json (profile=server) ──────────────────────┼──(cfssl gencert)──► server.pem + server-key.pem + server.csr
config/03-server-richiesta.json (localhost, 127.0.0.1) ───────────────┘
                                                                      │
config/02-ca-regole-firma.json (profile=client) ──────────────────────┼──(cfssl gencert)──► client.pem + client-key.pem + client.csr
config/04-client-richiesta.json (CN="client") ────────────────────────┘
                                                                      │
                                                                      ▼  (rinomina chiara + pulizia .csr)
                                                        security/certs/
                                                          ├── ca-public-cert.pem
                                                          ├── ca-private-key.pem
                                                          ├── server-public-cert.pem
                                                          ├── server-private-key.pem
                                                          ├── client-public-cert.pem
                                                          └── client-private-key.pem
```

`make gencert` rinomina subito i 6 file finali in nomi auto-esplicativi dentro `security/certs/` e rimuove automaticamente i file temporanei `ca.csr`, `server.csr` e `client.csr`.

---

## 2. Codice Go: `cert_paths.go` e `tls.go`

### [`cert_paths.go`](cert_paths.go) — Risoluzione dei percorsi dei certificati

Espone le variabili di package `CAPublicCertPath`, `ServerPublicCertPath`, `ServerPrivateKeyPath`, `ClientPublicCertPath` e `ClientPrivateKeyPath` tramite `certPath(filename)`:
- Se è impostata la variabile d'ambiente `CERTS_DIR`, usa `filepath.Join(CERTS_DIR, filename)` (utile per test o deploy in container).
- Altrimenti risolve automaticamente la cartella `security/certs/` usando `runtime.Caller(0)`, così i percorsi funzionano sempre indipendentemente dalla directory da cui avvii `go run` o `go test` (con fallback a `security/certs/`).

### [`tls.go`](tls.go) — `TLSOptions` e `BuildTLSConfig()`

La funzione `BuildTLSConfig(options TLSOptions) (*tls.Config, error)` costruisce un `*tls.Config` con `MinVersion: tls.VersionTLS12` e `ServerName: options.ExpectedServerName`, coprendo tutti i ruoli possibili in base ai campi valorizzati:

| Scenario | Campi valorizzati in `TLSOptions` | Configurazione risultante su `*tls.Config` |
| :--- | :--- | :--- |
| **Server TLS (1-way)** | `PublicCertPath`, `PrivateKeyPath` *(senza `CAPublicCertPath`)* | Carica certificato e chiave privata in `Certificates` per cifrare il canale ed essere verificato dal client. |
| **Client TLS (1-way)** | `CAPublicCertPath`, `ExpectedServerName`, `IsServer: false` | Carica `ca-public-cert.pem` in `RootCAs` e imposta `ServerName` per verificare l'identità del server. |
| **Client mTLS** *(attivo)* | `CAPublicCertPath`, `PublicCertPath`, `PrivateKeyPath`, `ExpectedServerName`, `IsServer: false` | Verifica il server (`RootCAs`) **e** presenta il proprio certificato client (`Certificates`). |
| **Server mTLS** *(attivo)* | `CAPublicCertPath`, `PublicCertPath`, `PrivateKeyPath`, `IsServer: true` | Presenta il proprio certificato (`Certificates`), carica `ca-public-cert.pem` in `ClientCAs` e imposta `ClientAuth = tls.RequireAndVerifyClientCert`. |

*(Nota moderna rispetto al libro: viene usato `os.ReadFile` invece del deprecato `io/ioutil.ReadFile` ed è impostato esplicitamente `MinVersion: tls.VersionTLS12`.)*

---

## 3. Comandi Rapidi

### Installazione di `cfssl` e `cfssljson` (se non già presenti)

Su Go moderno (`go get` non installa più binari dal 1.18+) o su macOS:

```bash
# Tramite Homebrew (consigliato su macOS)
brew install cfssl

# Oppure tramite Go install
go install github.com/cloudflare/cfssl/cmd/cfssl@latest
go install github.com/cloudflare/cfssl/cmd/cfssljson@latest
```

### Generazione e verifica dei certificati

```bash
# Crea security/certs/ e genera i 4 file .pem finali (pulendo i .csr temporanei)
make gencert

# Oppure solo creazione della cartella
make init

# Verifica che server-public-cert.pem e client-public-cert.pem siano firmati da ca-public-cert.pem
openssl verify -CAfile security/certs/ca-public-cert.pem security/certs/server-public-cert.pem
openssl verify -CAfile security/certs/ca-public-cert.pem security/certs/client-public-cert.pem

# Ispeziona dettagli e SAN (Subject Alternative Names) del certificato server o client
openssl x509 -in security/certs/server-public-cert.pem -text -noout
openssl x509 -in security/certs/client-public-cert.pem -noout -subject -issuer
```
