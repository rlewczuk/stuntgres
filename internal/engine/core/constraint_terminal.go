package core

// Admission is already owned by terminal control. Borrow only the active
// operation-to-scope binding: do not start a statement or rearm its timeout.
// Keep it through publication so nested SQL and DDL see this exact Stage and
// catalog journal, then restore any suspended/finished caller's lease.
func (s *StatementScope) borrowTerminalOperation() func() {
	op, sess := s.Op, s.Op.Session
	sess.Mu.Lock()
	previous, finished := op.Lease, op.SQLFinished
	if previous == &s.lease {
		sess.Mu.Unlock()
		return func() {}
	}
	s.lease.TimeoutRef = s.TxRef
	op.Lease, op.SQLFinished = &s.lease, false
	sess.Mu.Unlock()
	return func() {
		sess.Mu.Lock()
		op.Lease, op.SQLFinished = previous, finished
		sess.Mu.Unlock()
	}
}

// A native terminal operation borrows a scope without owning statement
// admission. Dispose its evaluator resources without Finish or a second publish.
// The caller holds no owner/engine/session mutex.
func (s *StatementScope) releaseTerminalScope() {
	for _, program := range s.rlsPrograms {
		program.Close()
	}
	if s.View != nil {
		s.View.Release()
		s.View = nil
	}
	s.partitionsSlot.ReleaseValidationView()
	s.partitionsSlot.DropPrograms()
	s.Domains.endAdmission()
	s.Inheritance.endStatement()
	s.afterTriggers, s.indexesSlot, s.foreignActionsSlot = nil, nil, nil
	s.activeConstraintTriggers = nil
	s.routinePrograms, s.checkPrograms = nil, nil
	s.rlsPrograms, s.rlsActive = nil, nil
	s.RoutineAuth, s.ForeignActionAuth = AuthorizationView{}, nil
	s.RoutineDependencies = nil
	s.Stage.Rows, s.Stage.Deletes, s.Stage.Stats = nil, nil, nil
	s.Stage.DiscardedHeaps = nil
	s.OwnWrites, s.OwnDeletes = nil, nil
	s.Catalog, s.CatalogAuthority = nil, nil
	s.currentSnapshot = nil
	s.QuerySources, s.QueryLockContext = nil, nil
	s.LockTicket = nil
	s.tupleAdmissions = nil
	if s.authority != nil {
		s.authority.Host = nil
	}
	s.Finished = true
}
