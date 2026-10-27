package session

import (
	"maps"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// StatisticsChange is an immutable selected-attribute delta, not a row rewrite.
// Savepoint journals retain its backing; every further collection replaces it.
type StatisticsChange struct {
	Original         *storage.Relation
	Relation         *catalog.Relation
	OriginalRelation *catalog.Relation
	PrivateMetadata  *storage.RelationMetadata
	Columns          map[storage.StatisticsKey]*storage.ColumnStatistics
	Extended         map[storage.ExtendedStatisticsKey]*storage.ExtendedStatistics
}

func (j *Journal) RecordStatistics(address ids.ObjectAddress, original *storage.Relation, originalRelation, relation *catalog.Relation, columns map[storage.StatisticsKey]*storage.ColumnStatistics, extended map[storage.ExtendedStatisticsKey]*storage.ExtendedStatistics, privateMetadata *storage.RelationMetadata) {
	previous, exists := j.Statistics[address]
	if exists {
		original = previous.Original
		originalRelation = previous.OriginalRelation
	}
	// This is a delta, not a resolved statistics root: nil tombstones must
	// survive until application to the latest canonical facts at commit.
	merged := maps.Clone(previous.Columns)
	if merged == nil {
		merged = make(map[storage.StatisticsKey]*storage.ColumnStatistics, len(columns))
	}
	maps.Copy(merged, columns)
	mergedExtended := maps.Clone(previous.Extended)
	if mergedExtended == nil {
		mergedExtended = make(map[storage.ExtendedStatisticsKey]*storage.ExtendedStatistics, len(extended))
	}
	maps.Copy(mergedExtended, extended)
	if j.Statistics == nil {
		j.Statistics = make(map[ids.ObjectAddress]StatisticsChange)
	}
	j.Statistics[address] = StatisticsChange{Original: original, OriginalRelation: originalRelation, Relation: relation, Columns: merged, Extended: mergedExtended, PrivateMetadata: privateMetadata}
}

// ResolveStatistics overlays private facts on the same canonical producer.
func ResolveStatistics(j *Journal, address ids.ObjectAddress, metadata *storage.RelationMetadata, relation *catalog.Relation, read catalog.Read) *storage.RelationStatistics {
	var facts *storage.RelationStatistics
	if metadata != nil {
		facts = metadata.Statistics
	}
	if j != nil {
		if delta, ok := j.Statistics[address]; ok {
			facts = storage.MergeStatistics(facts, delta.Columns)
			facts = storage.MergeExtendedStatistics(facts, delta.Extended)
		}
	}
	facts = storage.CompatibleStatistics(facts, relation)
	facts = storage.CompatibleExtendedStatistics(facts, relation, read)
	if facts == nil {
		return nil
	}
	var deletions map[storage.StatisticsKey]*storage.ColumnStatistics
	checked := make(map[*storage.StatisticsHierarchy]bool)
	for key, fact := range facts.Columns {
		if !key.Inherited || fact.Hierarchy == nil {
			continue
		}
		valid, seen := checked[fact.Hierarchy]
		if !seen {
			valid = storage.StatisticsHierarchyCurrent(fact.Hierarchy, relation, read)
			checked[fact.Hierarchy] = valid
		}
		if !valid {
			if deletions == nil {
				deletions = make(map[storage.StatisticsKey]*storage.ColumnStatistics)
			}
			deletions[key] = nil
		}
	}
	return storage.MergeStatistics(facts, deletions)
}

// ResolveRelationMetadata retains private maintenance only for not-yet-public
// objects. Existing-object maintenance is always read from the canonical root.
func ResolveRelationMetadata(j *Journal, canonical map[ids.ObjectAddress]*storage.RelationMetadata, address ids.ObjectAddress) *storage.RelationMetadata {
	if metadata := canonical[address]; metadata != nil {
		return metadata
	}
	if j != nil {
		return j.Statistics[address].PrivateMetadata
	}
	return nil
}
