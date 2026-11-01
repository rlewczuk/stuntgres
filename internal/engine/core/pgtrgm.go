package core

import (
	"fmt"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// ActivatePgTrgmLocked is the reached C-initialization boundary. Engine.Mu is
// held by the caller; physical settings precede a distinct effective image,
// then Session.Mu. Loading is physical lifetime, not a catalog-stage effect.
// Returned diagnostics are owned records; deliver them after settings/session
// locks are released, through the ordinary operation notice queue.
func (e *Engine) ActivatePgTrgmLocked(op *session.Operation) ([]*sqlstate.Error, error) {
	if op == nil || op.Session == nil {
		return nil, fmt.Errorf("stuntgres: pg_trgm initialization lacks an operation")
	}
	physical, effective := op.Session.Settings, op.EffectiveSettings()
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
	return effective.ActivatePgTrgmLocked(e.Settings)
}

// InitializePgTrgm runs only after ordinary routine EXECUTE/lifetime admission;
// Parse/Describe/binding and optimizer-elided strict NULL never call this host.
func (h *statementHost) InitializePgTrgm() error {
	scope := h.scope()
	e := scope.Engine()
	e.Mu.Lock()
	_, err := scope.RoutineAuthorizationLocked()
	var warnings []*sqlstate.Error
	if err == nil {
		warnings, err = e.ActivatePgTrgmLocked(scope.Op)
	}
	e.Mu.Unlock()
	for _, warning := range warnings {
		scope.Op.EmitNotice(session.LevelWarning, *warning)
	}
	return err
}

func (h *statementHost) PgTrgmThreshold(name string) (float64, error) {
	scope := h.scope()
	op, e := scope.Op, scope.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if _, err := scope.RoutineAuthorizationLocked(); err != nil {
		return 0, err
	}
	owner := op.EffectiveSettings()
	owner.Mu.Lock()
	defer owner.Mu.Unlock()
	op.Session.Mu.Lock()
	defer op.Session.Mu.Unlock()
	if err := owner.AdmitLocked(DomainOf(e), op); err != nil {
		return 0, err
	}
	return owner.PgTrgmThresholdLocked(e.Settings, name)
}
