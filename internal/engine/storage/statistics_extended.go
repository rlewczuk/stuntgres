package storage

import (
	"maps"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// ExtendedStatisticsKey includes the generation-qualified definition identity.
type ExtendedStatisticsKey struct {
	Definition ids.ObjectRef
	Inherited  bool
}

// ExtendedStatistics shares the immutable ANALYZE metadata publication root.
// Items and Usages are owned construction facts, never borrowed row storage.
type ExtendedStatistics struct {
	Relation  ids.ObjectRef
	Selection catalog.StatisticsSelection
	Usages    [8]sqltype.Usage
	Value     sqltype.Value
	Items     []sqltype.StatisticsItem
	Hierarchy *StatisticsHierarchy
}

func extendedStatistics(s *RelationStatistics) map[ExtendedStatisticsKey]*ExtendedStatistics {
	if s == nil {
		return nil
	}
	return s.Extended
}

// MergeExtendedStatistics preserves unselected immutable payload backing.
func MergeExtendedStatistics(base *RelationStatistics, replacements map[ExtendedStatisticsKey]*ExtendedStatistics) *RelationStatistics {
	if len(replacements) == 0 {
		return base
	}
	result := &RelationStatistics{}
	if base != nil {
		result.Columns = base.Columns
		result.Extended = maps.Clone(base.Extended)
	}
	if result.Extended == nil {
		result.Extended = make(map[ExtendedStatisticsKey]*ExtendedStatistics, len(replacements))
	}
	for key, fact := range replacements {
		if fact == nil {
			delete(result.Extended, key)
		} else {
			result.Extended[key] = fact
		}
	}
	if len(result.Columns) == 0 && len(result.Extended) == 0 {
		return nil
	}
	return result
}

// CompatibleExtendedStatistics rejects replaced definitions and selected usage
// changes, but deliberately retains stale facts across DML and target changes.
func CompatibleExtendedStatistics(stats *RelationStatistics, relation *catalog.Relation, read catalog.Read) *RelationStatistics {
	if stats == nil || len(stats.Extended) == 0 {
		return stats
	}
	var deletions map[ExtendedStatisticsKey]*ExtendedStatistics
	var hierarchies map[*StatisticsHierarchy]bool
	for key, fact := range stats.Extended {
		d, ok := read.Statistics(key.Definition.Address)
		valid := ok && fact != nil && relation != nil &&
			key.Definition.Domain() == read.Domain() && key.Definition.Epoch == read.Epoch() &&
			d.Relation == fact.Relation && d.Relation.Address == relation.Address() && d.Selection == fact.Selection
		if valid && key.Inherited {
			valid = fact.Hierarchy != nil
			if valid {
				current, checked := hierarchies[fact.Hierarchy]
				if !checked {
					current = StatisticsHierarchyCurrent(fact.Hierarchy, relation, read)
					if hierarchies == nil {
						hierarchies = make(map[*StatisticsHierarchy]bool)
					}
					hierarchies[fact.Hierarchy] = current
				}
				valid = current
			}
		}
		if valid {
			for i := range d.Selection.Len() {
				slot := relation.IndexForAttribute(d.Selection.Key(i))
				if slot < 0 || relation.Columns[slot].Usage != fact.Usages[i] {
					valid = false
					break
				}
			}
		}
		if !valid {
			if deletions == nil {
				deletions = make(map[ExtendedStatisticsKey]*ExtendedStatistics)
			}
			deletions[key] = nil
		}
	}
	return MergeExtendedStatistics(stats, deletions)
}

// StatisticsHierarchyCurrent checks captured closure and stable layouts, not
// descriptor pointers or display names. Both scalar and extended facts use it.
func StatisticsHierarchyCurrent(hierarchy *StatisticsHierarchy, root *catalog.Relation, read catalog.Read) bool {
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
