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
}

func (j *Journal) RecordStatistics(address ids.ObjectAddress, original *storage.Relation, originalRelation, relation *catalog.Relation, columns map[storage.StatisticsKey]*storage.ColumnStatistics, privateMetadata *storage.RelationMetadata) {
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
	if j.Statistics == nil {
		j.Statistics = make(map[ids.ObjectAddress]StatisticsChange)
	}
	j.Statistics[address] = StatisticsChange{Original: original, OriginalRelation: originalRelation, Relation: relation, Columns: merged, PrivateMetadata: privateMetadata}
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
		}
	}
	facts = storage.CompatibleStatistics(facts, relation)
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
			valid = statisticsHierarchyCurrent(fact.Hierarchy, relation, read)
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

func statisticsHierarchyCurrent(hierarchy *storage.StatisticsHierarchy, root *catalog.Relation, read catalog.Read) bool {
	captured := make(map[ids.ObjectAddress]*catalog.Relation, len(hierarchy.Relations))
	for _, relation := range hierarchy.Relations {
		captured[relation.Address()] = relation
	}
	seen := make(map[ids.ObjectAddress]bool, len(captured))
	var visit func(*catalog.Relation) bool
	visit = func(relation *catalog.Relation) bool {
		namespace, ok := read.Namespace(relation.Namespace())
		if !ok {
			return false
		}
		if namespace.Temporary != (ids.SessionToken{}) && namespace.Temporary != hierarchy.Session {
			return true
		}
		if seen[relation.Address()] {
			return true
		}
		seen[relation.Address()] = true
		old, ok := captured[relation.Address()]
		if !ok || len(old.Attributes) != len(relation.Attributes) {
			return false
		}
		for i, attribute := range old.Attributes {
			slot := relation.IndexForAttribute(attribute)
			if slot < 0 || !catalog.SamePartitionColumnUsage(old.Columns[i].Usage, relation.Columns[slot].Usage) {
				return false
			}
		}
		children := catalog.InheritanceChildren(read, relation.Address())
		if relation.Partition != nil {
			for _, address := range relation.Partition.Children {
				child, ok := read.Relation(address)
				if !ok {
					return false
				}
				children = append(children, child)
			}
		}
		for _, child := range children {
			if child.Foreign == nil && !visit(child) {
				return false
			}
		}
		return true
	}
	return root != nil && visit(root) && len(seen) == len(captured)
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
