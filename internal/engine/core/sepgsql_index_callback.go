package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) EnterIndexSupportCallback(callback catalog.IndexRoutineCallback) (sqltype.RoutineBodyToken, error) {
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
	target, transition := "", false
	if err == nil {
		current, _ := auth.Catalog.Procedure(callback.Reference.Address)
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
