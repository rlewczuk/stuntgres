package engine

import (
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/parser"
)

// CheckReplayTarget reports whether d is the plain partition leaf, current
// in view, that a Restore CHECK replay may target.
func CheckReplayTarget(view catalog.Read, d *catalog.Relation) bool {
	return d != nil && d.RelationKind == catalog.TableRelation && d.Partition == nil && d.Membership != nil && d.Inheritance == nil && view.Get(d.Address()) == d
}

// CaptureUniqueReplay captures the leased target of a Restore replay of the
// partitioned UNIQUE raw (one ADD UNIQUE of a partition-key constraint) on
// table, read in view: the preserved invalid parent index it rebuilds and
// the subtree it covers. Nil when raw is no such replay.
func CaptureUniqueReplay(view catalog.Read, raw *parser.AlterTable, table *catalog.Relation) (*partition.UniqueReplay, error) {
	if table.Partition == nil || table.Membership == nil || table.Inheritance != nil {
		return nil, nil
	}
	definition := raw.Actions[0].Unique
	bound, err := analyze.BindUniqueAttributes(definition, nil, table.Index, table.Attributes)
	if err != nil {
		return nil, err
	}
	parent := view.MustRelation(table.Membership.Parent)
	chosen := index.PartitionPreservedReplayChild(view, parent)
	if chosen == nil || chosen.Address() != table.Address() {
		return nil, nil
	}
	var selected *catalog.Relation
	for _, descriptor := range view.Descriptors() {
		idx, ok := descriptor.(*catalog.Relation)
		if !ok || idx.SQLIndex == nil || idx.SQLIndex.Parent != parent.Address() || !index.PartitionIndexPreservedInvalid(view, idx) {
			continue
		}
		child := index.PartitionDumpIndexBacking(view, idx.Address(), table.Address())
		if child == nil || child.SQLIndex.Constraint != nil || child.SQLIndex.Predicate != nil || len(child.SQLIndex.Include) != 0 || child.SQLIndex.NullsNotDistinct != bound.NullsNotDistinct || len(child.SQLIndex.Keys) != len(bound.Attributes) {
			continue
		}
		match := true
		for i, key := range child.SQLIndex.Keys {
			match = match && key.Derived == nil && key.Attribute == bound.Attributes[i]
		}
		if match {
			selected = idx
			break
		}
	}
	if selected == nil {
		return nil, nil
	}
	captured := &partition.UniqueReplay{Target: table, Syntax: raw, Definition: definition, Snapshot: *definition, Descriptors: []*catalog.Relation{parent, selected}}
	captured.Snapshot.Columns = slices.Clone(definition.Columns)
	tables := make(map[ids.ObjectAddress]bool)
	var capture func(*catalog.Relation) bool
	capture = func(node *catalog.Relation) bool {
		if node.Inheritance != nil {
			return false
		}
		if definition.Primary {
			for _, column := range definition.Columns {
				slot, ok := node.Index[column.Text]
				if !ok || node.Columns[slot].Nullable {
					return false
				}
			}
		}
		tables[node.Address()] = true
		captured.Descriptors = append(captured.Descriptors, node)
		if node.Partition != nil {
			for _, address := range node.Partition.Children {
				if !capture(view.MustRelation(address)) {
					return false
				}
			}
		}
		return true
	}
	if !capture(table) {
		return nil, nil
	}
	for _, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if ok && index.SQLIndex != nil && tables[index.SQLIndex.Parent] {
			captured.Descriptors = append(captured.Descriptors, index)
		}
	}
	return captured, nil
}

// uniqueReplayMatches reports whether raw on table is still exactly the
// captured replay r: the same syntax, an unchanged definition and every
// captured descriptor current in view.
func uniqueReplayMatches(r *partition.UniqueReplay, view catalog.Read, raw *parser.AlterTable, table *catalog.Relation) bool {
	if r.Target != table || r.Syntax != raw || raw.Only || raw.IfExists || len(raw.Actions) != 1 || raw.Actions[0].Action != parser.AddUnique || raw.Actions[0].Unique != r.Definition {
		return false
	}
	current := r.Definition
	saved := r.Snapshot
	if current.Name != saved.Name || current.Primary != saved.Primary || current.NullsNotDistinct != saved.NullsNotDistinct || current.Pos != saved.Pos || !slices.Equal(current.Columns, saved.Columns) {
		return false
	}
	for _, descriptor := range r.Descriptors {
		if view.Get(descriptor.Address()) != descriptor {
			return false
		}
	}
	return true
}

// CaptureUsingIndexReplay captures the leased target of a Restore replay of
// ADD ... USING INDEX raw on the plain partition leaf table, read in view:
// the existing unconstrained, non-partition index it names. Nil when raw is
// no such replay.
func CaptureUsingIndexReplay(view catalog.Read, raw *parser.AlterTable, table *catalog.Relation) *partition.UsingIndexReplay {
	if table.RelationKind != catalog.TableRelation || table.Partition != nil || table.Membership == nil || table.Inheritance != nil {
		return nil
	}
	address := view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: table.Namespace(), Name: raw.Actions[0].UsingIndex.Index.Text})
	index, ok := view.Relation(address)
	if ok && index.SQLIndex != nil && index.SQLIndex.Parent == table.Address() && index.SQLIndex.PartitionParent == (ids.ObjectAddress{}) && index.SQLIndex.Constraint == nil {
		return &partition.UsingIndexReplay{Target: table, Index: index, Syntax: raw}
	}
	return nil
}

// usingIndexReplayMatches reports whether raw on table is still exactly the
// captured replay r, with the target and index current in view.
func usingIndexReplayMatches(r *partition.UsingIndexReplay, view catalog.Read, raw *parser.AlterTable, table *catalog.Relation) bool {
	if r.Index == nil {
		return false
	}
	return r.Syntax == raw && r.Target == table && table != nil && table.RelationKind == catalog.TableRelation && table.Partition == nil && table.Membership != nil && table.Inheritance == nil && view.Get(table.Address()) == table && view.Get(r.Index.Address()) == r.Index && len(raw.Actions) == 1 && raw.Actions[0].Action == parser.AddUsingIndex && raw.Actions[0].UsingIndex != nil && raw.Actions[0].UsingIndex.Index.Text == r.Index.Name()
}
