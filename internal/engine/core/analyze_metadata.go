package core

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
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
		if facts := storage.CompatibleExtendedStatistics(entry.Statistics, relation, e.Catalog.Read(next)); facts != entry.Statistics {
			copy := *entry
			copy.Statistics = facts
			metadata[address] = &copy
		}
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
			entry.Statistics = storage.CompatibleStatistics(storage.MergeExtendedStatistics(storage.MergeStatistics(entry.Statistics, change.Columns), change.Extended), relation)
			entry.Statistics = storage.CompatibleExtendedStatistics(entry.Statistics, relation, e.Catalog.Read(next))
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
		candidate := *previous
		if previous.Statistics != nil && len(previous.Statistics.Extended) != 0 {
			payloadType, exists := e.Catalog.Read(next).LookupType(sqltype.PgNDistinct)
			if !exists {
				return nil, fmt.Errorf("stuntgres: missing retained ndistinct payload type")
			}
			stats := *previous.Statistics
			stats.Extended = make(map[storage.ExtendedStatisticsKey]*storage.ExtendedStatistics, len(previous.Statistics.Extended))
			for key, fact := range previous.Statistics.Extended {
				oldDefinition, ok := e.Catalog.Current().Statistics(key.Definition.Address)
				definition, exists := next.Statistics(key.Definition.Address)
				if !ok || !exists || definition.Namespace() != oldDefinition.Namespace() || definition.Revision() != oldDefinition.Revision() || temporary[definition.Namespace()] == nil || definition.Selection != fact.Selection {
					continue
				}
				key.Definition = e.Catalog.Domain.Ref(next.Epoch(), definition.Address())
				value := *fact
				value.Relation = definition.Relation
				if value.Value.Type() != payloadType {
					// TEMP rows retain their exact defining usages, but the
					// collected payload has independent builtin authority.
					// Rebuild from checked items before installation, leaving
					// the old fact and its captured typed value untouched.
					value.Value, err = sqltype.StatisticsValue(payloadType, fact.Items)
					if err != nil {
						return nil, err
					}
				}
				stats.Extended[key] = &value
			}
			candidate.Statistics = &stats
		}
		facts := session.ResolveStatistics(nil, address, &candidate, retained, e.Catalog.Read(next))
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
