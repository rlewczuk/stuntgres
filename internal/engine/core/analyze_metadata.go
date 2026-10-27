package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// prepareRelationMetadataLocked prepares the existing Store's single ANALYZE
// metadata root. Its entries are immutable and independent of physical heaps.
// All allocation precedes the caller's no-failure installation boundary.
func (e *Engine) prepareRelationMetadataLocked(next *catalog.State, journal *session.Journal) map[ids.ObjectAddress]*storage.RelationMetadata {
	if len(e.Storage.RelationMetadata) == 0 && (journal == nil || len(journal.Statistics) == 0) {
		return nil
	}
	metadata := make(map[ids.ObjectAddress]*storage.RelationMetadata, len(e.Storage.RelationMetadata))
	for address, previous := range e.Storage.RelationMetadata {
		relation, exists := next.Relation(address)
		if !exists {
			continue
		}
		entry := previous
		if next.Get(address) != e.Catalog.State.Get(address) {
			facts := storage.CompatibleStatistics(previous.Statistics, relation)
			if facts != previous.Statistics {
				copy := *previous
				copy.Statistics = facts
				entry = &copy
			}
		}
		metadata[address] = entry
	}
	if journal != nil {
		for address, change := range journal.Statistics {
			relation, exists := next.Relation(address)
			if !exists {
				continue
			}
			previous := metadata[address]
			if previous == nil {
				previous = change.PrivateMetadata
			}
			var entry storage.RelationMetadata
			if previous != nil {
				entry = *previous
			}
			entry.Statistics = storage.CompatibleStatistics(storage.MergeStatistics(entry.Statistics, change.Columns), relation)
			metadata[address] = &entry
		}
	}
	return metadata
}

// prepareRetainedRelationMetadataLocked uses the replacement's existing TEMP
// retention authority, not imported addresses or heap-pointer coincidence.
// Persistent and reminted identities deliberately receive no planner facts.
func (e *Engine) prepareRetainedRelationMetadataLocked(next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation) (map[ids.ObjectAddress]*storage.RelationMetadata, error) {
	if len(e.Storage.RelationMetadata) == 0 {
		return nil, nil
	}
	temporary, err := control.TemporarySet(e.Catalog.Current(), next)
	if err != nil {
		return nil, err
	}
	var metadata map[ids.ObjectAddress]*storage.RelationMetadata
	for address, descriptor := range temporary {
		original, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		previous := e.Storage.RelationMetadata[address]
		if previous == nil {
			continue
		}
		retained, exists := next.Relation(address)
		// Retention reinstalls the exact destination namespace. Policy and
		// trigger epoch rebindings may copy a relation descriptor, but cannot
		// change its defining identity or stored type/layout authority.
		if !exists || next.Get(original.Namespace()) != e.Catalog.State.Get(original.Namespace()) ||
			retained.Namespace() != original.Namespace() || retained.Revision() != original.Revision() ||
			retained.Owner != original.Owner || retained.RelationKind != original.RelationKind ||
			!index.SameRelationRowShape(original, retained) {
			continue
		}
		if original.RelationKind.Stored() && relations[address] != e.Storage.Relations[address] {
			continue
		}
		facts := session.ResolveStatistics(nil, address, previous, retained, e.Catalog.Read(next))
		entry := previous
		if facts != previous.Statistics {
			copy := *previous
			copy.Statistics = facts
			entry = &copy
		}
		if metadata == nil {
			metadata = make(map[ids.ObjectAddress]*storage.RelationMetadata)
		}
		metadata[address] = entry
	}
	return metadata, nil
}
