package core

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) EnterGroupingCallback(callback catalog.GroupingRoutineCallback) (sqltype.RoutineBodyToken, error) {
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
	if err == nil {
		err = callback.Validate(auth.Catalog)
	}
	var current *catalog.Procedure
	if err == nil {
		var ok bool
		current, ok = auth.Catalog.Procedure(callback.Reference.Address)
		if !ok || !exec.SameRoutineDeclaration(current, callback.Procedure) {
			err = fmt.Errorf("stuntgres: stale selected grouping procedure")
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
