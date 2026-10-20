package engine

import "stuntgres/internal/engine/ids"

// sessionHost is the engine seen by session-layer code as a session.Host. It
// is a pointer conversion of *Engine, so it has no state of its own and costs
// no allocation per session.
type sessionHost Engine

// engineOf returns the engine that hosts s, or nil for a host-less session.
func engineOf(s *Session) *Engine {
	h, _ := s.Host.(*sessionHost)
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
func (s *StatementScope) engine() *Engine { return engineOf(s.op.Session) }

// operationAllocation places an operation and its binding host in one
// allocation.
type operationAllocation struct {
	op       Operation
	analysis bindingHost
}

// NewOperation allocates an operation of s together with its binding host
// and sets op.Session, op.Self and the analysis slot.
func (h *sessionHost) NewOperation(s *Session) *Operation {
	a := &operationAllocation{op: Operation{Session: s}}
	op := &a.op
	op.Self = op
	a.analysis = bindingHost{engine: (*Engine)(h), op: op}
	op.Analysis = &a.analysis
	return op
}
