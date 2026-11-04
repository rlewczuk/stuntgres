package core

import (
	"stuntgres/internal/engine/core/tsearch"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/remote"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
)

// sessionPayload is the root-owned part of a session (13c decision 3): SQL
// cursors, the prepared SQL registry, retained SQL routine analyses, sequence
// session memory and the notification listener (a session.NotificationListener
// whose fields belong to Engine.mu). startSession allocates it
// together with the session (sessionAllocation) and stores it in
// Session.payload before the session is shared; the slot never changes.
// Each field keeps the lock discipline it had as a Session field.
type sessionPayload struct {
	allocation      *sessionAllocation // Immutable enclosing backing owner; set before publication.
	admission       *DatabaseAdmission // Terminal Unregister owns its once-only release.
	Cursors         *cursorRegistry
	PreparedSQL     *PreparedRegistry
	sqlRoutines     map[ids.ObjectAddress]sqlRoutineAnalysis
	Sequences       sequence.SessionMemory
	notifications   session.NotificationListener
	externalSQL     *remote.Owner            // Physical command admission serializes initial attachment.
	dictionaryMemos tsearch.DictionaryMemos  // Engine.Mu; this session's successful initializations.
	statsWriter     statistics.Writer        // Transaction admission serializes collection and drains.
	statsStopped    ids.TxRef                // Prevents failed-block cleanup from stamping the same stop twice.
	statsReader     statistics.Reader        // Cleared at every transaction terminal boundary.
	statsFlush      statsFlushSlot           // Session.Mu; callbacks use nonblocking transaction admission.
	origin          replicationOriginSession // Engine.Mu; physical, never journaled.
	statsPublic     bool                     // Immutable successful public-registration identity.
	statsOutcome    ids.TxRef                // Admission-owned once-only top transaction outcome.
	statsSession    statistics.SessionUsage
}

func (*sessionPayload) PayloadSlot() {}

// sessionAllocation places a session, its root payload and its transaction
// owner's root extension in one allocation.
type sessionAllocation struct {
	session session.Session
	payload sessionPayload
	owner   ownerExtension
}

// PayloadOf returns the root payload of s, or nil for a session built without
// one (unit-test literals).
func PayloadOf(s *session.Session) *sessionPayload {
	p, _ := s.Payload.(*sessionPayload)
	return p
}
