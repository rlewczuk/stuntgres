package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
)

// sessionPayload is the root-owned part of a session (13c decision 3): SQL
// cursors, the prepared SQL registry, retained SQL routine analyses, sequence
// session memory and the notification listener. startSession allocates it
// together with the session (sessionAllocation) and stores it in
// Session.payload before the session is shared; the slot never changes.
// Each field keeps the lock discipline it had as a Session field.
type sessionPayload struct {
	cursors       *cursorRegistry
	preparedSQL   *sqlPreparedRegistry
	sqlRoutines   map[ids.ObjectAddress]sqlRoutineAnalysis
	sequences     sequence.SessionMemory
	notifications sessionNotifications
}

func (*sessionPayload) PayloadSlot() {}

// sessionAllocation places a session, its root payload and its transaction
// owner's root extension in one allocation.
type sessionAllocation struct {
	session session.Session
	payload sessionPayload
	owner   ownerExtension
}

// payloadOf returns the root payload of s, or nil for a session built without
// one (unit-test literals).
func payloadOf(s *session.Session) *sessionPayload {
	p, _ := s.Payload.(*sessionPayload)
	return p
}
