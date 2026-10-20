package engine

import "stuntgres/internal/engine/ids"

// StatementLease is a statement's identity for session-layer state that must
// not name the statement scope: the operation's current statement, the
// transaction timeout slot and the owner's COPY reservation. Each scope holds
// one lease and holders compare leases by address, so a lease is never
// copied on purpose.
type StatementLease struct {
	// Statement is the owning statement, opaque to session-layer code; the
	// engine recovers it with scopeOfLease.
	Statement any
	// TimeoutRef is the transaction the statement was admitted under, for
	// preparse ERROR timer retirement. Session.mu guards it.
	TimeoutRef ids.TxRef
}
