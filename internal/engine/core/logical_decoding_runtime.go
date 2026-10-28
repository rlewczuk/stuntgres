package core

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// LogicalDecoding borrows the executing actor under Engine.Mu, following
// RoutineAuthorizationLocked's Engine -> Session order. No callback or external
// I/O runs under the lock; no slot/decoder state is created or retained.
func (h *statementHost) LogicalDecoding(slot, options sqltype.Value) error {
	s := h.scope()
	e := s.Engine()
	e.Mu.Lock()
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil { e.Mu.Unlock(); return err }
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	e.Mu.Unlock()
	if err != nil { return err }
	if !actor.Superuser && !actor.Replication {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied to use replication slots", Detail: "Only roles with the REPLICATION attribute may use replication slots."}
	}
	if err := functions.CheckLogicalDecodingOptions(slot, options); err != nil { return err }
	name, err := slot.Text()
	if err != nil { return err }
	// The native catalog has no replication slots. Preserve the truthful lookup
	// failure rather than fabricating a slot or an unreachable decoding branch.
	return SQLError("42704", "replication slot \""+name+"\" does not exist")
}
