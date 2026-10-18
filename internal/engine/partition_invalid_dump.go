package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/ids"
)

const partitionReplayStem = "__stuntgres_partition_replay_"

type partitionInvalidReplay struct {
	table   *relationDescriptor
	child   *relationDescriptor
	scratch string
}

func partitionIndexPreservedInvalid(view CatalogRead, index *relationDescriptor) bool {
	table, ok := view.Relation(index.SQLIndex.parent)
	return ok && table.Partition != nil && !index.SQLIndex.valid && partitionIndexCoverageComplete(view, index)
}

func partitionReplayNameCompare(view CatalogRead, a, b *relationDescriptor) int {
	if order := strings.Compare(view.Get(a.namespace).Name(), view.Get(b.namespace).Name()); order != 0 {
		return order
	}
	return strings.Compare(a.name, b.name)
}

func partitionPreservedReplayChild(view CatalogRead, table *relationDescriptor) *relationDescriptor {
	var chosen *relationDescriptor
	for _, address := range table.Partition.children {
		child := view.MustRelation(address)
		if chosen == nil || partitionReplayNameCompare(view, child, chosen) < 0 {
			chosen = child
		}
	}
	return chosen
}

func partitionPreservedReplayBacking(view CatalogRead, index *relationDescriptor) bool {
	if index.SQLIndex.constraint != nil || index.SQLIndex.partitionParent == (ids.ObjectAddress{}) {
		return false
	}
	parent := view.MustRelation(index.SQLIndex.partitionParent)
	if !partitionIndexPreservedInvalid(view, parent) {
		return false
	}
	table := view.MustRelation(parent.SQLIndex.parent)
	child := partitionPreservedReplayChild(view, table)
	return child != nil && child.address == index.SQLIndex.parent
}

// Unattached local unique providers must not steal a saved attached backing
// during the one table reattachment that preserves complete-invalid validity.
func partitionPreservedReplayLocal(view CatalogRead, index *relationDescriptor) bool {
	if !index.SQLIndex.unique || index.SQLIndex.partitionParent != (ids.ObjectAddress{}) {
		return false
	}
	table := view.MustRelation(index.SQLIndex.parent)
	if table.Membership == nil {
		return false
	}
	parent := view.MustRelation(table.Membership.parent)
	chosen := partitionPreservedReplayChild(view, parent)
	if chosen == nil || chosen.address != table.address {
		return false
	}
	for _, descriptor := range view.Descriptors() {
		candidate, ok := descriptor.(*relationDescriptor)
		if ok && candidate.SQLIndex != nil && candidate.SQLIndex.parent == parent.address && partitionIndexPreservedInvalid(view, candidate) {
			saved := partitionDumpIndexBacking(view, candidate.address, table.address)
			match, err := partitionIndexEquivalent(context.Background(), view, table, saved.SQLIndex, index.SQLIndex)
			if err == nil && match {
				return true
			}
		}
	}
	return false
}

func partitionReplayUsingIndex(view CatalogRead, index *relationDescriptor) bool {
	table := view.MustRelation(index.SQLIndex.parent)
	return usingIndexDump(index) && partitionReplayVirtualUniqueRoot(view, index) == nil || table.Partition == nil && index.SQLIndex.constraint != nil && index.SQLIndex.constraint.kind == uniqueBackingKind && partitionPreservedReplayLocal(view, index)
}

// An index subtree, not all table-local providers/rows, is required before the
// reattachment. Deferred local origins and their data consumers follow it.
func partitionReplayIndexReady(view CatalogRead, graph map[dumpEventKey][]dumpEventKey, event dumpEventKey, index *relationDescriptor) {
	table := view.MustRelation(index.SQLIndex.parent)
	if index.SQLIndex.constraint != nil && !partitionReplayUsingIndex(view, index) {
		addDumpDependency(graph, event, dumpEventKey{oid: table.address.ObjectID})
	} else {
		addDumpDependency(graph, event, dumpEventKey{oid: index.address.ObjectID})
	}
	if table.Membership != nil {
		member := dumpEventKey{oid: table.address.ObjectID}
		if partitionDumpNeedsAttach(view, table) {
			member.phase = dumpTableAttachment
		}
		addDumpDependency(graph, event, member)
	}
	if table.Partition == nil {
		return
	}
	for _, address := range table.Partition.children {
		child := partitionDumpIndexBacking(view, index.address, address)
		partitionReplayIndexReady(view, graph, event, child)
		if index.SQLIndex.constraint == nil {
			addDumpDependency(graph, event, dumpEventKey{oid: child.address.ObjectID, phase: dumpIndexAttachment})
		}
	}
}

// Existing declaration/data/attachment events remain the single replay scheduler.
// Manual complete-invalid trees use one real valid child. ONLY exclusions use
// a scratch gap even with saved children, avoiding non-adoptable EXCLUDE clones.
func partitionInvalidDumpEvents(view CatalogRead, graph map[dumpEventKey][]dumpEventKey, indexIndices map[ids.OID]int) map[ids.OID]partitionInvalidReplay {
	groups := make(map[ids.ObjectAddress][]*relationDescriptor)
	for oid := range indexIndices {
		index := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
		if partitionIndexPreservedInvalid(view, index) {
			groups[index.SQLIndex.parent] = append(groups[index.SQLIndex.parent], index)
		}
	}
	if len(groups) == 0 {
		return nil
	}
	var tables []*relationDescriptor
	for address := range groups {
		tables = append(tables, view.MustRelation(address))
	}
	slices.SortFunc(tables, func(a, b *relationDescriptor) int { return partitionReplayNameCompare(view, a, b) })
	reserved := make(map[ids.NameKey]bool, view.NameCount())
	for key := range view.Names() {
		reserved[key] = true
	}
	result := make(map[ids.OID]partitionInvalidReplay, len(tables))
	ordinal := 0
	for _, table := range tables {
		oid := table.address.ObjectID
		prepare := dumpEventKey{oid: oid, phase: dumpInvalidIndexPreparation}
		finish := dumpEventKey{oid: oid, phase: dumpInvalidIndexCompletion}
		item := partitionInvalidReplay{table: table, child: partitionPreservedReplayChild(view, table)}
		for _, index := range groups[table.address] {
			if partitionReplayOnlyExclusion(index) {
				// Never detach a captured EXCLUDE child: reattachment clones
				// fresh providers instead of adopting its independent locals.
				item.child = nil
				break
			}
		}
		if item.child == nil {
			for {
				ordinal++
				name := fmt.Sprintf("%s%d", partitionReplayStem, ordinal)
				key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: table.namespace, Name: name}
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
			for _, address := range table.Partition.children {
				child := view.MustRelation(address)
				member := dumpEventKey{oid: address.ObjectID}
				if partitionDumpNeedsAttach(view, child) {
					member.phase = dumpTableAttachment
				}
				addDumpDependency(graph, member, finish)
			}
			for event := range graph {
				if event.phase == dumpRows {
					addDumpDependency(graph, event, finish)
				}
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
				for _, address := range table.Partition.children {
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
				index := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: indexOID})
				if index.SQLIndex.parent == table.address && !index.SQLIndex.valid && !partitionIndexPreservedInvalid(view, index) {
					addDumpDependency(graph, dumpEventKey{oid: indexOID}, finish)
				}
				if index.SQLIndex.parent == item.child.address && partitionPreservedReplayLocal(view, index) {
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
		return quoteIdentifier(view.Get(d.namespace).Name()) + "." + quoteIdentifier(d.name)
	}
	parent := qualified(r.table)
	if r.child != nil {
		if phase == dumpInvalidIndexPreparation {
			return "ALTER TABLE " + parent + " DETACH PARTITION " + qualified(r.child) + ";\n", nil
		}
		return partitionAttachDumpSQL(ctx, view, r.child)
	}
	child := quoteIdentifier(view.Get(r.table.namespace).Name()) + "." + quoteIdentifier(r.scratch)
	if phase == dumpInvalidIndexPreparation {
		return "CREATE TABLE " + child + " PARTITION OF " + parent + " DEFAULT;\n", nil
	}
	return "ALTER TABLE " + parent + " DETACH PARTITION " + child + ";\nDROP TABLE " + child + ";\n", nil
}
