package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"time"
)

// sessionHost is the engine seen by session-layer code as a session.Host. It
// is a pointer conversion of *Engine, so it has no state of its own and costs
// no allocation per session.
type sessionHost Engine

// EngineOf returns the engine that hosts s, or nil for a host-less session.
func EngineOf(s *session.Session) *Engine {
	h, _ := s.Host.(*sessionHost)
	return (*Engine)(h)
}

func (h *sessionHost) EndMaintenance(s *session.Session) error {
	return (*Engine)(h).endTemporaryCollations(s)
}

func (h *sessionHost) StatisticsReadyLocked(s *session.Session) {
	if p := PayloadOf(s); p != nil && p.statsFlush.requested {
		statsReadyLocked(s, time.Now())
	}
}

func (h *sessionHost) DrainStatistics(s *session.Session) {
	cancelStatsFlush(s)
	// This hook runs outside Session.Mu after resources drain. Never take the
	// cluster mutex in Unregister: PID classification locks cluster then session.
	e := (*Engine)(h)
	token := s.Token()
	if token.ID() == 0 || token.IsDetached() || token.Private() != nil {
		return
	}
	e.Mu.Lock()
	e.sessionMu.Lock()
	registered := e.sessions[token] == s
	e.sessionMu.Unlock()
	if registered {
		e.activityProvider().End(token)
		e.Statistics.End(token)
		if e.Cluster != nil {
			for _, peer := range e.Cluster.Targets {
				if peer != e {
					peer.Statistics.End(token)
				}
			}
		}
	}
	e.Mu.Unlock()
}

func (h *sessionHost) Unregister(token ids.SessionToken, s *session.Session) {
	e := (*Engine)(h)
	e.sessionMu.Lock()
	registered := e.sessions[token] == s
	if registered {
		delete(e.sessions, token)
	}
	e.sessionMu.Unlock()
	if payload := PayloadOf(s); payload != nil {
		statsIdle(s, true)
		if registered {
			e.Statistics.End(token)
		}
		payload.admission.release(true)
	}
}

// Engine returns the engine that runs the statement.
func (s *StatementScope) Engine() *Engine { return EngineOf(s.Op.Session) }

// operationEngine is the engine of op's session, or nil without one.
func operationEngine(op *session.Operation) *Engine {
	if op == nil || op.Session == nil {
		return nil
	}
	return EngineOf(op.Session)
}

// operationAllocation places an operation and its binding host in one
// allocation.
type operationAllocation struct {
	op       session.Operation
	analysis BindingHost
}

// NewOperation allocates an operation of s together with its binding host
// and sets op.Session, op.Self and the analysis slot.
func (h *sessionHost) NewOperation(s *session.Session) *session.Operation {
	a := &operationAllocation{op: session.Operation{Session: s}}
	op := &a.op
	op.Self = op
	a.analysis = BindingHost{Engine: (*Engine)(h), Op: op}
	op.Analysis = &a.analysis
	return op
}
