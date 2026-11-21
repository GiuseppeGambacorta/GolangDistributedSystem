package security

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// TLSOptions raccoglie i file e le impostazioni per costruire un *tls.Config
// sia lato server sia lato client (TLS a 1 via oppure mTLS).
type TLSOptions struct {
	PublicCertPath     string // Passaporto pubblico da mostrare all'altro lato (es. server-public-cert.pem)
	PrivateKeyPath     string // Chiave privata segreta corrispondente al passaporto pubblico
	CAPublicCertPath   string // Certificato pubblico della CA di cui fidarsi per verificare l'altro lato
	ExpectedServerName string // Nome/IP che il client si aspetta sul certificato del server (es. "127.0.0.1")
	IsServer           bool   // true = lato server (verifica i client, mTLS); false = lato client (verifica il server)
}

func BuildTLSConfig(options TLSOptions) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: options.ExpectedServerName,
	}

	// 1. Carica la propria identità (certificato pubblico + chiave privata) da mostrare all'altro lato.
	if options.PublicCertPath != "" && options.PrivateKeyPath != "" {
		ownKeyPair, err := tls.LoadX509KeyPair(options.PublicCertPath, options.PrivateKeyPath)
		if err != nil {
			return nil, err
		}
		tlsConfig.Certificates = []tls.Certificate{ownKeyPair}
	}

	// 2. Carica il certificato pubblico della CA di cui fidarsi per verificare il certificato dell'altro lato.
	if options.CAPublicCertPath != "" {
		caCertPEM, err := os.ReadFile(options.CAPublicCertPath)
		if err != nil {
			return nil, err
		}
		trustedCAPool := x509.NewCertPool()
		if ok := trustedCAPool.AppendCertsFromPEM(caCertPEM); !ok {
			return nil, fmt.Errorf("failed to parse CA public certificate: %q", options.CAPublicCertPath)
		}
		if options.IsServer {
			// Lato server: controlla che il certificato del client sia firmato dalla nostra CA (mTLS).
			tlsConfig.ClientCAs = trustedCAPool
			tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		} else {
			// Lato client: controlla che il certificato del server sia firmato dalla nostra CA.
			tlsConfig.RootCAs = trustedCAPool
		}
	}

	return tlsConfig, nil
}
