package engine

import "stuntgres/internal/engine/ids"

func (s *StatementScope) physicalRowEvent(ref ids.ObjectRef, row rowVersion, mode multiMode, successor ids.VersionID) error {
	e := s.op.session.Engine()
	e.mu.Lock()
	err := e.assignTransactionXIDLocked(s.txRef)
	e.mu.Unlock()
	if err != nil {
		return err
	}
	return s.op.session.Engine().locks.physicalEvents(s.ticket, []multiEvent{{tuple: multiTuple{lockResource{ObjectRef: ref, row: row.row}, row.version}, mode: mode, successor: successor}})
}

func (s *StatementScope) flushPhysicalEvents() error {
	events := s.multiEvents
	if len(events) == 0 {
		return nil
	}
	e := s.op.session.Engine()
	e.mu.Lock()
	assignmentErr := e.assignTransactionXIDLocked(s.txRef)
	e.mu.Unlock()
	if assignmentErr != nil {
		clear(events)
		s.multiEvents = events[:0]
		return assignmentErr
	}
	err := s.op.session.Engine().locks.physicalEvents(s.ticket, events)
	clear(events)
	s.multiEvents = events[:0]
	return err
}
