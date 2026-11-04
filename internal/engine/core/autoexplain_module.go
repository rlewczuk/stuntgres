package core

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// ActivateAutoExplainLocked runs only at authorized LOAD, never at bind/describe.
// The caller holds TransactionOwner.Mu and Engine.Mu; physical settings precede
// effective and Session. Frozen replay permissions use original setter identity.
func (e *Engine) ActivateAutoExplainLocked(op *session.Operation) ([]*sqlstate.Error, error) {
	physical, effective := op.Session.Settings, op.EffectiveSettings()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return nil, err
	}
	physical.Mu.Lock()
	if effective != physical {
		effective.Mu.Lock()
	}
	op.Session.Mu.Lock()
	defer func() {
		op.Session.Mu.Unlock()
		if effective != physical {
			effective.Mu.Unlock()
		}
		physical.Mu.Unlock()
	}()
	if err := effective.AdmitLocked(DomainOf(e), op); err != nil {
		return nil, err
	}
	return effective.ActivateAutoExplainLocked(op, e.Settings, func(name string, setter authorization.PrincipalRef) (bool, error) {
		if _, exists := auth.Roles.Find(setter); !exists {
			return false, nil
		}
		original := auth
		original.State.CurrentUser = setter
		return parameterSetAuthority(original, name)
	})
}
