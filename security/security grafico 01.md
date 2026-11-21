================================================================================
01 — mTLS 2-WAY: ANCHE IL SERVER AUTENTICA IL CLIENT (entrambi hanno il passaporto)
================================================================================

### FASE 1 — CREAZIONE (`make gencert`, una volta sola)

```text
  [1] NASCE IL COMUNE (CA)
  ────────────────────────
  01-ca-identita.json
  (Ravenna, GolangDistributedSystem CA)
            │
            ▼  cfssl -initca
    ┌───────┴───────────────────────────────┐
    ▼                                       ▼
  ca-private-key.pem                      ca-public-cert.pem
  (timbro SEGRETO del Comune)             (fac-simile PUBBLICO del timbro)
    │                                       │
    │                                       ├──────────────────────► [al Client] (verifica Server)
    │                                       ├──────────────────────► [al Server] (verifica Client!)
    │                                       │
    ├───────────────────────────────────────┼──────────┐
    ▼                                       ▼          │
  [2] PASSAPORTO DEL SERVER                            │
  ─────────────────────────                            │
  02-ca-regole-firma.json (profilo "server")           │
  03-server-richiesta.json (localhost, 127.0.0.1)      │
            │  cfssl gencert                           │
            ▼                                          │
  server-public-cert.pem + server-private-key.pem ─────┼──► [al Server]
                                                       │
                                                       ▼
  [3] PASSAPORTO DEL CLIENT (NOVITÀ DI QUESTO STEP!)
  ──────────────────────────────────────────────────
  02-ca-regole-firma.json (profilo "client")
  04-client-richiesta.json (CN = "client", Ravenna)  ◄── CN = username del Client!
            │  cfssl gencert
            ▼
  client-public-cert.pem + client-private-key.pem ───────► [al Client]

  (Tutti e 6 i file .pem finiscono in security/certs/; i .csr vengono cancellati)
```

---

### FASE 2 — UTILIZZO A RUNTIME (`go run ./grpc`, mTLS a 2 vie)

```text
  CLIENT (-op produce/consume...)                 SERVER (-server, IsServer: true)
  ───────────────────────────────                 ────────────────────────────────
  Carica via BuildTLSConfig:                      Carica via BuildTLSConfig:
   • ca-public-cert.pem     (RootCAs)              • ca-public-cert.pem     (ClientCAs + RequireAndVerify!)
   • client-public-cert.pem (Certificates)         • server-public-cert.pem (Certificates)
   • client-private-key.pem (Certificates)         • server-private-key.pem (Certificates)

         │                                                 │
         │ 1. "Ciao 127.0.0.1:8080, parliamo in TLS?"      │
         ├────────────────────────────────────────────────►│
         │                                                 │
         │ 2. "Ecco il mio passaporto (server-public-cert) │
         │     + firma fatta con server-private-key.       │
         │     TU CHI SEI? Mostrami il tuo passaporto!"    │  ◄── NOVITÀ (RequireAndVerifyClientCert)
         │◄────────────────────────────────────────────────┤
         │                                                 │
         ▼                                                 │
  3. CLIENT VERIFICA IL SERVER (RootCAs):                  │
     • Timbrato dalla CA di Ravenna?      [OK]             │
     • Valido per 127.0.0.1 / localhost?  [OK]             │
     • Firma con chiave privata valida?   [OK]             │
         │                                                 │
         │ 4. "Ecco il mio passaporto (client-public-cert) │
         │     con CN='client' + firma client-private-key" │
         ├────────────────────────────────────────────────►│
         │                                                 ▼
         │                                          5. SERVER VERIFICA IL CLIENT (ClientCAs):
         │                                             • Timbrato dalla CA di Ravenna?      [OK]
         │                                             • Uso consentito = "client auth"?    [OK]
         │                                             • Firma con client-private-key OK?   [OK]
         │                                             • Identità estratta: CN = "client"   [OK]
         │                                                 │
         │ 6. Chiave di sessione condivisa                 │
         │◄═══════════════════════════════════════════════►│
         │       TUNNEL mTLS RECIPROCO ATTIVO!             │
         │   Entrambi sanno con certezza chi è l'altro     │
         │◄═══════════════════════════════════════════════►│
```

---

### CASI RIFIUTATI DAL SERVER mTLS

```text
  [Caso A] CLIENT CON -no-client-cert (TLS 1-way, senza passaporto client)
  ────────────────────────────────────────────────────────────────────────
  CLIENT                                          SERVER (mTLS)
     │  Verifica server OK, ma NON invia passaporto   │
     ├───────────────────────────────────────────────►│
     │        X  "Manca il tuo certificato client!"   │
     │◄───────────────────────────────────────────────┤

  [Caso B] CLIENT CON -insecure (connessione in chiaro, senza TLS)
  ────────────────────────────────────────────────────────────────
  CLIENT                                          SERVER (mTLS)
     │  "Ciao, mando dati in chiaro!"                 │
     ├───────────────────────────────────────────────►│
     │        X  "Qui si parla solo mTLS! Rifiutato." │
     │◄───────────────────────────────────────────────┤
```
