package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/nativetrig"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// invokeNativeTrigger borrows row slots for one admitted invocation. It enters
// the same balanced body boundary as PL without constructing a PL frame:
// routine and trigger depth, the routine environment, the command policy and
// function statistics. The native body (nativetrig.Invoke) runs inside it.
func (s *StatementScope) invokeNativeTrigger(d *catalog.Procedure, relation *catalog.Relation, p stored.TriggerParts, event stored.TriggerEvent, next, old []sqltype.Value, targets []int32) (result dml.BeforeResult, err error) {
	result = dml.BeforeResult{Row: next, Proceed: true}
	if err = s.Op.Context().Err(); err != nil {
		return result, err
	}
	if err = s.enterSQLRoutine(); err != nil {
		return result, err
	}
	defer s.leaveSQLRoutine()
	s.enterTrigger()
	defer s.leaveTrigger()
	leave, err := s.enterRoutineEnvironment(d)
	if err != nil {
		return result, err
	}
	defer func() { err = leave(err) }()
	leavePolicy := s.activateRoutineCommandPolicy(d, false)
	defer leavePolicy()
	stats := s.beginStatsFunction(p.Procedure, statsFunctionLanguage(d), false)
	defer func() { s.endStatsFunction(stats, err == nil) }()
	return nativetrig.Invoke[AuthorizationView](s.WriteHost(), d, relation, p, event, next, old, targets)
}
