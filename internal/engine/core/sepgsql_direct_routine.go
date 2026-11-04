package core

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// EnterDirectRoutine is for an actual fmgr callback, never ordinary SQL
// expression execution. The callback owner supplies its original selected ref.
func (h *statementHost) EnterDirectRoutine(original *catalog.Procedure, ref ids.ObjectRef) (sqltype.RoutineBodyToken, error) {
	s := h.scope()
	if err := s.Active(); err != nil {
		return sqltype.RoutineBodyToken{}, err
	}
	e := s.Engine()
	if e.Sepgsql == nil {
		return sqltype.RoutineBodyToken{}, nil
	}
	mark := s.MarkSepgsqlObservations()
	e.Mu.Lock()
	auth, err := s.RoutineAuthorizationLocked()
	var current *catalog.Procedure
	if err == nil {
		if original == nil || ref.Address != original.Address() || ref.Domain() != auth.Catalog.Domain() || ref.Epoch != auth.Catalog.Epoch() {
			err = ids.ErrCatalogReplaced
		} else {
			var ok bool
			current, ok = auth.Catalog.Procedure(ref.Address)
			if !ok || !exec.SameRoutineDeclaration(current, original) {
				err = fmt.Errorf("stuntgres: stale direct routine callback")
			}
		}
	}
	target, transition := "", false
	if err == nil {
		target, transition, err = s.sepgsqlBodyDecision(auth, current, false, nil)
	}
	e.Mu.Unlock()
	if err != nil {
		s.RestoreSepgsqlObservations(mark)
		return sqltype.RoutineBodyToken{}, err
	}
	if !transition {
		return sqltype.RoutineBodyToken{}, nil
	}
	return s.Op.Session.Sepgsql.Enter(target), nil
}
