package engine

import "stuntgres/internal/engine/sequence"

func (e *Engine) clearPersistentSequenceMemoryLocked() {
	e.sessionMu.Lock()
	defer e.sessionMu.Unlock()
	read := e.catalog.read(e.catalog.state)
	for _, s := range e.sessions {
		payloadOf(s).sqlRoutines = nil
		payloadOf(s).sequences.RetainLocked(sequence.OwnTemporary(read, s.SessionToken))
	}
}
