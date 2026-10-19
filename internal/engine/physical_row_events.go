package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
)

func (s *StatementScope) physicalRowEvent(ref ids.ObjectRef, row storage.RowVersion, mode lock.MultiMode, successor ids.VersionID) error {
	e := s.op.session.Engine()
	e.mu.Lock()
	err := e.assignTransactionXIDLocked(s.txRef)
	e.mu.Unlock()
	if err != nil {
		return err
	}
	return s.op.session.Engine().locks.PhysicalEvents(s.ticket, []lock.MultiEvent{{Tuple: lock.MultiTuple{lock.Resource{ObjectRef: ref, Row: row.Row}, row.Version}, Mode: mode, Successor: successor}})
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
	err := s.op.session.Engine().locks.PhysicalEvents(s.ticket, events)
	clear(events)
	s.multiEvents = events[:0]
	return err
}
