package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
)

const partitionReplayStem = "__stuntgres_partition_replay_"

type partitionInvalidReplay struct {
	table   *catalog.Relation
	child   *catalog.Relation
	scratch string
}

func partitionIndexPreservedInvalid(view catalog.Read, index *catalog.Relation) bool {
	table, ok := view.Relation(index.SQLIndex.Parent)
	return ok && table.Partition != nil && !index.SQLIndex.Valid && partitionIndexCoverageComplete(view, index)
}

func partitionReplayNameCompare(view catalog.Read, a, b *catalog.Relation) int {
	if order := strings.Compare(view.Get(a.Namespace()).Name(), view.Get(b.Namespace()).Name()); order != 0 {
		return order
	}
	return strings.Compare(a.Name(), b.Name())
}

func partitionPreservedReplayChild(view catalog.Read, table *catalog.Relation) *catalog.Relation {
	var chosen *catalog.Relation
	for _, address := range table.Partition.Children {
		child := view.MustRelation(address)
		if chosen == nil || partitionReplayNameCompare(view, child, chosen) < 0 {
			chosen = child
		}
	}
	return chosen
}

func partitionPreservedReplayBacking(view catalog.Read, index *catalog.Relation) bool {
	if index.SQLIndex.Constraint != nil || index.SQLIndex.PartitionParent == (ids.ObjectAddress{}) {
		return false
	}
	parent := view.MustRelation(index.SQLIndex.PartitionParent)
	if !partitionIndexPreservedInvalid(view, parent) {
		return false
	}
	table := view.MustRelation(parent.SQLIndex.Parent)
	child := partitionPreservedReplayChild(view, table)
	return child != nil && child.Address() == index.SQLIndex.Parent
}

// Unattached local unique providers must not steal a saved attached backing
// during the one table reattachment that preserves complete-invalid validity.
func partitionPreservedReplayLocal(view catalog.Read, index *catalog.Relation) bool {
	if !index.SQLIndex.Unique || index.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
		return false
	}
	table := view.MustRelation(index.SQLIndex.Parent)
	if table.Membership == nil {
		return false
	}
	parent := view.MustRelation(table.Membership.Parent)
	chosen := partitionPreservedReplayChild(view, parent)
	if chosen == nil || chosen.Address() != table.Address() {
		return false
	}
	for _, descriptor := range view.Descriptors() {
		candidate, ok := descriptor.(*catalog.Relation)
		if ok && candidate.SQLIndex != nil && candidate.SQLIndex.Parent == parent.Address() && partitionIndexPreservedInvalid(view, candidate) {
			saved := partitionDumpIndexBacking(view, candidate.Address(), table.Address())
			match, err := partitionIndexEquivalent(context.Background(), view, table, saved.SQLIndex, index.SQLIndex)
			if err == nil && match {
				return true
			}
		}
	}
	return false
}

func partitionReplayUsingIndex(view catalog.Read, index *catalog.Relation) bool {
	table := view.MustRelation(index.SQLIndex.Parent)
	return usingIndexDump(index) && partitionReplayVirtualUniqueRoot(view, index) == nil || table.Partition == nil && index.SQLIndex.Constraint != nil && index.SQLIndex.Constraint.Kind == catalog.UniqueBackingKind && partitionPreservedReplayLocal(view, index)
}

// An index subtree, not all table-local providers/rows, is required before the
// reattachment. Deferred local origins and their data consumers follow it.
func partitionReplayIndexReady(view catalog.Read, graph dump.Graph, event dump.Event, index *catalog.Relation) {
	table := view.MustRelation(index.SQLIndex.Parent)
	if index.SQLIndex.Constraint != nil && !partitionReplayUsingIndex(view, index) {
		graph.Add(event, dump.Event{OID: table.Address().ObjectID})
	} else {
		graph.Add(event, dump.Event{OID: index.Address().ObjectID})
	}
	if table.Membership != nil {
		member := dump.Event{OID: table.Address().ObjectID}
		if partitionDumpNeedsAttach(view, table) {
			member.Phase = dump.TableAttachment
		}
		graph.Add(event, member)
	}
	if table.Partition == nil {
		return
	}
	for _, address := range table.Partition.Children {
		child := partitionDumpIndexBacking(view, index.Address(), address)
		partitionReplayIndexReady(view, graph, event, child)
		if index.SQLIndex.Constraint == nil {
			graph.Add(event, dump.Event{OID: child.Address().ObjectID, Phase: dump.IndexAttachment})
		}
	}
}

// Existing declaration/data/attachment events remain the single replay scheduler.
// Manual complete-invalid trees use one real valid child. ONLY exclusions use
// a scratch gap even with saved children, avoiding non-adoptable EXCLUDE clones.
func partitionInvalidDumpEvents(view catalog.Read, graph dump.Graph, indexIndices map[ids.OID]int) map[ids.OID]partitionInvalidReplay {
	groups := make(map[ids.ObjectAddress][]*catalog.Relation)
	for oid := range indexIndices {
		index := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
		if partitionIndexPreservedInvalid(view, index) {
			groups[index.SQLIndex.Parent] = append(groups[index.SQLIndex.Parent], index)
		}
	}
	if len(groups) == 0 {
		return nil
	}
	var tables []*catalog.Relation
	for address := range groups {
		tables = append(tables, view.MustRelation(address))
	}
	slices.SortFunc(tables, func(a, b *catalog.Relation) int { return partitionReplayNameCompare(view, a, b) })
	reserved := make(map[ids.NameKey]bool, view.NameCount())
	for key := range view.Names() {
		reserved[key] = true
	}
	result := make(map[ids.OID]partitionInvalidReplay, len(tables))
	ordinal := 0
	for _, table := range tables {
		oid := table.Address().ObjectID
		prepare := dump.Event{OID: oid, Phase: dump.InvalidIndexPreparation}
		finish := dump.Event{OID: oid, Phase: dump.InvalidIndexCompletion}
		item := partitionInvalidReplay{table: table, child: partitionPreservedReplayChild(view, table)}
		for _, index := range groups[table.Address()] {
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
				key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: table.Namespace(), Name: name}
				if !reserved[key] {
					reserved[key] = true
					item.scratch = name
					break
				}
			}
			graph[prepare] = []dump.Event{{OID: oid}}
			graph[finish] = []dump.Event{prepare}
			for _, index := range groups[table.Address()] {
				declaration := dump.Event{OID: index.Address().ObjectID}
				graph.Add(declaration, prepare)
				graph.Add(finish, declaration)
			}
			for _, address := range table.Partition.Children {
				child := view.MustRelation(address)
				member := dump.Event{OID: address.ObjectID}
				if partitionDumpNeedsAttach(view, child) {
					member.Phase = dump.TableAttachment
				}
				graph.Add(member, finish)
			}
			for event := range graph {
				if event.Phase == dump.Rows {
					graph.Add(event, finish)
				}
			}
		} else {
			membership := dump.Event{OID: item.child.Address().ObjectID}
			if partitionDumpNeedsAttach(view, item.child) {
				membership.Phase = dump.TableAttachment
			}
			graph[prepare] = []dump.Event{membership}
			graph[finish] = []dump.Event{prepare}
			var previousBacking dump.Event
			for _, index := range partitionIndexDeclarationOrder(view, table) {
				if !partitionIndexPreservedInvalid(view, index) {
					continue
				}
				graph.Add(prepare, dump.Event{OID: index.Address().ObjectID})
				backing := partitionDumpIndexBacking(view, index.Address(), item.child.Address())
				attachment := dump.Event{OID: backing.Address().ObjectID, Phase: dump.IndexAttachment}
				if partitionPreservedReplayBacking(view, backing) {
					declaration := dump.Event{OID: backing.Address().ObjectID}
					graph.Add(declaration, prepare)
					if previousBacking != (dump.Event{}) {
						graph.Add(declaration, previousBacking)
					}
					previousBacking = declaration
					partitionReplayIndexReady(view, graph, finish, backing)
				}
				// Table reattachment, not explicit index completion, must supply
				// this one real final edge to retain the parent's invalid flag.
				delete(graph, attachment)
				for event, predecessors := range graph {
					graph[event] = slices.DeleteFunc(predecessors, func(p dump.Event) bool { return p == attachment })
				}
				for _, address := range table.Partition.Children {
					if address == item.child.Address() {
						continue
					}
					other := partitionDumpIndexBacking(view, index.Address(), address)
					graph.Add(prepare, dump.Event{OID: other.Address().ObjectID, Phase: dump.IndexAttachment})
				}
			}
			// Incomplete manual indexes must appear only after reattachment;
			// otherwise ATTACH would generate unwanted backings for their gaps.
			for indexOID := range indexIndices {
				index := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: indexOID})
				if index.SQLIndex.Parent == table.Address() && !index.SQLIndex.Valid && !partitionIndexPreservedInvalid(view, index) {
					graph.Add(dump.Event{OID: indexOID}, finish)
				}
				if index.SQLIndex.Parent == item.child.Address() && partitionPreservedReplayLocal(view, index) {
					graph.Add(dump.Event{OID: indexOID}, finish)
				}
			}
		}
		graph.Add(dump.Event{OID: oid, Phase: dump.TableCompletion}, finish)
		result[oid] = item
	}
	return result
}

func (r partitionInvalidReplay) sql(ctx context.Context, view catalog.Read, phase dump.Phase) (string, error) {
	qualified := func(d *catalog.Relation) string {
		return catalog.QuoteIdentifier(view.Get(d.Namespace()).Name()) + "." + catalog.QuoteIdentifier(d.Name())
	}
	parent := qualified(r.table)
	if r.child != nil {
		if phase == dump.InvalidIndexPreparation {
			return "ALTER TABLE " + parent + " DETACH PARTITION " + qualified(r.child) + ";\n", nil
		}
		return partitionAttachDumpSQL(ctx, view, r.child)
	}
	child := catalog.QuoteIdentifier(view.Get(r.table.Namespace()).Name()) + "." + catalog.QuoteIdentifier(r.scratch)
	if phase == dump.InvalidIndexPreparation {
		return "CREATE TABLE " + child + " PARTITION OF " + parent + " DEFAULT;\n", nil
	}
	return "ALTER TABLE " + parent + " DETACH PARTITION " + child + ";\nDROP TABLE " + child + ";\n", nil
}
