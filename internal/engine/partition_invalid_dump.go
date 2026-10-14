package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

const partitionReplayStem = "__stuntgres_partition_replay_"

type partitionInvalidReplay struct {
	table   *relationDescriptor
	child   *relationDescriptor
	scratch string
}

func partitionIndexPreservedInvalid(view CatalogRead, index *relationDescriptor) bool {
	table, ok := view.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
	return ok && table.partition != nil && !index.sqlIndex.valid && partitionIndexCoverageComplete(view, index)
}

func partitionReplayNameCompare(view CatalogRead, a, b *relationDescriptor) int {
	if order := strings.Compare(view.state.descriptors[a.namespace].Name(), view.state.descriptors[b.namespace].Name()); order != 0 {
		return order
	}
	return strings.Compare(a.name, b.name)
}

func partitionPreservedReplayChild(view CatalogRead, table *relationDescriptor) *relationDescriptor {
	var chosen *relationDescriptor
	for _, address := range table.partition.children {
		child := view.state.descriptors[address].(*relationDescriptor)
		if chosen == nil || partitionReplayNameCompare(view, child, chosen) < 0 {
			chosen = child
		}
	}
	return chosen
}

func partitionPreservedReplayBacking(view CatalogRead, index *relationDescriptor) bool {
	if index.sqlIndex.constraint != nil || index.sqlIndex.partitionParent == (ObjectAddress{}) {
		return false
	}
	parent := view.state.descriptors[index.sqlIndex.partitionParent].(*relationDescriptor)
	if !partitionIndexPreservedInvalid(view, parent) {
		return false
	}
	table := view.state.descriptors[parent.sqlIndex.parent].(*relationDescriptor)
	child := partitionPreservedReplayChild(view, table)
	return child != nil && child.address == index.sqlIndex.parent
}

// Unattached local unique providers must not steal a saved attached backing
// during the one table reattachment that preserves complete-invalid validity.
func partitionPreservedReplayLocal(view CatalogRead, index *relationDescriptor) bool {
	if !index.sqlIndex.unique || index.sqlIndex.partitionParent != (ObjectAddress{}) {
		return false
	}
	table := view.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
	if table.membership == nil {
		return false
	}
	parent := view.state.descriptors[table.membership.parent].(*relationDescriptor)
	chosen := partitionPreservedReplayChild(view, parent)
	if chosen == nil || chosen.address != table.address {
		return false
	}
	for _, descriptor := range view.state.descriptors {
		candidate, ok := descriptor.(*relationDescriptor)
		if ok && candidate.sqlIndex != nil && candidate.sqlIndex.parent == parent.address && partitionIndexPreservedInvalid(view, candidate) {
			saved := partitionDumpIndexBacking(view, candidate.address, table.address)
			match, err := partitionIndexEquivalent(context.Background(), view, table, saved.sqlIndex, index.sqlIndex)
			if err == nil && match {
				return true
			}
		}
	}
	return false
}

func partitionReplayUsingIndex(view CatalogRead, index *relationDescriptor) bool {
	table := view.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
	return usingIndexDump(index) && partitionReplayVirtualUniqueRoot(view, index) == nil || table.partition == nil && index.sqlIndex.constraint != nil && index.sqlIndex.constraint.kind == uniqueBackingKind && partitionPreservedReplayLocal(view, index)
}

// An index subtree, not all table-local providers/rows, is required before the
// reattachment. Deferred local origins and their data consumers follow it.
func partitionReplayIndexReady(view CatalogRead, graph map[dumpEventKey][]dumpEventKey, event dumpEventKey, index *relationDescriptor) {
	table := view.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
	if index.sqlIndex.constraint != nil && !partitionReplayUsingIndex(view, index) {
		addDumpDependency(graph, event, dumpEventKey{oid: table.address.ObjectID})
	} else {
		addDumpDependency(graph, event, dumpEventKey{oid: index.address.ObjectID})
	}
	if table.membership != nil {
		member := dumpEventKey{oid: table.address.ObjectID}
		if partitionDumpNeedsAttach(view, table) {
			member.phase = dumpTableAttachment
		}
		addDumpDependency(graph, event, member)
	}
	if table.partition == nil {
		return
	}
	for _, address := range table.partition.children {
		child := partitionDumpIndexBacking(view, index.address, address)
		partitionReplayIndexReady(view, graph, event, child)
		if index.sqlIndex.constraint == nil {
			addDumpDependency(graph, event, dumpEventKey{oid: child.address.ObjectID, phase: dumpIndexAttachment})
		}
	}
}

// Existing declaration/data/attachment events remain the single replay scheduler.
// Complete-invalid tables are restored through one deterministic real valid child;
// empty tables alone need an empty, collision-reserved DEFAULT removed afterward.
func partitionInvalidDumpEvents(view CatalogRead, graph map[dumpEventKey][]dumpEventKey, indexIndices map[OID]int) map[OID]partitionInvalidReplay {
	groups := make(map[ObjectAddress][]*relationDescriptor)
	for oid := range indexIndices {
		index := view.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: oid}].(*relationDescriptor)
		if partitionIndexPreservedInvalid(view, index) {
			groups[index.sqlIndex.parent] = append(groups[index.sqlIndex.parent], index)
		}
	}
	if len(groups) == 0 {
		return nil
	}
	var tables []*relationDescriptor
	for address := range groups {
		tables = append(tables, view.state.descriptors[address].(*relationDescriptor))
	}
	slices.SortFunc(tables, func(a, b *relationDescriptor) int { return partitionReplayNameCompare(view, a, b) })
	reserved := make(map[NameKey]bool, len(view.state.names))
	for key := range view.state.names {
		reserved[key] = true
	}
	result := make(map[OID]partitionInvalidReplay, len(tables))
	ordinal := 0
	for _, table := range tables {
		oid := table.address.ObjectID
		prepare := dumpEventKey{oid: oid, phase: dumpInvalidIndexPreparation}
		finish := dumpEventKey{oid: oid, phase: dumpInvalidIndexCompletion}
		item := partitionInvalidReplay{table: table, child: partitionPreservedReplayChild(view, table)}
		if item.child == nil {
			for {
				ordinal++
				name := fmt.Sprintf("%s%d", partitionReplayStem, ordinal)
				key := NameKey{ClassID: RelationClassID, Namespace: table.namespace, Name: name}
				if !reserved[key] {
					reserved[key] = true
					item.scratch = name
					break
				}
			}
			graph[prepare] = []dumpEventKey{{oid: oid}}
			graph[finish] = []dumpEventKey{prepare}
			for _, index := range groups[table.address] {
				declaration := dumpEventKey{oid: index.address.ObjectID}
				addDumpDependency(graph, declaration, prepare)
				addDumpDependency(graph, finish, declaration)
			}
		} else {
			membership := dumpEventKey{oid: item.child.address.ObjectID}
			if partitionDumpNeedsAttach(view, item.child) {
				membership.phase = dumpTableAttachment
			}
			graph[prepare] = []dumpEventKey{membership}
			graph[finish] = []dumpEventKey{prepare}
			var previousBacking dumpEventKey
			for _, index := range partitionIndexDeclarationOrder(view, table) {
				if !partitionIndexPreservedInvalid(view, index) {
					continue
				}
				addDumpDependency(graph, prepare, dumpEventKey{oid: index.address.ObjectID})
				backing := partitionDumpIndexBacking(view, index.address, item.child.address)
				attachment := dumpEventKey{oid: backing.address.ObjectID, phase: dumpIndexAttachment}
				if partitionPreservedReplayBacking(view, backing) {
					declaration := dumpEventKey{oid: backing.address.ObjectID}
					addDumpDependency(graph, declaration, prepare)
					if previousBacking != (dumpEventKey{}) {
						addDumpDependency(graph, declaration, previousBacking)
					}
					previousBacking = declaration
					partitionReplayIndexReady(view, graph, finish, backing)
				}
				// Table reattachment, not explicit index completion, must supply
				// this one real final edge to retain the parent's invalid flag.
				delete(graph, attachment)
				for event, predecessors := range graph {
					graph[event] = slices.DeleteFunc(predecessors, func(p dumpEventKey) bool { return p == attachment })
				}
				for _, address := range table.partition.children {
					if address == item.child.address {
						continue
					}
					other := partitionDumpIndexBacking(view, index.address, address)
					addDumpDependency(graph, prepare, dumpEventKey{oid: other.address.ObjectID, phase: dumpIndexAttachment})
				}
			}
			// Incomplete manual indexes must appear only after reattachment;
			// otherwise ATTACH would generate unwanted backings for their gaps.
			for indexOID := range indexIndices {
				index := view.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: indexOID}].(*relationDescriptor)
				if index.sqlIndex.parent == table.address && !index.sqlIndex.valid && !partitionIndexPreservedInvalid(view, index) {
					addDumpDependency(graph, dumpEventKey{oid: indexOID}, finish)
				}
				if index.sqlIndex.parent == item.child.address && partitionPreservedReplayLocal(view, index) {
					addDumpDependency(graph, dumpEventKey{oid: indexOID}, finish)
				}
			}
		}
		addDumpDependency(graph, dumpEventKey{oid: oid, phase: dumpTableCompletion}, finish)
		result[oid] = item
	}
	return result
}

func (r partitionInvalidReplay) sql(ctx context.Context, view CatalogRead, phase dumpPhase) (string, error) {
	qualified := func(d *relationDescriptor) string {
		return quoteIdentifier(view.state.descriptors[d.namespace].Name()) + "." + quoteIdentifier(d.name)
	}
	parent := qualified(r.table)
	if r.child != nil {
		if phase == dumpInvalidIndexPreparation {
			return "ALTER TABLE " + parent + " DETACH PARTITION " + qualified(r.child) + ";\n", nil
		}
		return partitionAttachDumpSQL(ctx, view, r.child)
	}
	child := quoteIdentifier(view.state.descriptors[r.table.namespace].Name()) + "." + quoteIdentifier(r.scratch)
	if phase == dumpInvalidIndexPreparation {
		return "CREATE TABLE " + child + " PARTITION OF " + parent + " DEFAULT;\n", nil
	}
	return "ALTER TABLE " + parent + " DETACH PARTITION " + child + ";\nDROP TABLE " + child + ";\n", nil
}
