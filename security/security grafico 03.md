================================================================================
03 — GIRO INTERNO IN grpc/server.go: INTERCEPTOR, CONTEXT E AUTHORIZER
================================================================================

### CASO 1 — CHIAMATA SINGOLA (Unary: `Produce` o `Consume`)

```text
  CLIENT (-user root / -user reader)
    │
    │ 1. Arriva la richiesta gRPC (con handshake mTLS già superato)
    ▼
  gRPC Runtime (registrato in NewGRPCServer con grpc.ChainUnaryInterceptor)
    │
    │ 2. Chiama in automatico unaryAuthInterceptor(ctx, req, info, handler)
    ▼
  unaryAuthInterceptor()
    │
    ├──► 3. Chiama authenticate(ctx):
    │         • peer.FromContext(ctx)                  -> scheda del chiamante
    │         • peerInfo.AuthInfo.(credentials.TLSInfo)-> certificato verificato mTLS
    │         • VerifiedChains[0][0].Subject.CommonName-> legge "root" oppure "reader"
    │         • context.WithValue(ctx, subjectContextKey{}, "root"/"reader")
    │         ◄── restituisce newCtx (con dentro l'identità!)
    │
    └──► 4. Chiama handler(newCtx, req)
              │   (handler È il tuo vero metodo Produce() o Consume()!)
              ▼
         Produce(newCtx, req)  /  Consume(newCtx, req)
              │
              ├──► 5. Prima riga: subject(ctx)
              │         • legge ctx.Value(subjectContextKey{}) -> "root" o "reader"
              │
              ├──► 6. server.Authorizer.Authorize(subject(ctx), "*", "produce"/"consume")
              │         • se FALSE -> return codes.PermissionDenied (SI FERMA QUI!)
              │         • se TRUE  -> prosegue!
              │
              └──► 7. server.CommitLog.Append(...) / Read(...)
```

---

### CASO 2 — CHIAMATA STREAMING (`ProduceStream` o `ConsumeStream`)

*(Perché qui serve `authenticatedServerStream`? Perché gli stream NON hanno un parametro `ctx` separato:
il context vive dentro `stream.Context()`, e lo stream di gRPC non permette di modificarlo direttamente!)*

```text
  CLIENT (-op produce-stream / -op consume-stream)
    │
    │ 1. Apre lo stream gRPC (handshake mTLS già superato)
    ▼
  gRPC Runtime (registrato in NewGRPCServer con grpc.ChainStreamInterceptor)
    │
    │ 2. Chiama in automatico streamAuthInterceptor(srv, ss, info, handler)
    ▼
  streamAuthInterceptor()
    │
    ├──► 3. Chiama authenticate(ss.Context())
    │         ◄── restituisce newCtx (con dentro "root" o "reader", fatto 1 SOLA volta!)
    │
    ├──► 4. Crea il wrapper:
    │         wrapped := &authenticatedServerStream{
    │             ServerStream: ss,     // eredita Send, Recv, ecc. dallo stream originale
    │             ctx:          newCtx, // sovrascrive SOLO il metodo Context()!
    │         }
    │
    └──► 5. Chiama handler(srv, wrapped)
              │   (entra in ProduceStream / ConsumeStream usando wrapped al posto di ss)
              ▼
         ProduceStream(stream)  /  ConsumeStream(req, stream)
              │
              └──► 6. Chiama server.Produce(stream.Context(), ...) / server.Consume(stream.Context(), ...)
                        • stream.Context() restituisce newCtx (grazie al wrapper!)
                        • Produce / Consume chiamano subject(ctx) -> "root" / "reader"
                        • Authorizer.Authorize(...) controlla i permessi su Casbin!
```

---

### RIEPILOGO RAPIDO DEI 5 PEZZI IN FONDO A `grpc/server.go`

| Simbolo | Chi lo chiama | Cosa fa | Dove va dopo |
| :--- | :--- | :--- | :--- |
| `subjectContextKey{}` | `authenticate` e `subject` | Chiave privata (0 byte) per salvare/leggere l'utente nel `context` senza collisioni. | — |
| `unaryAuthInterceptor` | gRPC (prima di `Produce`/`Consume`) | Chiama `authenticate(ctx)` e passa `newCtx` all'handler. | `Produce` / `Consume` |
| `streamAuthInterceptor` | gRPC (prima degli stream) | Chiama `authenticate(ss.Context())` e avvolge lo stream. | `ProduceStream` / `ConsumeStream` |
| `authenticatedServerStream` | `streamAuthInterceptor` | Wrapper su `grpc.ServerStream` che sovrascrive solo `Context()`. | Passato agli stream |
| `authenticate(ctx)` | I due interceptor | Estrae il `CN` (`"root"`/`"reader"`) dal certificato mTLS e lo mette in `newCtx`. | Ritorna `newCtx` |
| `subject(ctx)` | `Produce` e `Consume` | Rilegge `"root"`/`"reader"` dal `context`. | `Authorizer.Authorize(...)` |
