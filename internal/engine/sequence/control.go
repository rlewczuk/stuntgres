package sequence

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// CopyGenerators detaches the generator of every sequence in view from
// generators, a source engine's live generators. The caller holds the
// source's synchronization; unlike immutable relation descriptors,
// generator scalars must not be shared.
func CopyGenerators(view catalog.Read, generators map[ids.ObjectAddress]*storage.SequenceGenerator) (map[ids.ObjectAddress]*storage.SequenceGenerator, error) {
	result := make(map[ids.ObjectAddress]*storage.SequenceGenerator)
	for a, d := range view.Descriptors() {
		r, ok := d.(*catalog.Relation)
		if !ok || r.RelationKind != catalog.SequenceRelation {
			continue
		}
		g := generators[a]
		if g == nil {
			return nil, fmt.Errorf("stuntgres: sequence state missing")
		}
		copy := *g
		result[a] = &copy
	}
	return result, nil
}

// OwnTemporary reports, for SessionMemory.RetainLocked across a persistent
// control, whether a remembered sequence is one of token's own TEMP
// sequences in read.
func OwnTemporary(read catalog.Read, token ids.SessionToken) func(ids.ObjectAddress) bool {
	return func(a ids.ObjectAddress) bool {
		r, ok := read.Relation(a)
		if !ok {
			return false
		}
		ns, ok := read.Namespace(r.Namespace())
		return ok && ns.Temporary == token
	}
}
