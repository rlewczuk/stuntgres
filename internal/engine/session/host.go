package session

import "stuntgres/internal/engine/ids"

// Host is what session-layer code needs from the engine that owns the
// session. The root engine implements it; session code never names the
// engine. Each method states the locks its caller may hold.
type Host interface {
	// EndMaintenance runs the engine's end-of-session maintenance for owner
	// (TEMP collation namespaces). The session calls it after the
	// Transaction resource callback has returned, without Session.mu and
	// never from inside a SessionResource callback. It takes Engine.mu and
	// may start and end a private maintenance session.
	EndMaintenance(owner ids.SessionOwner) error
	// Unregister removes owner from the engine's session registry if it is
	// still registered under token. The session calls it while holding
	// Session.mu; it takes only Engine.sessionMu (lock order Session.mu,
	// then Engine.sessionMu) and never Engine.mu or the lock manager.
	Unregister(token ids.SessionToken, owner ids.SessionOwner)
	// NewOperation allocates an operation of s with Session and Self set,
	// together with the engine's per-operation analysis state (the Analysis
	// slot). Session.Operation calls it, possibly while holding Session.Mu;
	// it only allocates and takes no lock.
	NewOperation(s *Session) *Operation
}
