================================================================================
00 — TLS 1-WAY: CLIENT VERIFICA IL SERVER (solo il Server ha il passaporto)
================================================================================

### FASE 1 — CREAZIONE (`make gencert`, una volta sola)

```text
  [1] NASCE IL COMUNE (CA)
  ────────────────────────
  01-ca-identita.json
  (Ravenna, GolangDistributedSystem CA)
            │
            ▼  cfssl -initca
    ┌───────┴───────────────────────────┐
    ▼                                   ▼
  ca-private-key.pem                  ca-public-cert.pem
  (timbro SEGRETO del Comune)         (fac-simile PUBBLICO del timbro)
    │                                   │
    │                                   ├──────────────────────────────► [al Client]
    │                                   │
    ▼                                   ▼
  [2] IL COMUNE TIMBRA IL PASSAPORTO DEL SERVER
  ─────────────────────────────────────────────
  02-ca-regole-firma.json (profilo "server", 1 anno) ──┐
  03-server-richiesta.json (localhost, 127.0.0.1)    ──┼──► cfssl gencert
  ca-private-key.pem + ca-public-cert.pem            ──┘         │
                                                 ┌───────────────┴───────────────┐
                                                 ▼                               ▼
                                       server-private-key.pem          server-public-cert.pem
                                       (chiave SEGRETA del Server)     (passaporto PUBBLICO)
                                                 │                               │
                                                 └───────────────┬───────────────┘
                                                                 ▼
                                                           [al Server]

  (Tutti e 4 finiscono in security/certs/; i .csr temporanei vengono cancellati)
```

---

### FASE 2 — UTILIZZO A RUNTIME (`go run ./grpc`)

```text
  CLIENT (-op produce/consume...)                 SERVER (-server)
  ───────────────────────────────                 ────────────────
  Carica via BuildTLSConfig:                      Carica via BuildTLSConfig:
   • ca-public-cert.pem (RootCAs)                  • server-public-cert.pem (Certificates)
                                                   • server-private-key.pem (Certificates)

         │                                                 │
         │ 1. "Ciao 127.0.0.1:8080, parliamo in TLS?"      │
         ├────────────────────────────────────────────────►│
         │                                                 │
         │ 2. "Ecco il mio passaporto (server-public-cert) │
         │     + firma al volo fatta con server-private-key"
         │◄────────────────────────────────────────────────┤
         │                                                 │
         ▼                                                 │
  3. VERIFICA COL TIMBRO (ca-public-cert.pem):             │
     • Timbrato dalla CA di Ravenna?      [OK]             │
     • Valido per 127.0.0.1 / localhost?  [OK]             │
     • Firma con chiave privata valida?   [OK]             │
         │                                                 │
         │ 4. Si accordano su chiave di sessione veloce    │
         │◄═══════════════════════════════════════════════►│
         │          TUNNEL TLS CIFRATO ATTIVO              │
         │     Produce / Consume / Stream protetti         │
         │◄═══════════════════════════════════════════════►│
```

---

### CASO `-insecure` (connessione in chiaro rifiutata)

```text
  CLIENT (-insecure)                              SERVER (TLS)
         │  "Ciao, mando dati in chiaro!"                  │
         ├────────────────────────────────────────────────►│
         │         X  "Qui si parla solo TLS! Rifiutato."  │
         │◄────────────────────────────────────────────────┤
```
