package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	api "distribuiti/api/v1"
	commitlog "distribuiti/log"
	"distribuiti/observability"
	"distribuiti/security"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

const (
	// Al momento c'è un solo commit log, quindi usiamo "*" (tutto il log).
	objectWildcard = "*"
	produceAction  = "produce"
	consumeAction  = "consume"
)

type CommitLog interface {
	Append(*api.Record) (uint64, error)
	Read(uint64) (*api.Record, error)
}

type Authorizer interface {
	Authorize(subject, object, action string) error
}

type Config struct {
	CommitLog  CommitLog
	Authorizer Authorizer
	Logger     *slog.Logger // opzionale: se valorizzato, scrive i log strutturati JSON di ogni RPC
}

func NewGRPCServer(config *Config, opts ...grpc.ServerOption) (*grpc.Server, error) {
	unaryInterceptors := []grpc.UnaryServerInterceptor{unaryAuthInterceptor}
	streamInterceptors := []grpc.StreamServerInterceptor{streamAuthInterceptor}
	if config != nil && config.Logger != nil {
		unaryInterceptors = append(unaryInterceptors, unaryLogInterceptor(config.Logger))
		streamInterceptors = append(streamInterceptors, streamLogInterceptor(config.Logger))
	}

	opts = append(opts,
		grpc.ChainUnaryInterceptor(unaryInterceptors...),
		grpc.ChainStreamInterceptor(streamInterceptors...),
	)
	grpcServer := grpc.NewServer(opts...)
	server, err := createGrpcServer(config)
	if err != nil {
		return nil, err
	}
	api.RegisterLogServer(grpcServer, server)
	return grpcServer, nil
}

// RunServer avvia il server gRPC completo (CommitLog su data/ + mTLS + Casbin ACL + Observability)
// e si arresta in modo pulito quando ctx viene cancellato (es. Ctrl+C).
func RunServer(ctx context.Context, address string) error {
	dataDir := "data"
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("impossibile creare la directory dati: %w", err)
	}

	commitLog, err := commitlog.NewLog(dataDir, commitlog.Config{})
	if err != nil {
		return fmt.Errorf("impossibile aprire il commit log: %w", err)
	}
	defer commitLog.Close()

	// Avvia i 3 pilastri dell'osservabilità (server.log, metrics.log, traces.log in observability/output/).
	telemetry, err := observability.Setup(observability.Options{
		ServiceName: "proglog-server",
	})
	if err != nil {
		return fmt.Errorf("impossibile inizializzare observability: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := telemetry.Shutdown(shutdownCtx); err != nil {
			log.Printf("errore chiusura observability: %v", err)
		}
	}()

	// Il server carica il proprio passaporto + chiave privata E il timbro della CA con IsServer: true:
	// attiva tls.RequireAndVerifyClientCert (mTLS a 2 vie) per verificare anche i client.
	serverTLSConfig, err := security.BuildTLSConfig(security.TLSOptions{
		PublicCertPath:   security.ServerPublicCertPath,
		PrivateKeyPath:   security.ServerPrivateKeyPath,
		CAPublicCertPath: security.CAPublicCertPath,
		IsServer:         true,
	})
	if err != nil {
		return fmt.Errorf("impossibile configurare mTLS lato server: %w", err)
	}
	serverCreds := credentials.NewTLS(serverTLSConfig)

	authorizer, err := security.NewAuthorizer(security.ACLModelPath, security.ACLPolicyPath)
	if err != nil {
		return fmt.Errorf("impossibile inizializzare l'authorizer ACL: %w", err)
	}

	grpcServer, err := NewGRPCServer(
		&Config{
			CommitLog:  commitLog,
			Authorizer: authorizer,
			Logger:     telemetry.Logger,
		},
		grpc.Creds(serverCreds),
		grpc.StatsHandler(telemetry.GRPCStatsHandler()),
	)
	if err != nil {
		return fmt.Errorf("impossibile creare il server gRPC: %w", err)
	}

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("impossibile ascoltare su %s: %w", address, err)
	}
	defer listener.Close()

	go func() {
		<-ctx.Done()
		log.Println("arresto server gRPC in corso...")
		grpcServer.GracefulStop()
	}()

	log.Printf("server gRPC (mTLS + ACL + Observability) in ascolto su %s (premi Ctrl+C per fermare)", address)
	log.Printf("observability -> logs: %s | metrics: %s | traces: %s",
		telemetry.LogsFilePath, telemetry.MetricsFilePath, telemetry.TracesFilePath)
	if err := grpcServer.Serve(listener); err != nil {
		return fmt.Errorf("errore server gRPC: %w", err)
	}
	return nil
}

var _ api.LogServer = (*grpcServer)(nil)

type grpcServer struct {
	api.UnimplementedLogServer
	*Config

	// Meccanismo di notifica broadcast per i consumer in attesa
	mu       sync.Mutex
	notifyCh chan struct{}
}

func createGrpcServer(config *Config) (*grpcServer, error) {
	if config == nil || config.CommitLog == nil || config.Authorizer == nil {
		return nil, status.Error(codes.InvalidArgument, "commit log and authorizer are required")
	}
	return &grpcServer{
		Config:   config,
		notifyCh: make(chan struct{}),
	}, nil
}

// waitChannel restituisce il canale attuale su cui mettersi in ascolto
func (server *grpcServer) waitChannel() <-chan struct{} {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.notifyCh
}

// notifyConsumers sveglia in broadcast tutti i consumer in attesa di nuovi record.
// In Go, chiudere un canale (close) sblocca istantaneamente TUTTE le goroutine
// ferme in lettura su quel canale (<-waitCh), a differenza dell'invio di un singolo
// valore che ne sveglierebbe solo una. Subito dopo creiamo un nuovo canale aperto
// pronto per il prossimo ciclo di attesa.
func (server *grpcServer) notifyConsumers() {
	server.mu.Lock()
	defer server.mu.Unlock()

	close(server.notifyCh)
	server.notifyCh = make(chan struct{})

}

func (server *grpcServer) Produce(ctx context.Context, req *api.ProduceRequest) (*api.ProduceResponse, error) {
	if err := server.Authorizer.Authorize(subject(ctx), objectWildcard, produceAction); err != nil {
		return nil, err
	}
	offset, err := server.CommitLog.Append(req.Record)
	if err != nil {
		return nil, err
	}

	server.notifyConsumers()
	return &api.ProduceResponse{Offset: offset}, nil
}

func (server *grpcServer) Consume(ctx context.Context, req *api.ConsumeRequest) (*api.ConsumeResponse, error) {
	if err := server.Authorizer.Authorize(subject(ctx), objectWildcard, consumeAction); err != nil {
		return nil, err
	}
	record, err := server.CommitLog.Read(req.Offset)
	if err != nil {
		return nil, err
	}
	return &api.ConsumeResponse{Record: record}, nil
}

func (server *grpcServer) ProduceStream(stream grpc.BidiStreamingServer[api.ProduceRequest, api.ProduceResponse]) error {
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		// Chiama server.Produce, quindi attiva automaticamente anche notifyConsumers()
		response, err := server.Produce(stream.Context(), req)
		if err != nil {
			return err
		}
		if err := stream.Send(response); err != nil {
			return err
		}
	}
}

func (server *grpcServer) ConsumeStream(req *api.ConsumeRequest, stream grpc.ServerStreamingServer[api.ConsumeResponse]) error {
	for {
		// Prendiamo il canale PRIMA di tentare la lettura: evita una race condition
		// se un Produce avviene esattamente tra Read() e l'ingresso nel select.
		waitCh := server.waitChannel()

		response, err := server.Consume(stream.Context(), req)

		if err != nil {
			if _, ok := errors.AsType[api.ErrOffsetOutOfRange](err); ok {
				// Siamo in pari col log: finché waitCh è aperto e vuoto, la lettura
				// <-waitCh blocca la goroutine a 0% CPU. Appena un Produce chiama
				// close(notifyCh), <-waitCh si sblocca immediatamente e riprende il ciclo.
				select {
				case <-waitCh:
					continue
				case <-stream.Context().Done():
					return nil
				}
			}
			return err
		}

		if err := stream.Send(response); err != nil {
			return err
		}
		req.Offset++
	}
}

/*
================================================================================
GIRO ESATTO DI UNA RICHIESTA (dal client fino a Produce / Consume):

  Chiamata singola (Produce / Consume):
    Client ──► gRPC chiama unaryAuthInterceptor()
                 └──► chiama authenticate(ctx)        [legge "root"/"reader" dal cert mTLS e lo mette in newCtx]
                 └──► chiama handler(newCtx, req)     [cioè entra davvero in Produce(newCtx) o Consume(newCtx)]
                        └──► chiama subject(ctx)      [rilegge "root"/"reader" da ctx e lo passa a Casbin]

  Chiamata streaming (ProduceStream / ConsumeStream):
    Client ──► gRPC chiama streamAuthInterceptor()
                 └──► chiama authenticate(ss.Context()) [legge "root"/"reader" dal cert mTLS e lo mette in newCtx]
                 └──► avvolge ss in authenticatedServerStream{ctx: newCtx}
                 └──► chiama handler(srv, wrappedStream) [entra in ProduceStream / ConsumeStream]
                        └──► stream.Context() restituisce newCtx ──► passa a Produce/Consume ──► subject(ctx)
================================================================================
*/

// Chiave privata usata per salvare e rileggere il nome utente ("root"/"reader") dentro context.Context
// senza rischiare collisioni con altre librerie.
type subjectContextKey struct{}

// CHI LO USA: streamAuthInterceptor (solo per gli stream).
// COSA FA: avvolge lo stream originale di gRPC cambiando SOLO il metodo Context() così da restituire
// il nuovo context con dentro "root"/"reader" (gli altri metodi Send/Recv restano quelli dello stream originale).
// DOVE VA: viene passato dentro ProduceStream / ConsumeStream al posto dello stream grezzo.
type authenticatedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *authenticatedServerStream) Context() context.Context {
	return s.ctx
}

// CHI LO CHIAMA: gRPC in automatico (registrato in NewGRPCServer con grpc.ChainUnaryInterceptor)
// PRIMA di far entrare la richiesta in Produce() o Consume().
// COSA FA: chiama authenticate(ctx) per leggere "root"/"reader" dal certificato mTLS e infilarlo in newCtx.
// DOVE VA DOPO: chiama handler(newCtx, req), che esegue finalmente Produce() o Consume() con newCtx.
func unaryAuthInterceptor(
	ctx context.Context,
	req any,
	_ *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	newCtx, err := authenticate(ctx)
	if err != nil {
		return nil, err
	}
	return handler(newCtx, req)
}

// CHI LO CHIAMA: gRPC in automatico (registrato in NewGRPCServer con grpc.ChainStreamInterceptor)
// PRIMA di far entrare la richiesta in ProduceStream() o ConsumeStream().
// COSA FA: chiama authenticate(ss.Context()) e impacchetta newCtx dentro authenticatedServerStream.
// DOVE VA DOPO: chiama handler(srv, ...), che esegue ProduceStream() o ConsumeStream() con lo stream arricchito.
func streamAuthInterceptor(
	srv any,
	ss grpc.ServerStream,
	_ *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	newCtx, err := authenticate(ss.Context())
	if err != nil {
		return err
	}
	return handler(srv, &authenticatedServerStream{ServerStream: ss, ctx: newCtx})
}

// CHI LO CHIAMA: unaryAuthInterceptor e streamAuthInterceptor.
// COSA FA: apre le info TLS della connessione, legge il CommonName ("root" o "reader")
// dal certificato del client e lo salva dentro un nuovo context usando subjectContextKey{}.
// DOVE VA DOPO: ritorna newCtx all'interceptor, che lo passa a Produce/Consume (o agli stream).
func authenticate(ctx context.Context) (context.Context, error) {
	peerInfo, ok := peer.FromContext(ctx)
	if !ok {
		return ctx, status.Error(codes.Unknown, "couldn't find peer info")
	}
	if peerInfo.AuthInfo == nil {
		return context.WithValue(ctx, subjectContextKey{}, ""), nil
	}
	tlsInfo, ok := peerInfo.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.VerifiedChains) == 0 || len(tlsInfo.State.VerifiedChains[0]) == 0 {
		return ctx, status.Error(codes.Unauthenticated, "client certificate was not verified")
	}
	subject := tlsInfo.State.VerifiedChains[0][0].Subject.CommonName
	return context.WithValue(ctx, subjectContextKey{}, subject), nil
}

// CHI LO CHIAMA: Produce() e Consume() (le prime righe di entrambi i metodi).
// COSA FA: tira fuori dal context la stringa ("root" o "reader") salvata prima da authenticate().
// DOVE VA DOPO: la stringa viene passata a server.Authorizer.Authorize(subject(ctx), "*", "produce"/"consume").
func subject(ctx context.Context) string {
	value, _ := ctx.Value(subjectContextKey{}).(string)
	return value
}

// unaryLogInterceptor misura la durata di ogni RPC Unary e scrive una riga JSON in server.log.
// Gira DOPO unaryAuthInterceptor, così subject(ctx) contiene già "root" o "reader".
func unaryLogInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		logRPCCall(ctx, logger, "finished unary call", info.FullMethod, time.Since(start), err)
		return resp, err
	}
}

// streamLogInterceptor misura la durata di ogni RPC Streaming e scrive una riga JSON in server.log.
func streamLogInterceptor(logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(
		srv any,
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		start := time.Now()
		err := handler(srv, ss)
		logRPCCall(ss.Context(), logger, "finished streaming call", info.FullMethod, time.Since(start), err)
		return err
	}
}

func logRPCCall(ctx context.Context, logger *slog.Logger, msg, method string, duration time.Duration, err error) {
	level := slog.LevelInfo
	if err != nil {
		level = slog.LevelWarn
	}
	logger.Log(ctx, level, msg,
		"grpc.method", method,
		"grpc.code", status.Code(err).String(),
		"grpc.time_ns", duration.Nanoseconds(),
		"subject", subject(ctx),
		"peer.address", peerAddress(ctx),
	)
}

func peerAddress(ctx context.Context) string {
	if peerInfo, ok := peer.FromContext(ctx); ok && peerInfo.Addr != nil {
		return peerInfo.Addr.String()
	}
	return ""
}
