package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// Called under source synchronization; generator scalars are detached from the
// source, unlike immutable relation descriptors.
func copySequenceState(source *Engine, view catalog.Read) (map[ids.ObjectAddress]*storage.SequenceGenerator, error) {
	result := make(map[ids.ObjectAddress]*storage.SequenceGenerator)
	for a, d := range view.Descriptors() {
		r, ok := d.(*catalog.Relation)
		if !ok || r.RelationKind != catalog.SequenceRelation {
			continue
		}
		g := source.storage.Sequences[a]
		if g == nil {
			return nil, fmt.Errorf("stuntgres: sequence state missing")
		}
		copy := *g
		result[a] = &copy
	}
	return result, nil
}

func (e *Engine) clearPersistentSequenceMemoryLocked() {
	e.sessionMu.Lock()
	defer e.sessionMu.Unlock()
	for _, s := range e.sessions {
		payloadOf(s).sqlRoutines = nil
		payloadOf(s).sequences.RetainLocked(func(a ids.ObjectAddress) bool {
			r, ok := e.catalog.state.Relation(a)
			if !ok {
				return false
			}
			ns, ok := e.catalog.state.Namespace(r.Namespace())
			return ok && ns.Temporary == s.SessionToken
		})
	}
}
