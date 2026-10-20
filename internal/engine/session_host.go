package engine

import "stuntgres/internal/engine/ids"

// sessionHost is the engine seen by session-layer code as a session.Host. It
// is a pointer conversion of *Engine, so it has no state of its own and costs
// no allocation per session.
type sessionHost Engine

// engineOf returns the engine that hosts s, or nil for a host-less session.
func engineOf(s *Session) *Engine {
	h, _ := s.host.(*sessionHost)
	return (*Engine)(h)
}

// sessionOfOwner returns the session behind a session.Host owner, or nil.
func sessionOfOwner(owner ids.SessionOwner) *Session {
	s, _ := owner.(*Session)
	return s
}

func (h *sessionHost) EndMaintenance(owner ids.SessionOwner) error {
	return (*Engine)(h).endTemporaryCollations(sessionOfOwner(owner))
}

func (h *sessionHost) Unregister(token ids.SessionToken, owner ids.SessionOwner) {
	e, s := (*Engine)(h), sessionOfOwner(owner)
	e.sessionMu.Lock()
	if e.sessions[token] == s {
		delete(e.sessions, token)
	}
	e.sessionMu.Unlock()
}

// engine returns the engine that runs the statement.
func (s *StatementScope) engine() *Engine { return engineOf(s.op.session) }

// operationAllocation places an operation and its binding host in one
// allocation.
type operationAllocation struct {
	op       Operation
	analysis bindingHost
}

// newSessionOperation allocates an operation of s together with its binding
// host (engine nil for a host-less test session) and sets op.session,
// op.self and the analysis slot. When Operation moves to session (13d.6),
// this becomes the Host's NewOperation.
func newSessionOperation(s *Session) *Operation {
	a := &operationAllocation{op: Operation{session: s}}
	op := &a.op
	op.self = op
	a.analysis = bindingHost{engine: engineOf(s), op: op}
	op.analysis = &a.analysis
	return op
}
