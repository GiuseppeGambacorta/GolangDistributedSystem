package security

import (
	"os"
	"path/filepath"
	"runtime"
)

var (
	// Certificato pubblico della CA (il "fac-simile del timbro" per verificare gli altri certificati).
	CAPublicCertPath = certPath("ca-public-cert.pem")

	// Identità del server: passaporto pubblico + chiave privata segreta.
	ServerPublicCertPath = certPath("server-public-cert.pem")
	ServerPrivateKeyPath = certPath("server-private-key.pem")

	// Identità client "root" (CN="root"): lettura e scrittura (produce + consume).
	RootClientPublicCertPath = certPath("root-client-public-cert.pem")
	RootClientPrivateKeyPath = certPath("root-client-private-key.pem")

	// Identità client "reader" (CN="reader"): sola lettura (consume).
	ReaderClientPublicCertPath = certPath("reader-client-public-cert.pem")
	ReaderClientPrivateKeyPath = certPath("reader-client-private-key.pem")

	// Configurazione ACL per Casbin (modello delle regole + tabella permessi).
	ACLModelPath  = configPath("05-acl-modello.conf")
	ACLPolicyPath = configPath("06-acl-permessi.csv")
)

func certPath(filename string) string {
	if customDir := os.Getenv("CERTS_DIR"); customDir != "" {
		return filepath.Join(customDir, filename)
	}
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		return filepath.Join("security", "certs", filename)
	}
	return filepath.Join(filepath.Dir(currentFile), "certs", filename)
}

func configPath(filename string) string {
	if customDir := os.Getenv("SECURITY_CONFIG_DIR"); customDir != "" {
		return filepath.Join(customDir, filename)
	}
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		return filepath.Join("security", "config", filename)
	}
	return filepath.Join(filepath.Dir(currentFile), "config", filename)
}
