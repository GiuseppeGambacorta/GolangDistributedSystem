package main

import (
	"context"
	"io"
	"sync"

	api "distribuiti/api/v1"

	"google.golang.org/grpc"
)

type CommitLog interface {
	Append(*api.Record) (uint64, error)
	Read(uint64) (*api.Record, error)
}

type Config struct {
	CommitLog CommitLog
}

func NewGRPCServer(config *Config) (*grpc.Server, error) {
	grpcServer := grpc.NewServer()
	server, err := newgrpcServer(config)
	if err != nil {
		return nil, err
	}
	api.RegisterLogServer(grpcServer, server)
	return grpcServer, nil
}

var _ api.LogServer = (*grpcServer)(nil)

type grpcServer struct {
	api.UnimplementedLogServer
	*Config

	// Meccanismo di notifica broadcast per i consumer in attesa
	mu       sync.Mutex
	notifyCh chan struct{}
}

func newgrpcServer(config *Config) (*grpcServer, error) {
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
	offset, err := server.CommitLog.Append(req)
	if err != nil {
		return nil, err
	}

	server.notifyConsumers()
	return &api.ProduceResponse{Offset: offset}, nil
}

func (server *grpcServer) Consume(ctx context.Context, req *api.ConsumeRequest) (*api.ConsumeResponse, error) {
	record, err := server.CommitLog.Read(req.Offset)
	if err != nil {
		return nil, err
	}
	return &api.ConsumeResponse{Record: record}, nil
}

func (server *grpcServer) ProduceStream(stream api.Log_ProduceStreamServer) error {
	for {
		req, err := stream.Recv()
		if err == io.EOF {
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

func (server *grpcServer) ConsumeStream(req *api.ConsumeRequest, stream api.Log_ConsumeStreamServer) error {
	for {
		// Prendiamo il canale PRIMA di tentare la lettura: evita una race condition
		// se un Produce avviene esattamente tra Read() e l'ingresso nel select.
		waitCh := server.waitChannel()

		response, err := server.Consume(stream.Context(), req)
		switch err.(type) {
		case nil:

		case api.ErrOffsetOutOfRange:
			// Siamo in pari col log: finché waitCh è aperto e vuoto, la lettura
			// <-waitCh blocca la goroutine a 0% CPU. Appena un Produce chiama
			// close(notifyCh), <-waitCh si sblocca immediatamente e riprende il ciclo.
			select {
			case <-waitCh:
				continue
			case <-stream.Context().Done():
				return nil
			}
		default:
			return err
		}

		if err := stream.Send(response); err != nil {
			return err
		}
		req.Offset++
	}
}
