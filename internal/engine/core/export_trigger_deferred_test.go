package core

import (
	"time"

	"stuntgres/internal/engine/ids"
)

func ScopeSettleTriggerTerminalTest(s *StatementScope) error {
	return s.Engine().checkTransactionConstraints(s)
}

func ScopeEndTriggerTerminalTest(s *StatementScope) error {
	_, err := s.Engine().endAdmitted(s.Op, s.Owner, s.TxRef, true, restartNone, false, time.Time{}, s)
	s.controlOutcome = controlExecuted
	return err
}

func ScopeActiveConstraintTriggersTest(s *StatementScope) []ids.ObjectAddress {
	return s.activeConstraintTriggers
}
