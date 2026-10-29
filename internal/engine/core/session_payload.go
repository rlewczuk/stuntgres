package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
)

// sessionPayload is the root-owned part of a session (13c decision 3): SQL
// cursors, the prepared SQL registry, retained SQL routine analyses, sequence
// session memory and the notification listener (a session.NotificationListener
// whose fields belong to Engine.mu). startSession allocates it
// together with the session (sessionAllocation) and stores it in
// Session.payload before the session is shared; the slot never changes.
// Each field keeps the lock discipline it had as a Session field.
type sessionPayload struct {
	admission      *DatabaseAdmission // Terminal Unregister owns its once-only release.
	Cursors        *cursorRegistry
	PreparedSQL    *PreparedRegistry
	sqlRoutines    map[ids.ObjectAddress]sqlRoutineAnalysis
	Sequences      sequence.SessionMemory
	notifications  session.NotificationListener
	thesaurusMemos map[ids.ObjectAddress]thesaurusMemo // Engine.Mu; this session's successful initializations.
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
