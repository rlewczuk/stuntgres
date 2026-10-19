package storage

import "stuntgres/internal/engine/ids"

// SequenceGenerator is a sequence's nontransactional generator state. It is
// distinct from the transactional sequence options in the catalog and from
// per-session currval/lastval memories, which stay in the engine.
type SequenceGenerator struct {
	Last       int64
	Called     bool
	Generation ids.DefinitionRevision
}
