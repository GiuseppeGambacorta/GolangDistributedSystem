package security

import (
	"github.com/casbin/casbin/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Authorizer incapsula Casbin per applicare le regole ACL (Access Control List).
// Isolarlo in questa struct permette di cambiare libreria di autorizzazione in futuro
// senza toccare il resto del progetto.
type Authorizer struct {
	enforcer *casbin.Enforcer
}

// NewAuthorizer carica il modello (05-acl-modello.conf) e la tabella permessi (06-acl-permessi.csv).
func NewAuthorizer(modelPath, policyPath string) (*Authorizer, error) {
	enforcer, err := casbin.NewEnforcer(modelPath, policyPath)
	if err != nil {
		return nil, err
	}
	return &Authorizer{enforcer: enforcer}, nil
}

// Authorize controlla se "subject" (es. "root" o "reader", letto dal CN del certificato mTLS)
// ha il permesso di eseguire "action" ("produce" o "consume") su "object".
// Nota: al momento il progetto gestisce un solo log, quindi "object" è sempre "*"
// (nessun filtro su oggetti specifici); il parametro resta pronto per futuri topic/log multipli.
func (authorizer *Authorizer) Authorize(subject, object, action string) error {
	allowed, err := authorizer.enforcer.Enforce(subject, object, action)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	if !allowed {
		return status.Errorf(codes.PermissionDenied, "%s not permitted to %s on %s", subject, action, object)
	}
	return nil
}
