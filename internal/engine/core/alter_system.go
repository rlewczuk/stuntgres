package core

// ALTER SYSTEM is strictly read-only for database admission. PostgreSQL's block
// predicate includes live internal subtransactions, unlike Owner.InBlock's
// SQL block predicate. Check those existing marks before routine scope, without
// acquiring a snapshot or consulting setting definitions.
func (s *StatementScope) alterSystemEligibility() error {
	s.Owner.Mu.Lock()
	inBlock := s.Owner.InBlock()
	// PL exception marks represent PostgreSQL's TBLOCK_SUBINPROGRESS.
	for _, mark := range s.Owner.Savepoints {
		if mark.Internal {
			inBlock = true
			break
		}
	}
	s.Owner.Mu.Unlock()
	if inBlock {
		return SQLError("25001", "ALTER SYSTEM cannot run inside a transaction block")
	}
	if s.activeSQLRoutineDepth != 0 {
		return SQLError("25001", "ALTER SYSTEM cannot be executed from a function")
	}
	return nil
}

func (s *StatementScope) executeAlterSystem() (Result, error) {
	if err := s.alterSystemEligibility(); err != nil {
		return Result{}, err
	}
	return Result{}, SQLError("0A000", "ALTER SYSTEM is not allowed in this environment")
}
