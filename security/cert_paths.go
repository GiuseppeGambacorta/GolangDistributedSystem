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

	// Identità del client (mTLS, CN="client"): passaporto pubblico + chiave privata segreta.
	ClientPublicCertPath = certPath("client-public-cert.pem")
	ClientPrivateKeyPath = certPath("client-private-key.pem")
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
