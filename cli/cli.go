package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	grpcserver "distribuiti/grpc"

	"github.com/charmbracelet/huh"
)

const defaultAddress = "127.0.0.1:8080"

// Run avvia il menu interattivo della CLI (Server e Client gRPC con mTLS + ACL).
func Run() error {
	for {
		var mode string
		err := huh.NewSelect[string]().
			Title("GolangDistributedSystem — Menu Principale").
			Description("Usa le frecce su/giù e premi Invio").
			Options(
				huh.NewOption("Avvia Server gRPC (mTLS + ACL)", "server"),
				huh.NewOption("Avvia Client interattivo", "client"),
				huh.NewOption("Esci", "exit"),
			).
			Value(&mode).
			Run()
		if err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				fmt.Println("Uscita.")
				return nil
			}
			return err
		}

		switch mode {
		case "server":
			if err := runServerFlow(); err != nil {
				fmt.Printf("\n[ERRORE Server] %v\n\n", err)
			}
		case "client":
			if err := runClientFlow(); err != nil {
				fmt.Printf("\n[ERRORE Client] %v\n\n", err)
			}
		case "exit":
			fmt.Println("Alla prossima!")
			return nil
		}
	}
}

func runServerFlow() error {
	address := defaultAddress
	err := huh.NewInput().
		Title("Indirizzo su cui mettere in ascolto il server").
		Placeholder(defaultAddress).
		Value(&address).
		Run()
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return nil
		}
		return err
	}
	if strings.TrimSpace(address) == "" {
		address = defaultAddress
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return grpcserver.RunServer(ctx, address)
}

func runClientFlow() error {
	address := defaultAddress
	profile := string(grpcserver.ClientModeRoot)

	for {
		err := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Indirizzo del server gRPC").
					Placeholder(defaultAddress).
					Value(&address),
				huh.NewSelect[string]().
					Title("Scegli utente / modalità di sicurezza").
					Options(
						huh.NewOption("root (mTLS — legge e scrive)", string(grpcserver.ClientModeRoot)),
						huh.NewOption("reader (mTLS — sola lettura)", string(grpcserver.ClientModeReader)),
						huh.NewOption("no-client-cert (TLS 1-way — test rifiuto mTLS)", string(grpcserver.ClientModeNoClientCert)),
						huh.NewOption("insecure (senza TLS — test rifiuto in chiaro)", string(grpcserver.ClientModeInsecure)),
					).
					Value(&profile),
			),
		).Run()
		if err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return nil
			}
			return err
		}
		if strings.TrimSpace(address) == "" {
			address = defaultAddress
		}

		backToProfile, err := runClientOperationsLoop(address, grpcserver.ClientMode(profile))
		if err != nil {
			return err
		}
		if !backToProfile {
			return nil
		}
	}
}

// runClientOperationsLoop mantiene aperta la connessione finché l'utente non cambia profilo o torna al menu.
func runClientOperationsLoop(address string, mode grpcserver.ClientMode) (changeProfile bool, err error) {
	conn, client, err := grpcserver.NewClient(address, mode)
	if err != nil {
		return false, err
	}
	defer conn.Close()

	for {
		var op string
		err := huh.NewSelect[string]().
			Title(fmt.Sprintf("Client [%s @ %s] — Scegli operazione", mode, address)).
			Options(
				huh.NewOption("Produce (scrivi 1 messaggio)", "produce"),
				huh.NewOption("Consume (leggi 1 offset)", "consume"),
				huh.NewOption("ProduceStream (scrittura continua da tastiera)", "produce-stream"),
				huh.NewOption("ConsumeStream (ascolto in tempo reale)", "consume-stream"),
				huh.NewOption("Cambia utente / indirizzo", "change-profile"),
				huh.NewOption("Torna al menu principale", "back"),
			).
			Value(&op).
			Run()
		if err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return false, nil
			}
			return false, err
		}

		switch op {
		case "produce":
			var value string
			err := huh.NewInput().
				Title("Messaggio da scrivere nel log").
				Placeholder("ciao mondo").
				Value(&value).
				Run()
			if err != nil {
				if errors.Is(err, huh.ErrUserAborted) {
					continue
				}
				return false, err
			}
			offset, err := grpcserver.Produce(context.Background(), client, value)
			if err != nil {
				fmt.Printf("\n[ERRORE Produce] %v\n\n", err)
				continue
			}
			fmt.Printf("\n[OK] Scritto con successo -> offset=%d\n\n", offset)

		case "consume":
			offset, ok, err := askOffset("Offset da leggere")
			if err != nil {
				return false, err
			}
			if !ok {
				continue
			}
			record, err := grpcserver.Consume(context.Background(), client, offset)
			if err != nil {
				fmt.Printf("\n[ERRORE Consume] %v\n\n", err)
				continue
			}
			fmt.Printf("\n[OK] Letto -> offset=%d | value=%q\n\n", record.Offset, string(record.Value))

		case "produce-stream":
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			err := grpcserver.ProduceStream(ctx, client)
			stop()
			if err != nil {
				fmt.Printf("\n[ERRORE ProduceStream] %v\n\n", err)
			}
			fmt.Println()

		case "consume-stream":
			offset, ok, err := askOffset("Offset di partenza per lo stream")
			if err != nil {
				return false, err
			}
			if !ok {
				continue
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			err = grpcserver.ConsumeStream(ctx, client, offset)
			stop()
			if err != nil {
				fmt.Printf("\n[ERRORE ConsumeStream] %v\n\n", err)
			}
			fmt.Println()

		case "change-profile":
			return true, nil

		case "back":
			return false, nil
		}
	}
}

func askOffset(title string) (uint64, bool, error) {
	offsetText := "0"
	err := huh.NewInput().
		Title(title).
		Placeholder("0").
		Value(&offsetText).
		Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return nil
			}
			if _, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64); err != nil {
				return errors.New("inserisci un numero intero >= 0")
			}
			return nil
		}).
		Run()
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return 0, false, nil
		}
		return 0, false, err
	}
	if strings.TrimSpace(offsetText) == "" {
		return 0, true, nil
	}
	offset, err := strconv.ParseUint(strings.TrimSpace(offsetText), 10, 64)
	if err != nil {
		return 0, false, err
	}
	return offset, true, nil
}
