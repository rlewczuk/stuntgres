package engine

import "stuntgres/internal/engine/sequence"

func (e *Engine) clearPersistentSequenceMemoryLocked() {
	e.sessionMu.Lock()
	defer e.sessionMu.Unlock()
	read := e.Catalog.Current()
	for _, s := range e.sessions {
		PayloadOf(s).sqlRoutines = nil
		PayloadOf(s).Sequences.RetainLocked(sequence.OwnTemporary(read, s.SessionToken))
	}
}
