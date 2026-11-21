================================================================================
02 — mTLS + INTERCEPTOR + CASBIN ACL: AUTENTICAZIONE E AUTORIZZAZIONE COMPLETE
================================================================================

### FLUSSO DI UNA RICHIESTA RPC (dalla dogana mTLS al buttafuori Casbin)

```text
  CLIENT (-user root oppure -user reader)
    │
    │ 1. Connessione mTLS (presenta il proprio passaporto firmato dalla CA)
    ▼
  ┌─────────────────────────────────────────────────────────────────────────┐
  │ [DOGANA: mTLS Handshake — security/tls.go]                              │
  │  • Verifica che il passaporto del client sia timbrato da ca-public-cert │
  │  • Se manca o è falso -> BLOCCATO prima ancora di arrivare al codice Go │
  └─────────────────────────────────────────────────────────────────────────┘
    │ (Passaporto valido!)
    ▼
  ┌─────────────────────────────────────────────────────────────────────────┐
  │ [TORNELLO: Interceptor gRPC — grpc/server.go]                           │
  │  • unaryAuthInterceptor / streamAuthInterceptor                         │
  │  • authenticate(ctx) legge peer.AuthInfo -> TLSInfo -> CommonName (CN)  │
  │  • Salva subject = "root" oppure "reader" dentro il context della RPC   │
  └─────────────────────────────────────────────────────────────────────────┘
    │ ctx contiene subject ("root" o "reader")
    ▼
  ┌─────────────────────────────────────────────────────────────────────────┐
  │ [BUTTAFUORI: Casbin ACL — security/authorizer.go]                       │
  │  Produce() -> Authorizer.Authorize(subject(ctx), "*", "produce")        │
  │  Consume() -> Authorizer.Authorize(subject(ctx), "*", "consume")        │
  │                                                                         │
  │  Confronta con:                                                         │
  │   • 05-acl-modello.conf  (m = sub, obj/wildcard, act)                   │
  │   • 06-acl-permessi.csv:                                                │
  │       p, root,   *, produce                                             │
  │       p, root,   *, consume                                             │
  │       p, reader, *, consume                                             │
  └──────────────────────────┬──────────────────────────┬───────────────────┘
                             │                          │
                   [ALLOWED = true]            [ALLOWED = false]
                             │                          │
                             ▼                          ▼
                   Esegue CommitLog           codes.PermissionDenied
                   Append() / Read()          ("reader not permitted to produce on *")
```

*(Nota: anche `ProduceStream` e `ConsumeStream` sono protetti automaticamente, perché chiamano internamente `server.Produce` e `server.Consume` usando il `stream.Context()` arricchito da `streamAuthInterceptor`.)*

---

### CONFRONTO PRATICO: `root` vs `reader`

```text
  ┌───────────────────┬──────────────────────────┬──────────────────────────┐
  │ Utente (-user)    │ -op produce / stream     │ -op consume / stream     │
  ├───────────────────┼──────────────────────────┼──────────────────────────┤
  │ root   (CN=root)  │ [OK] Scrive nel log      │ [OK] Legge dal log       │
  │ reader (CN=reader)│ [X]  PermissionDenied    │ [OK] Legge dal log       │
  └───────────────────┴──────────────────────────┴──────────────────────────┘
```
