package session

import "stuntgres/internal/engine/ids"

// Host is what session-layer code needs from the engine that owns the
// session. The root engine implements it; session code never names the
// engine. Each method states the locks its caller may hold.
type Host interface {
	// EndMaintenance runs the engine's end-of-session maintenance for s
	// (TEMP collation namespaces). The session calls it after the
	// Transaction resource callback has returned, without Session.Mu and
	// never from inside a SessionResource callback. It takes Engine.mu and
	// may start and end a private maintenance session.
	EndMaintenance(s *Session) error
	// Unregister removes only s's exact registration and releases its physical
	// admission after all owners drain. Called under Session.Mu, it takes local
	// sessionMu, drops it, then takes cluster AdmissionMu; never metadata Mu or
	// the lock manager. AdmissionMu has no path back to session/metadata locks.
	Unregister(token ids.SessionToken, s *Session)
	// NewOperation allocates an operation of s with Session and Self set,
	// together with the engine's per-operation analysis state (the Analysis
	// slot). Session.Operation calls it, possibly while holding Session.Mu;
	// it only allocates and takes no lock.
	NewOperation(s *Session) *Operation
	// StatisticsReadyLocked services an already-due cumulative flush request.
	// Session.Mu is held; it may only try admission and owner mutexes without
	// blocking, then take the leaf provider lock. It never takes metadata Mu.
	StatisticsReadyLocked(s *Session)
	// DrainStatistics invalidates and joins deferred publication callbacks
	// after all resource callbacks, before Unregister, without session,
	// admission, transaction-owner or metadata locks held.
	DrainStatistics(s *Session)
}
