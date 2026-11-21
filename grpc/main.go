/*
Esempi di lancio (dalla root del progetto; se eseguito dentro grpc/, usa `go run .` invece di `go run ./grpc`,
non usare `go run main.go` perché `server.go` fa parte dello stesso package `main`).

Prerequisito (una sola volta, genera i certificati in security/certs/):
   make gencert

Nota sicurezza: sia il server sia il client usano di default mTLS (Mutual TLS a 2 vie:
cifratura + il client verifica il server tramite ca-public-cert.pem + il server verifica
il passaporto del client scelto con -user: "root" o "reader", default "root").

 1. Avvio Server (mTLS attivo):
    go run ./grpc -server
    go run ./grpc -server -addr 127.0.0.1:8080

2) Avvio Client (in un altro terminale; flag -addr opzionale, default 127.0.0.1:8080; -user root | reader):

  - Produce singolo (Unary, default -user root):
    go run ./grpc -op produce -value "hello world"
    go run ./grpc -user root -op produce -value "ciao"

  - Consume singolo (Unary, funziona sia con root sia con reader):
    go run ./grpc -op consume -offset 0
    go run ./grpc -user reader -op consume -offset 0

  - ProduceStream (Bidirectional Streaming interattivo da tastiera):
    go run ./grpc -op produce-stream

  - ConsumeStream (Server-Side Streaming in tempo reale dall'offset indicato):
    go run ./grpc -op consume-stream -offset 0
    go run ./grpc -user reader -op consume-stream -offset 0

  - Test ACL PermissionDenied (reader può leggere ma NON può scrivere):
    go run ./grpc -user reader -op produce -value "non autorizzato"

  - Test rifiuto TLS a 1 via (client senza certificato proprio: il server mTLS deve rifiutare):
    go run ./grpc -no-client-cert -op produce -value "senza passaporto"

  - Test rifiuto connessione in chiaro (il server mTLS deve rifiutare questa chiamata):
    go run ./grpc -insecure -op produce -value "test in chiaro"
*/
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	api "distribuiti/api/v1"
	commitlog "distribuiti/log"
	"distribuiti/security"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func main() {
	address := flag.String("addr", "127.0.0.1:8080", "gRPC server address")
	serverMode := flag.Bool("server", false, "start the gRPC server")
	op := flag.String("op", "produce", "operation: produce | consume | produce-stream | consume-stream")
	value := flag.String("value", "hello world", "record value for produce")
	offset := flag.Uint64("offset", 0, "record offset for consume / consume-stream")
	insecureMode := flag.Bool("insecure", false, "connect without TLS")
	noClientCert := flag.Bool("no-client-cert", false, "connect with 1-way TLS without client certificate (for testing mTLS rejection)")
	user := flag.String("user", "root", "client identity certificate: root | reader")
	flag.Parse()

	if *serverMode {
		runServer(*address)
		return
	}

	var clientCreds credentials.TransportCredentials
	switch {
	case *insecureMode:
		// Solo per fare la prova del 9: il server mTLS deve rifiutare questa connessione!
		clientCreds = insecure.NewCredentials()
	case *noClientCert:
		// TLS a 1 via (senza passaporto del client): il server mTLS deve rifiutarlo perché richiede il certificato client!
		clientTLSConfig, err := security.BuildTLSConfig(security.TLSOptions{
			CAPublicCertPath: security.CAPublicCertPath,
		})
		if err != nil {
			log.Fatalf("impossibile configurare TLS 1-way lato client: %v", err)
		}
		clientCreds = credentials.NewTLS(clientTLSConfig)
	default:
		var clientCertPath, clientKeyPath string
		switch *user {
		case "root":
			clientCertPath = security.RootClientPublicCertPath
			clientKeyPath = security.RootClientPrivateKeyPath
		case "reader":
			clientCertPath = security.ReaderClientPublicCertPath
			clientKeyPath = security.ReaderClientPrivateKeyPath
		default:
			log.Fatalf("utente non supportato: %q (usa 'root' o 'reader')", *user)
		}

		// mTLS (2-way): il client verifica il server con CAPublicCertPath E presenta il proprio passaporto + chiave privata.
		clientTLSConfig, err := security.BuildTLSConfig(security.TLSOptions{
			PublicCertPath:   clientCertPath,
			PrivateKeyPath:   clientKeyPath,
			CAPublicCertPath: security.CAPublicCertPath,
		})
		if err != nil {
			log.Fatalf("impossibile configurare mTLS lato client: %v", err)
		}
		clientCreds = credentials.NewTLS(clientTLSConfig)
	}

	conn, err := grpc.NewClient(*address, grpc.WithTransportCredentials(clientCreds))
	if err != nil {
		log.Fatalf("impossibile creare il client gRPC: %v", err)
	}

	defer conn.Close()

	client := api.NewLogClient(conn)

	// Contesto cancellabile con Ctrl+C (fondamentale per uscire pulitamente dagli stream)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch *op {
	case "produce":
		runProduce(ctx, client, *value)

	case "consume":
		runConsume(ctx, client, *offset)

	case "produce-stream":
		runProduceStream(ctx, client)

	case "consume-stream":
		runConsumeStream(ctx, client, *offset)

	default:
		log.Fatal("operazione non supportata: " + strconv.Quote(*op))
	}
}

// 1. Produce singolo (Unary)
func runProduce(parentCtx context.Context, client api.LogClient, val string) {
	ctx, cancel := context.WithTimeout(parentCtx, 5*time.Second)
	defer cancel()

	res, err := client.Produce(ctx, &api.ProduceRequest{
		Record: &api.Record{Value: []byte(val)},
	})
	if err != nil {
		log.Fatalf("errore Produce: %v", err)
	}
	fmt.Printf("Scritto con successo -> offset=%d\n", res.Offset)
}

// 2. Consume singolo (Unary)
func runConsume(parentCtx context.Context, client api.LogClient, offset uint64) {
	ctx, cancel := context.WithTimeout(parentCtx, 5*time.Second)
	defer cancel()

	res, err := client.Consume(ctx, &api.ConsumeRequest{Offset: offset})
	if err != nil {
		log.Fatalf("errore Consume: %v", err)
	}
	fmt.Printf("Letto -> offset=%d value=%q\n", res.Record.Offset, string(res.Record.Value))
}

// 3. ProduceStream (Bidirectional Streaming interattivo da tastiera)
func runProduceStream(ctx context.Context, client api.LogClient) {
	stream, err := client.ProduceStream(ctx)
	if err != nil {
		log.Fatalf("errore apertura ProduceStream: %v", err)
	}

	// Goroutine separata per ricevere gli offset man mano che il server conferma
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			res, err := stream.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				if status.Code(err) != codes.Canceled {
					log.Printf("errore ricezione stream: %v", err)
				}
				return
			}
			fmt.Printf("  [Server ACK] salvato all'offset=%d\n", res.Offset)
		}
	}()

	fmt.Println("Modalità ProduceStream attiva. Scrivi un messaggio e premi Invio (Ctrl+D o 'exit' per chiudere):")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		text := scanner.Text()
		if text == "exit" {
			break
		}
		err := stream.Send(&api.ProduceRequest{
			Record: &api.Record{Value: []byte(text)},
		})
		if err != nil {
			log.Fatalf("errore invio su stream: %v", err)
		}
	}

	// Segnala al server che abbiamo finito di inviare (lato server stream.Recv() riceverà io.EOF)
	if err := stream.CloseSend(); err != nil {
		log.Printf("errore CloseSend: %v", err)
	}
	<-done
}

// 4. ConsumeStream (Server-Side Streaming in tempo reale)
func runConsumeStream(ctx context.Context, client api.LogClient, startOffset uint64) {
	stream, err := client.ConsumeStream(ctx, &api.ConsumeRequest{Offset: startOffset})
	if err != nil {
		log.Fatalf("errore apertura ConsumeStream: %v", err)
	}

	fmt.Printf("In ascolto dal log a partire dall'offset %d (premi Ctrl+C per uscire)...\n", startOffset)
	for {
		res, err := stream.Recv()
		if err == io.EOF {
			fmt.Println("Stream terminato dal server.")
			return
		}
		if err != nil {
			// Se l'utente ha premuto Ctrl+C, usciamo in modo pulito senza errori
			if status.Code(err) == codes.Canceled {
				fmt.Println("\nChiusura consumer.")
				return
			}
			log.Fatalf("errore lettura ConsumeStream: %v", err)
		}
		fmt.Printf("[Record] offset=%d | value=%q\n", res.Record.Offset, string(res.Record.Value))
	}
}

func runServer(address string) {
	dataDir := "data"
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("impossibile creare la directory dati: %v", err)
	}

	commitLog, err := commitlog.NewLog(dataDir, commitlog.Config{})
	if err != nil {
		log.Fatalf("impossibile aprire il commit log: %v", err)
	}
	defer commitLog.Close()

	// Il server carica il proprio passaporto + chiave privata E il timbro della CA con IsServer: true:
	// in questo modo attiva tls.RequireAndVerifyClientCert (mTLS a 2 vie) e verifica anche i client.
	serverTLSConfig, err := security.BuildTLSConfig(security.TLSOptions{
		PublicCertPath:   security.ServerPublicCertPath,
		PrivateKeyPath:   security.ServerPrivateKeyPath,
		CAPublicCertPath: security.CAPublicCertPath,
		IsServer:         true,
	})
	if err != nil {
		log.Fatalf("impossibile configurare mTLS lato server: %v", err)
	}
	serverCreds := credentials.NewTLS(serverTLSConfig)

	authorizer, err := security.NewAuthorizer(security.ACLModelPath, security.ACLPolicyPath)
	if err != nil {
		log.Fatalf("impossibile inizializzare l'authorizer ACL: %v", err)
	}

	grpcServer, err := NewGRPCServer(
		&Config{
			CommitLog:  commitLog,
			Authorizer: authorizer,
		},
		grpc.Creds(serverCreds),
	)
	if err != nil {
		log.Fatalf("impossibile creare il server gRPC: %v", err)
	}

	listener, err := net.Listen("tcp", address)
	if err != nil {
		log.Fatalf("impossibile ascoltare su %s: %v", address, err)
	}
	defer listener.Close()

	log.Printf("server gRPC (mTLS + ACL) in ascolto su %s", address)
	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("errore server gRPC: %v", err)
	}
}
