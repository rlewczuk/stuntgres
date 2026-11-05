package core

import (
	"stuntgres/internal/engine/remote"
	"stuntgres/internal/engine/session"
)

// Engine.Mu serializes this capture with admitted Open registration/publication.
// Never nest sessionMu with Session.Mu (Unregister takes them in the reverse
// direction). Detachment only advances the same owner's generation and removes
// feature indices; the returned work must run after ALL replacement locks drop.
func (e *Engine) detachDblinkLocked() []remote.DblinkCleanup {
	e.sessionMu.Lock()
	sessions := make([]*session.Session, 0, len(e.sessions))
	for _, physical := range e.sessions {
		sessions = append(sessions, physical)
	}
	e.sessionMu.Unlock()
	var cleanup []remote.DblinkCleanup
	for _, physical := range sessions {
		physical.Mu.Lock()
		payload := PayloadOf(physical)
		if payload != nil && payload.externalSQL != nil {
			cleanup = append(cleanup, payload.externalSQL.DetachDblink())
		}
		physical.Mu.Unlock()
	}
	return cleanup
}
