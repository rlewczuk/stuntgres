package core

import "stuntgres/internal/sqlstate"

// SyncReplicationSlots borrows the effective actor under Engine -> Session lock
// ordering. No authority or provider state survives the call.
func (h *statementHost) SyncReplicationSlots() error {
	s := h.scope()
	e := s.Engine()
	e.Mu.Lock()
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	if !actor.Superuser && !actor.Replication {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied to use replication slots", Detail: "Only roles with the REPLICATION attribute may use replication slots."}
	}
	return SQLError("0A000", "pg_sync_replication_slots physical replication slot synchronization is unsupported in embedded mode")
}
