package grpcserver

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	api "distribuiti/api/v1"
	"distribuiti/security"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// ClientMode definisce il profilo di identità/sicurezza usato dal client.
type ClientMode string

const (
	ClientModeRoot         ClientMode = "root"
	ClientModeReader       ClientMode = "reader"
	ClientModeNoClientCert ClientMode = "no-client-cert"
	ClientModeInsecure     ClientMode = "insecure"
)

// NewClient apre una connessione gRPC configurata in base al profilo scelto (mTLS root/reader, TLS 1-way o insecure).
func NewClient(address string, mode ClientMode) (*grpc.ClientConn, api.LogClient, error) {
	var clientCreds credentials.TransportCredentials

	switch mode {
	case ClientModeInsecure:
		clientCreds = insecure.NewCredentials()
	case ClientModeNoClientCert:
		clientTLSConfig, err := security.BuildTLSConfig(security.TLSOptions{
			CAPublicCertPath: security.CAPublicCertPath,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("impossibile configurare TLS 1-way lato client: %w", err)
		}
		clientCreds = credentials.NewTLS(clientTLSConfig)
	case ClientModeRoot, ClientModeReader:
		certPath, keyPath := security.RootClientPublicCertPath, security.RootClientPrivateKeyPath
		if mode == ClientModeReader {
			certPath, keyPath = security.ReaderClientPublicCertPath, security.ReaderClientPrivateKeyPath
		}
		clientTLSConfig, err := security.BuildTLSConfig(security.TLSOptions{
			PublicCertPath:   certPath,
			PrivateKeyPath:   keyPath,
			CAPublicCertPath: security.CAPublicCertPath,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("impossibile configurare mTLS lato client (%s): %w", mode, err)
		}
		clientCreds = credentials.NewTLS(clientTLSConfig)
	default:
		return nil, nil, fmt.Errorf("profilo client non supportato: %q", mode)
	}

	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(clientCreds))
	if err != nil {
		return nil, nil, fmt.Errorf("impossibile creare il client gRPC: %w", err)
	}
	return conn, api.NewLogClient(conn), nil
}

// Produce invia un singolo record (Unary) e restituisce l'offset assegnato.
func Produce(parentCtx context.Context, client api.LogClient, val string) (uint64, error) {
	ctx, cancel := context.WithTimeout(parentCtx, 5*time.Second)
	defer cancel()

	res, err := client.Produce(ctx, &api.ProduceRequest{
		Record: &api.Record{Value: []byte(val)},
	})
	if err != nil {
		return 0, err
	}
	return res.Offset, nil
}

// Consume legge un singolo record dato l'offset (Unary).
func Consume(parentCtx context.Context, client api.LogClient, offset uint64) (*api.Record, error) {
	ctx, cancel := context.WithTimeout(parentCtx, 5*time.Second)
	defer cancel()

	res, err := client.Consume(ctx, &api.ConsumeRequest{Offset: offset})
	if err != nil {
		return nil, err
	}
	return res.Record, nil
}

// ProduceStream apre uno stream bidirezionale interattivo da tastiera ('exit' o Ctrl+D per terminare).
func ProduceStream(ctx context.Context, client api.LogClient) error {
	stream, err := client.ProduceStream(ctx)
	if err != nil {
		return err
	}

	recvErrCh := make(chan error, 1)
	go func() {
		defer close(recvErrCh)
		for {
			res, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				if status.Code(err) != codes.Canceled {
					recvErrCh <- err
				}
				return
			}
			fmt.Printf("  [Server ACK] salvato all'offset=%d\n", res.Offset)
		}
	}()

	fmt.Println("Modalità ProduceStream attiva. Scrivi un messaggio e premi Invio (scrivi 'exit' o premi Ctrl+D per tornare al menu):")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		text := scanner.Text()
		if text == "exit" {
			break
		}
		if err := stream.Send(&api.ProduceRequest{
			Record: &api.Record{Value: []byte(text)},
		}); err != nil {
			_ = stream.CloseSend()
			<-recvErrCh
			return err
		}
	}

	if err := stream.CloseSend(); err != nil {
		return err
	}
	if recvErr := <-recvErrCh; recvErr != nil {
		return recvErr
	}
	return nil
}

// ConsumeStream resta in ascolto in tempo reale a partire da startOffset (Ctrl+C per tornare al menu).
func ConsumeStream(ctx context.Context, client api.LogClient, startOffset uint64) error {
	stream, err := client.ConsumeStream(ctx, &api.ConsumeRequest{Offset: startOffset})
	if err != nil {
		return err
	}

	fmt.Printf("In ascolto dal log a partire dall'offset %d (premi Ctrl+C per tornare al menu)...\n", startOffset)
	for {
		res, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			fmt.Println("Stream terminato dal server.")
			return nil
		}
		if err != nil {
			if status.Code(err) == codes.Canceled {
				fmt.Println("\nChiusura consumer.")
				return nil
			}
			return err
		}
		fmt.Printf("[Record] offset=%d | value=%q\n", res.Record.Offset, string(res.Record.Value))
	}
}
