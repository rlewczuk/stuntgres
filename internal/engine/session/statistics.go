package session

import (
	"maps"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// StatisticsChange composes immutable column, extended and imported relation
// deltas, never row rewrites. Savepoints retain their immutable backing.
type StatisticsChange struct {
	Original         *storage.Relation
	Relation         *catalog.Relation
	OriginalRelation *catalog.Relation
	PrivateMetadata  *storage.RelationMetadata
	Columns          map[storage.StatisticsKey]*storage.ColumnStatistics
	Extended         map[storage.ExtendedStatisticsKey]*storage.ExtendedStatistics
	Imported         storage.ImportedRelationStatistics
	ImportedReset    uint8
	// Successful existing-object ANALYZE survives savepoint rollback. This
	// provenance prevents an older saved write from hiding its newer estimate.
	ImportedAnalyzeCount int64
	// Opaque last semantic new-file identity; ordinary storage transports and
	// in-place TRUNCATE preserve it. Only the journal, never snapshots, owns it.
	ImportedStorage  *storage.Relation
}

func (j *Journal) RecordStatistics(address ids.ObjectAddress, original *storage.Relation, originalRelation, relation *catalog.Relation, columns map[storage.StatisticsKey]*storage.ColumnStatistics, extended map[storage.ExtendedStatisticsKey]*storage.ExtendedStatistics, privateMetadata *storage.RelationMetadata) {
	previous, exists := j.Statistics[address]
	if exists {
		original = previous.Original
		originalRelation = previous.OriginalRelation
	}
	if privateMetadata == nil { privateMetadata = previous.PrivateMetadata }
	// This is a delta, not a resolved statistics root: nil tombstones must
	// survive until application to the latest canonical facts at commit.
	merged := previous.Columns
	if len(columns) != 0 {
		merged = maps.Clone(merged)
		if merged == nil { merged = make(map[storage.StatisticsKey]*storage.ColumnStatistics, len(columns)) }
		maps.Copy(merged, columns)
	}
	mergedExtended := previous.Extended
	if len(extended) != 0 {
		mergedExtended = maps.Clone(mergedExtended)
		if mergedExtended == nil { mergedExtended = make(map[storage.ExtendedStatisticsKey]*storage.ExtendedStatistics, len(extended)) }
		maps.Copy(mergedExtended, extended)
	}
	if j.Statistics == nil {
		j.Statistics = make(map[ids.ObjectAddress]StatisticsChange)
	}
	j.Statistics[address] = StatisticsChange{Original: original, OriginalRelation: originalRelation, Relation: relation, Columns: merged, Extended: mergedExtended, PrivateMetadata: privateMetadata, Imported: previous.Imported, ImportedReset: previous.ImportedReset, ImportedStorage: previous.ImportedStorage, ImportedAnalyzeCount: previous.ImportedAnalyzeCount}
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

// RecordRelationStatistics composes with attribute/extended deltas without
// changing any immutable state retained by savepoints.
func (j *Journal) RecordRelationStatistics(address ids.ObjectAddress, original *storage.Relation, originalRelation, relation *catalog.Relation, delta storage.ImportedRelationStatistics, baseline, private *storage.RelationMetadata) {
	j.RecordStatistics(address, original, originalRelation, relation, nil, nil, private)
	change := j.Statistics[address]
	change.Imported = change.ImportedWrites(baseline)
	change.ImportedAnalyzeCount = 0
	if baseline != nil { change.ImportedAnalyzeCount = baseline.AnalyzeCount }
	change.Imported = *storage.MergeImportedRelationStatistics(&change.Imported, delta)
	j.Statistics[address] = change
}

func ResolveRelationStatistics(j *Journal, address ids.ObjectAddress, metadata *storage.RelationMetadata) *storage.ImportedRelationStatistics {
	var facts *storage.ImportedRelationStatistics
	if metadata != nil { facts = metadata.Imported }
	if j != nil {
		if change, ok := j.Statistics[address]; ok {
			facts = storage.WithoutImportedRelationStatistics(facts, change.ImportedReset)
			facts = storage.MergeImportedRelationStatistics(facts, change.ImportedWrites(metadata))
		}
	}
	return facts
}

// ForgetRelationStatistics drops only estimates replaced by real maintenance.
func (j *Journal) ForgetRelationStatistics(address ids.ObjectAddress, mask uint8) {
	if j == nil { return }
	change, ok := j.Statistics[address]
	if !ok { return }
	change.Imported.Mask &^= mask
	j.Statistics[address] = change
}

// ResetRelationStatistics records an actual reset in operation order. Earlier
// writes are discarded only for reset fields; later writes may replace defaults.
func (j *Journal) ResetRelationStatistics(address ids.ObjectAddress, original *storage.Relation, originalRelation, relation *catalog.Relation, mask uint8, replacement storage.ImportedRelationStatistics, baseline, private *storage.RelationMetadata) {
	j.RecordStatistics(address, original, originalRelation, relation, nil, nil, private)
	change := j.Statistics[address]
	change.Imported = change.ImportedWrites(baseline)
	change.ImportedAnalyzeCount = 0
	if baseline != nil { change.ImportedAnalyzeCount = baseline.AnalyzeCount }
	change.ImportedReset |= mask
	change.Imported.Mask &^= mask
	change.Imported = *storage.MergeImportedRelationStatistics(&change.Imported, replacement)
	j.Statistics[address] = change
}

// ImportedWrites removes only fields superseded by nontransactional ANALYZE.
// Call before refreshing provenance as well as while resolving saved journals:
// a later unrelated page/visibility write cannot resurrect an old tuple value.
func (change StatisticsChange) ImportedWrites(baseline *storage.RelationMetadata) storage.ImportedRelationStatistics {
	writes := change.Imported
	var count int64
	if baseline != nil { count = baseline.AnalyzeCount }
	if change.ImportedAnalyzeCount != count {
		writes.Mask &^= storage.RelationTuples
		if baseline != nil && baseline.ForeignAnalyze != nil { writes.Mask &^= storage.RelationPages }
	}
	return writes
}
