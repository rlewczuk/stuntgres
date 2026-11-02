package core

import (
	"crypto/fips140"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
)

// InitializePgcryptoLocked is the bounded stock-provider initialization seam.
// TransactionOwner.Mu and Engine.Mu are held, in that order. The settings owner
// converts original placeholder/journal assignments, not extension SQL objects.
func (e *Engine) InitializePgcryptoLocked(op *session.Operation, scriptContext string) error {
	if op == nil || op.Session == nil || op.Session.Settings == nil { return fmt.Errorf("stuntgres: missing pgcrypto physical session") }
	if op.Session.Settings.PgcryptoInitialized() { return nil }
	auth, err := e.AuthorizationLocked(op)
	if err != nil { return err }
	notices, err := op.Session.Settings.InitializePgcrypto(op, DomainOf(e), e.Settings, func(setter authorization.PrincipalRef) (bool, error) {
		if setter == (authorization.PrincipalRef{}) { return true, nil }
		if _, exists := auth.Roles.Find(setter); !exists { return false, nil }
		original := auth
		original.State.CurrentUser = setter
		return parameterSetAuthority(original, settings.PgcryptoBuiltinCrypto)
	}, scriptContext)
	if err != nil { return err }
	for _, notice := range notices { op.EmitNotice(session.LevelWarning, notice) }
	return nil
}

// InitializePgcrypto authenticates the captured installed declaration before
// touching physical module state. Names, current e-membership and analysis-only
// dependencies cannot load the provider on another session.
func (h *statementHost) InitializePgcrypto(procedure *catalog.Procedure, ref ids.ObjectRef) error {
	if h == nil || h.Op == nil || procedure == nil || procedure.Implementation != extension.StockPgcrypto {
		return fmt.Errorf("stuntgres: invalid pgcrypto initialization authority")
	}
	if h.Op.Session.Settings.PgcryptoInitialized() { return nil }
	scope := h.scope()
	scope.Owner.Mu.Lock()
	defer scope.Owner.Mu.Unlock()
	e := scope.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := scope.RoutineAuthorizationLocked()
	if err != nil { return err }
	if auth.Ref(procedure.Address()) != ref { return ids.ErrCatalogReplaced }
	// Stored recipes retain their original declaration across compatible COW
	// owner/ACL/remap revisions; provider validation needs the canonical current
	// descriptor, after the original reference and ordinary admission checks.
	current, err := exec.ValidateRoutineDescriptor(auth.Catalog, auth.Roles, procedure)
	if err != nil { return err }
	if err := extension.StockPgcrypto.ValidateProcedure(auth.Catalog, current); err != nil { return err }
	return e.InitializePgcryptoLocked(h.Op, "")
}

func (h *statementHost) CheckPgcryptoBuiltinCrypto() error {
	if h == nil || h.Op == nil { return fmt.Errorf("stuntgres: missing pgcrypto statement authority") }
	if err := h.Context().Err(); err != nil { return err }
	return h.Op.EffectiveSettings().CheckPgcryptoBuiltinCrypto(fips140.Enabled())
}
