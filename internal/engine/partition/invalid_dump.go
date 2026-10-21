package partition

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

const ReplayStem = "__stuntgres_partition_replay_"

type InvalidReplay struct {
	table   *catalog.Relation
	Child   *catalog.Relation
	Scratch string
}

func preservedReplayBacking(view catalog.Read, idx *catalog.Relation) bool {
	if idx.SQLIndex.Constraint != nil || idx.SQLIndex.PartitionParent == (ids.ObjectAddress{}) {
		return false
	}
	parent := view.MustRelation(idx.SQLIndex.PartitionParent)
	if !index.PartitionIndexPreservedInvalid(view, parent) {
		return false
	}
	table := view.MustRelation(parent.SQLIndex.Parent)
	child := index.PartitionPreservedReplayChild(view, table)
	return child != nil && child.Address() == idx.SQLIndex.Parent
}

// An index subtree, not all table-local providers/rows, is required before the
// reattachment. Deferred local origins and their data consumers follow it.
func replayIndexReady(view catalog.Read, graph dump.Graph, event dump.Event, idx *catalog.Relation) {
	table := view.MustRelation(idx.SQLIndex.Parent)
	if idx.SQLIndex.Constraint != nil && !index.PartitionReplayUsingIndex(view, idx) {
		graph.Add(event, dump.Event{OID: table.Address().ObjectID})
	} else {
		graph.Add(event, dump.Event{OID: idx.Address().ObjectID})
	}
	if table.Membership != nil {
		member := dump.Event{OID: table.Address().ObjectID}
		if DumpNeedsAttach(view, table) {
			member.Phase = dump.TableAttachment
		}
		graph.Add(event, member)
	}
	if table.Partition == nil {
		return
	}
	for _, address := range table.Partition.Children {
		child := index.PartitionDumpIndexBacking(view, idx.Address(), address)
		replayIndexReady(view, graph, event, child)
		if idx.SQLIndex.Constraint == nil {
			graph.Add(event, dump.Event{OID: child.Address().ObjectID, Phase: dump.IndexAttachment})
		}
	}
}

// Existing declaration/data/attachment events remain the single replay scheduler.
// Manual complete-invalid trees use one real valid child. ONLY exclusions use
// a scratch gap even with saved children, avoiding non-adoptable EXCLUDE clones.
func InvalidDumpEvents(view catalog.Read, graph dump.Graph, indexIndices map[ids.OID]int) map[ids.OID]InvalidReplay {
	groups := make(map[ids.ObjectAddress][]*catalog.Relation)
	for oid := range indexIndices {
		idx := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
		if index.PartitionIndexPreservedInvalid(view, idx) {
			groups[idx.SQLIndex.Parent] = append(groups[idx.SQLIndex.Parent], idx)
		}
	}
	if len(groups) == 0 {
		return nil
	}
	var tables []*catalog.Relation
	for address := range groups {
		tables = append(tables, view.MustRelation(address))
	}
	slices.SortFunc(tables, func(a, b *catalog.Relation) int { return index.PartitionReplayNameCompare(view, a, b) })
	reserved := make(map[ids.NameKey]bool, view.NameCount())
	for key := range view.Names() {
		reserved[key] = true
	}
	result := make(map[ids.OID]InvalidReplay, len(tables))
	ordinal := 0
	for _, table := range tables {
		oid := table.Address().ObjectID
		prepare := dump.Event{OID: oid, Phase: dump.InvalidIndexPreparation}
		finish := dump.Event{OID: oid, Phase: dump.InvalidIndexCompletion}
		item := InvalidReplay{table: table, Child: index.PartitionPreservedReplayChild(view, table)}
		for _, idx := range groups[table.Address()] {
			if index.PartitionReplayOnlyExclusion(idx) {
				// Never detach a captured EXCLUDE child: reattachment clones
				// fresh providers instead of adopting its independent locals.
				item.Child = nil
				break
			}
		}
		if item.Child == nil {
			for {
				ordinal++
				name := fmt.Sprintf("%s%d", ReplayStem, ordinal)
				key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: table.Namespace(), Name: name}
				if !reserved[key] {
					reserved[key] = true
					item.Scratch = name
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
				if DumpNeedsAttach(view, child) {
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
			membership := dump.Event{OID: item.Child.Address().ObjectID}
			if DumpNeedsAttach(view, item.Child) {
				membership.Phase = dump.TableAttachment
			}
			graph[prepare] = []dump.Event{membership}
			graph[finish] = []dump.Event{prepare}
			var previousBacking dump.Event
			for _, idx := range index.PartitionIndexDeclarationOrder(view, table) {
				if !index.PartitionIndexPreservedInvalid(view, idx) {
					continue
				}
				graph.Add(prepare, dump.Event{OID: idx.Address().ObjectID})
				backing := index.PartitionDumpIndexBacking(view, idx.Address(), item.Child.Address())
				attachment := dump.Event{OID: backing.Address().ObjectID, Phase: dump.IndexAttachment}
				if preservedReplayBacking(view, backing) {
					declaration := dump.Event{OID: backing.Address().ObjectID}
					graph.Add(declaration, prepare)
					if previousBacking != (dump.Event{}) {
						graph.Add(declaration, previousBacking)
					}
					previousBacking = declaration
					replayIndexReady(view, graph, finish, backing)
				}
				// Table reattachment, not explicit index completion, must supply
				// this one real final edge to retain the parent's invalid flag.
				delete(graph, attachment)
				for event, predecessors := range graph {
					graph[event] = slices.DeleteFunc(predecessors, func(p dump.Event) bool { return p == attachment })
				}
				for _, address := range table.Partition.Children {
					if address == item.Child.Address() {
						continue
					}
					other := index.PartitionDumpIndexBacking(view, idx.Address(), address)
					graph.Add(prepare, dump.Event{OID: other.Address().ObjectID, Phase: dump.IndexAttachment})
				}
			}
			// Incomplete manual indexes must appear only after reattachment;
			// otherwise ATTACH would generate unwanted backings for their gaps.
			for indexOID := range indexIndices {
				idx := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: indexOID})
				if idx.SQLIndex.Parent == table.Address() && !idx.SQLIndex.Valid && !index.PartitionIndexPreservedInvalid(view, idx) {
					graph.Add(dump.Event{OID: indexOID}, finish)
				}
				if idx.SQLIndex.Parent == item.Child.Address() && index.PartitionPreservedReplayLocal(view, idx) {
					graph.Add(dump.Event{OID: indexOID}, finish)
				}
			}
		}
		graph.Add(dump.Event{OID: oid, Phase: dump.TableCompletion}, finish)
		result[oid] = item
	}
	return result
}

func (r InvalidReplay) SQL(ctx context.Context, view catalog.Read, phase dump.Phase) (string, error) {
	qualified := func(d *catalog.Relation) string {
		return catalog.QuoteIdentifier(view.Get(d.Namespace()).Name()) + "." + catalog.QuoteIdentifier(d.Name())
	}
	parent := qualified(r.table)
	if r.Child != nil {
		if phase == dump.InvalidIndexPreparation {
			return "ALTER TABLE " + parent + " DETACH PARTITION " + qualified(r.Child) + ";\n", nil
		}
		return AttachDumpSQL(ctx, view, r.Child)
	}
	child := catalog.QuoteIdentifier(view.Get(r.table.Namespace()).Name()) + "." + catalog.QuoteIdentifier(r.Scratch)
	if phase == dump.InvalidIndexPreparation {
		return "CREATE TABLE " + child + " PARTITION OF " + parent + " DEFAULT;\n", nil
	}
	return "ALTER TABLE " + parent + " DETACH PARTITION " + child + ";\nDROP TABLE " + child + ";\n", nil
}
