package partition

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/table"
)

const ReplayStem = "__stuntgres_partition_replay_"

type InvalidReplay struct {
	Table        *catalog.Relation
	Child        *catalog.Relation
	Scratch      string
	Fresh        *FreshReplay
	PromotionGap bool
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
// a scratch gap even with saved children. Fresh-local provenance uses the same
// per-table preparation/completion pair with temporary parent recipes.
func InvalidDumpEvents(r dump.Resolver, view catalog.Read, graph dump.Graph, indexIndices map[ids.OID]int, captured []table.DumpRelation) (map[ids.OID]InvalidReplay, error) {
	groups := make(map[ids.ObjectAddress][]*catalog.Relation)
	for oid := range indexIndices {
		idx := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
		if index.PartitionIndexPreservedInvalid(view, idx) {
			groups[idx.SQLIndex.Parent] = append(groups[idx.SQLIndex.Parent], idx)
		}
	}
	var freshTables []*catalog.Relation
	for i := range captured {
		source := captured[i].Descriptor
		if freshOriginTable(view, source) {
			freshTables = append(freshTables, source)
		}
	}
	if len(groups) == 0 && len(freshTables) == 0 {
		return nil, nil
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
		item := InvalidReplay{Table: table, Child: index.PartitionPreservedReplayChild(view, table)}
		for _, idx := range groups[table.Address()] {
			for _, address := range table.Partition.Children {
				if child := index.PartitionDumpIndexBacking(view, idx.Address(), address); child != nil && index.PartitionReplayCompetingExclusion(view, child) {
					item.PromotionGap = true
					member := dump.Event{OID: address.ObjectID}
					if DumpNeedsAttach(view, view.MustRelation(address)) {
						member.Phase = dump.TableAttachment
					}
					graph.Add(prepare, member)
					graph.Add(finish, dump.Event{OID: child.Address().ObjectID, Phase: dump.IndexAttachment})
					// Finish the coexisting independent names before removing
					// this gap. OID remint must not choose their relative order.
					owner := view.MustRelation(address)
					for i := range owner.Constraints.Exclusion {
						other := &owner.Constraints.Exclusion[i]
						if other.Parent == 0 && !other.NoInherit {
							graph.Add(finish, dump.Event{OID: other.Index.ObjectID, Phase: dump.Rename})
						}
					}
				}
			}
		}
		for _, idx := range groups[table.Address()] {
			if index.PartitionReplayOnlyExclusion(view, idx) {
				// A real scratch gap preserves ONLY invalidity without changing
				// any captured child provider or declaration provenance.
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
			graph.Add(prepare, dump.Event{OID: oid})
			graph.Add(finish, prepare)
			for _, index := range groups[table.Address()] {
				declaration := dump.Event{OID: index.Address().ObjectID}
				graph.Add(declaration, prepare)
				graph.Add(finish, declaration)
			}
			for _, address := range table.Partition.Children {
				child := view.MustRelation(address)
				if item.PromotionGap {
					chosen := false
					for _, idx := range groups[table.Address()] {
						backing := index.PartitionDumpIndexBacking(view, idx.Address(), address)
						chosen = chosen || backing != nil && index.PartitionReplayCompetingExclusion(view, backing)
					}
					if chosen {
						continue
					}
				}
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
	slices.SortFunc(freshTables, func(a, b *catalog.Relation) int { return index.PartitionReplayNameCompare(view, a, b) })
	for _, table := range freshTables {
		stages, err := freshReplayGroups(view, table)
		if err != nil {
			return nil, err
		}
		name := func() string {
			for {
				ordinal++
				value := fmt.Sprintf("%s%d", ReplayStem, ordinal)
				key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: table.Namespace(), Name: value}
				if !reserved[key] {
					reserved[key] = true
					return value
				}
			}
		}
		for constraint := range catalog.ConstraintNames(view, table.Namespace(), table) {
			reserved[ids.NameKey{ClassID: ids.RelationClassID, Namespace: table.Namespace(), Name: constraint}] = true
		}
		var previous dump.Event
		var cleanup []string
		for i := range stages {
			stage := &stages[i]
			anchor := stage.Indexes[0].Address().ObjectID
			prepare := dump.Event{OID: anchor, Phase: dump.InvalidIndexPreparation}
			finish := dump.Event{OID: anchor, Phase: dump.InvalidIndexCompletion}
			if stage.Open {
				stage.Scratch = name()
			} else {
				stage.Scratch = stages[i-1].Scratch
			}
			if stage.Open {
				cleanup = nil
			}
			if freshStageInvalid(view, stage.Indexes[0]) {
				stage.InvalidChild, stage.InvalidGap = name(), name()
				stage.InvalidSeeds = make([]string, len(stage.Indexes))
				stage.Created = make([]string, len(stage.Indexes))
				for j, idx := range stage.Indexes {
					if index.PartitionIndexPreservedInvalid(view, idx) {
						stage.InvalidSeeds[j] = name()
					}
				}
			}
			for _, guard := range stage.Guards {
				stage.GuardSeeds = append(stage.GuardSeeds, name())
				graph.Add(prepare, constraintCreator(r, view, guard))
			}
			graph.Add(prepare, dump.Event{OID: table.Address().ObjectID})
			if previous != (dump.Event{}) {
				graph.Add(prepare, previous)
			}
			graph.Add(finish, prepare)
			for _, key := range stage.Parent.Partition.Keys {
				predecessor, err := r.Predecessor(view, ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: ids.OID(key.Class.OID())}, table.Address())
				if err != nil {
					return nil, err
				}
				if predecessor != 0 {
					graph.Add(prepare, dump.Event{OID: predecessor})
				}
			}
			for _, idx := range stage.Indexes {
				stage.Seeds = append(stage.Seeds, name())
				freshReplayReady(r, view, graph, prepare, idx)
			}
			cleanup = append(cleanup, stage.GuardSeeds...)
			cleanup = append(cleanup, stage.Seeds...)
			if stage.Close {
				stage.CleanupSeeds = cleanup
			}
			if stage.InvalidChild != "" || !stage.Close {
				for _, address := range table.Partition.Children {
					child := view.MustRelation(address)
					member := dump.Event{OID: address.ObjectID}
					if DumpNeedsAttach(view, child) {
						member.Phase = dump.TableAttachment
					}
					graph.Add(member, finish)
				}
			}
			result[anchor] = InvalidReplay{Table: table, Fresh: stage}
			previous = finish
		}
		if table.Membership != nil {
			graph.Add(dump.Event{OID: table.Address().ObjectID, Phase: dump.TableAttachment}, previous)
		}
		graph.Add(dump.Event{OID: table.Address().ObjectID, Phase: dump.TableCompletion}, previous)
		for indexOID := range indexIndices {
			idx := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: indexOID})
			if idx.SQLIndex.Parent == table.Address() && index.PartitionReplayDeferredExclusion(view, idx) {
				graph.Add(dump.Event{OID: indexOID}, previous)
			}
		}
	}
	return result, nil
}

func (r InvalidReplay) SQL(ctx context.Context, view catalog.Read, phase dump.Phase) (string, error) {
	qualified := func(d *catalog.Relation) string {
		return catalog.QuoteIdentifier(view.Get(d.Namespace()).Name()) + "." + catalog.QuoteIdentifier(d.Name())
	}
	parent := qualified(r.Table)
	if r.Child != nil {
		if phase == dump.InvalidIndexPreparation {
			return "ALTER TABLE " + parent + " DETACH PARTITION " + qualified(r.Child) + ";\n", nil
		}
		return AttachDumpSQL(ctx, view, r.Child)
	}
	if r.Scratch == "" {
		return "", nil
	}
	child := catalog.QuoteIdentifier(view.Get(r.Table.Namespace()).Name()) + "." + catalog.QuoteIdentifier(r.Scratch)
	if phase == dump.InvalidIndexPreparation {
		bound := " DEFAULT"
		if r.PromotionGap {
			var err error
			bound, err = ReplayPromotionBoundSQL(r.Table)
			if err != nil {
				return "", err
			}
		}
		return "CREATE TABLE " + child + " PARTITION OF " + parent + bound + ";\n", nil
	}
	return "ALTER TABLE " + parent + " DETACH PARTITION " + child + ";\nDROP TABLE " + child + ";\n", nil
}
