package engine

import (
	"context"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
)

func partitionDumpIndexBacking(view catalog.Read, index, table ids.ObjectAddress) *catalog.Relation {
	for _, descriptor := range view.Descriptors() {
		candidate, ok := descriptor.(*catalog.Relation)
		if ok && candidate.SQLIndex != nil && candidate.SQLIndex.Parent == table && candidate.SQLIndex.PartitionParent == index {
			return candidate
		}
	}
	return nil
}

// A preexisting incomplete manual index must not be present while ATTACH could
// synthesize a missing child or replace a captured invalid child. Delay all
// manual virtual indexes on that table, including the intended valid tree, so
// physical backings cannot be consumed by the wrong equivalent parent index.
func partitionDumpLateIndexTables(view catalog.Read) map[ids.ObjectAddress]bool {
	late := make(map[ids.ObjectAddress]bool)
	for _, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Valid || index.SQLIndex.Constraint != nil {
			continue
		}
		table := view.MustRelation(index.SQLIndex.Parent)
		if table.Partition == nil || late[table.Address()] {
			continue
		}
		if partitionIndexPreservedInvalid(view, index) {
			late[table.Address()] = true
			continue
		}
		for _, child := range table.Partition.Children {
			candidate := view.MustRelation(child)
			if !partitionDumpNeedsAttach(view, candidate) {
				continue
			}
			backing := partitionDumpIndexBacking(view, index.Address(), child)
			if backing == nil || !backing.SQLIndex.Valid {
				late[table.Address()] = true
				break
			}
		}
	}
	return late
}

// Extend the existing declaration graph in place. The same source-priority
// scheduler owns declarations, names, physical data, constraint origins and
// captured membership/index edges; there is no independent topology sorter.
func partitionDumpEvents(ctx context.Context, view catalog.Read, graph dump.Graph, declarations []dump.Event, tables []dumpRelation, indexIndices map[ids.OID]int, replay *partitionConstraintReplay, deferDefaults bool) error {
	lateTables := partitionDumpLateIndexTables(view)
	lateIndexes := make(map[ids.OID]bool)
	for oid := range indexIndices {
		index := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
		lateIndexes[oid] = index.SQLIndex.Constraint == nil && lateTables[index.SQLIndex.Parent] || partitionPreservedReplayBacking(view, index) || partitionPreservedReplayLocal(view, index)
		if root := partitionReplayVirtualUniqueRoot(view, index); root != nil && !partitionReplayOnlyExclusion(root) {
			partitionVirtualUniqueMembershipReady(view, graph, dump.Event{OID: oid}, view.MustRelation(index.SQLIndex.Parent))
		}
	}
	membership := func(table *catalog.Relation) dump.Event {
		if partitionDumpNeedsAttach(view, table) {
			return dump.Event{OID: table.Address().ObjectID, Phase: dump.TableAttachment}
		}
		return dump.Event{OID: table.Address().ObjectID}
	}
	for _, t := range tables {
		d := t.descriptor
		event := membership(d)
		if event.Phase != dump.TableAttachment {
			continue
		}
		graph[event] = []dump.Event{{OID: d.Address().ObjectID}, {OID: d.Membership.Parent.ObjectID}}
		if d.Partition != nil {
			for _, child := range d.Partition.Children {
				graph.Add(event, membership(view.MustRelation(child)))
			}
		}
		if d.Membership.DefaultBound {
			parent := view.MustRelation(d.Membership.Parent)
			for _, child := range parent.Partition.Children {
				if child != d.Address() && !partitionExclusionDefaultBefore(view, d, view.MustRelation(child)) {
					graph.Add(event, membership(view.MustRelation(child)))
				}
			}
		}
	}
	for oid := range indexIndices {
		if !lateIndexes[oid] {
			continue
		}
		index := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
		table := view.MustRelation(index.SQLIndex.Parent)
		if table.Partition == nil {
			continue
		}
		for _, child := range table.Partition.Children {
			backing := partitionDumpIndexBacking(view, index.Address(), child)
			if index.SQLIndex.Valid || partitionIndexPreservedInvalid(view, index) || backing == nil || !backing.SQLIndex.Valid {
				graph.Add(dump.Event{OID: oid}, membership(view.MustRelation(child)))
			}
		}
	}
	lateDeclarations := make(map[ids.OID]bool)
	for _, event := range declarations {
		lateDeclarations[event.OID] = lateIndexes[event.OID]
		if _, exists := indexIndices[event.OID]; exists {
			index := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: event.OID})
			if partitionReplayOnlyExclusion(index) {
				lateDeclarations[event.OID] = true
			}
		}
		if descriptor, ok := view.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: event.OID}); ok && descriptor.Membership != nil && !partitionDumpNeedsAttach(view, descriptor) {
			parent := view.MustRelation(descriptor.Membership.Parent)
			for _, index := range partitionIndexDeclarationOrder(view, parent) {
				if root := partitionReplayVirtualUniqueRoot(view, index); root != nil && partitionReplayOnlyExclusion(root) && partitionDumpIndexBacking(view, index.Address(), descriptor.Address()) != nil {
					lateDeclarations[event.OID] = true
				}
			}
		}
		for _, predecessor := range graph[event] {
			if predecessor.Phase == dump.Declaration {
				lateDeclarations[event.OID] = lateDeclarations[event.OID] || lateDeclarations[predecessor.OID]
			}
		}
	}
	virtualNameReady := make(map[ids.ObjectAddress]dump.Event)
	nameReady := func(address ids.ObjectAddress) (dump.Event, bool, error) {
		address.SubID = 0
		if _, exists := replay.temporary[address]; exists {
			index := view.MustRelation(address)
			if root := partitionReplayVirtualUniqueRoot(view, index); root != nil && !partitionReplayOnlyExclusion(root) {
				return virtualNameReady[root.Address()], true, nil
			}
			return dump.Event{OID: address.ObjectID, Phase: dump.Rename}, true, nil
		}
		oid, err := dumpPredecessor(view, address, ids.ObjectAddress{})
		return dump.Event{OID: oid}, oid != 0, err
	}
	lateNames := make(map[ids.OID]bool)

	// Retain the existing nonoperator definition chain. Operator declarations
	// interleave through real prerequisites: chaining an unrelated type after
	// an operator could make it wait for links whose endpoint needs that type.
	var earlyTail dump.Event
	for _, event := range declarations {
		if event.Phase != dump.Declaration {
			continue
		}
		if _, operator := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: event.OID}); operator {
			continue
		}
		if lateDeclarations[event.OID] {
			continue
		}
		if earlyTail != (dump.Event{}) {
			graph.Add(event, earlyTail)
		}
		earlyTail = event
	}
	if earlyTail == (dump.Event{}) {
		return nil
	}
	ready := earlyTail
	seen := make(map[dump.Event]bool)
	renameOrder := replay.finalRenameOrder()
	for _, index := range renameOrder {
		provider, err := dumpPredecessor(view, index.Address(), ids.ObjectAddress{})
		if err != nil {
			return err
		}
		event := dump.Event{OID: index.Address().ObjectID, Phase: dump.Rename}
		creator := partitionConstraintCreator(view, index)
		graph[event] = []dump.Event{earlyTail, creator}
		lateNames[index.Address().ObjectID] = lateDeclarations[provider] || creator.Phase == dump.TableAttachment
		if root := partitionReplayVirtualUniqueRoot(view, index); root != nil && !partitionReplayOnlyExclusion(root) {
			// A definition creates the whole local tree. Complete its terminal
			// names in captured logical order before any original-name consumer,
			// independent of reminted root/leaf allocation order.
			if previous := virtualNameReady[root.Address()]; previous != (dump.Event{}) {
				graph.Add(event, previous)
			}
			virtualNameReady[root.Address()] = event
		}
		if root := partitionReplayVirtualUniqueRoot(view, index); root != nil && partitionReplayOnlyExclusion(root) {
			lateNames[index.Address().ObjectID] = true
		}
		if lateNames[index.Address().ObjectID] {
			// Late definitions still bind the captured private spelling. Keep
			// that spelling alive through their declarations, then rename before
			// original-name row/default replay consumers.
			for _, declaration := range declarations {
				if declaration.OID == provider || !lateDeclarations[declaration.OID] {
					continue
				}
				clear(seen)
				if graph.DependsOn(declaration, dump.Event{OID: provider}, seen) {
					graph.Add(event, declaration)
				}
			}
		}
		if !lateNames[index.Address().ObjectID] {
			graph.Add(event, ready)
			ready = event
		}
	}

	lateRows := make(map[ids.OID]bool)
	lateConstraints := make(map[ids.OID]bool)
	rowSources := make(map[ids.OID][]dump.Event)
	constraintSources := make(map[ids.OID][]dump.Event)
	defaultSources := make(map[ids.OID][]dump.Event)
	lateDefaults := make(map[ids.OID]bool)
	rowAfterMembership := make(map[ids.OID]bool)
	checkAfterMembership := make(map[ids.OID]bool)
	for i := range tables {
		t := &tables[i]
		oid := t.descriptor.Address().ObjectID
		collect := func(destination *[]dump.Event, late *bool) func(ids.ObjectAddress) error {
			return func(address ids.ObjectAddress) error {
				event, exists, err := nameReady(address)
				if err != nil {
					return err
				}
				if exists && !slices.Contains(*destination, event) {
					*destination = append(*destination, event)
				}
				*late = *late || event.Phase == dump.Declaration && lateDeclarations[event.OID] || event.Phase == dump.Rename && lateNames[event.OID]
				return nil
			}
		}
		var sources []dump.Event
		late := lateDeclarations[oid]
		if err := t.rowReplayReferences(ctx, view, collect(&sources, &late)); err != nil {
			return err
		}
		// Rerouted hash rows can land in any descendant, not just their captured
		// physical leaf. All relevant memberships must precede this row event.
		if target := partitionHashReplayTarget(view, t.descriptor); target != t.descriptor {
			var requireTree func(*catalog.Relation)
			requireTree = func(d *catalog.Relation) {
				event := membership(d)
				late = late || event.Phase == dump.TableAttachment
				if !slices.Contains(sources, event) {
					sources = append(sources, event)
				}
				if d.Partition != nil {
					for _, address := range d.Partition.Children {
						requireTree(view.MustRelation(address))
					}
				}
			}
			requireTree(target)
		}
		rowSources[oid], lateRows[oid] = sources, late
		target := membership(t.descriptor)
		clear(seen)
		after := false
		if target.Phase == dump.TableAttachment {
			after = graph.DependsOn(dump.Event{OID: oid}, target, seen)
			for _, source := range sources {
				after = after || graph.DependsOn(source, target, seen)
			}
		}
		rowAfterMembership[oid] = target.Phase == dump.TableAttachment && after
		sources, late = nil, lateRows[oid]
		for _, check := range t.descriptor.Constraints.Check {
			if !check.NotValid {
				continue
			}
			add := collect(&sources, &late)
			if err := regclassReplayReferences(view, check.Expression, add); err != nil {
				return err
			}
			for _, dependency := range check.Dependencies {
				if err := add(dependency.Ref.Address); err != nil {
					return err
				}
			}
		}
		constraintSources[oid], lateConstraints[oid] = sources, late
		after = rowAfterMembership[oid]
		if target.Phase == dump.TableAttachment {
			for _, source := range sources {
				after = after || graph.DependsOn(source, target, seen)
			}
		}
		checkAfterMembership[oid] = target.Phase == dump.TableAttachment && after
		sources, late = nil, lateDeclarations[oid]
		if deferDefaults {
			for attribute, definition := range t.defaults {
				if !t.deferredDefinitions[attribute] {
					continue
				}
				add := collect(&sources, &late)
				if err := regclassReplayReferences(view, definition.Expression, add); err != nil {
					return err
				}
				for _, dependency := range definition.Dependencies {
					if err := add(dependency.Address); err != nil {
						return err
					}
				}
			}
		}
		defaultSources[oid], lateDefaults[oid] = sources, late
	}

	byOID := make(map[ids.OID]*dumpRelation, len(tables))
	for i := range tables {
		byOID[tables[i].descriptor.Address().ObjectID] = &tables[i]
	}
	for _, declaration := range declarations {
		t := byOID[declaration.OID]
		if t == nil || !deferDefaults || len(t.deferredDefinitions) == 0 {
			continue
		}
		event := dump.Event{OID: declaration.OID, Phase: dump.Defaults}
		graph[event] = append([]dump.Event{ready, declaration}, defaultSources[declaration.OID]...)
		if !lateDefaults[declaration.OID] {
			ready = event
		}
	}
	rowTail := ready
	for _, declaration := range declarations {
		if byOID[declaration.OID] == nil || lateRows[declaration.OID] {
			continue
		}
		event := dump.Event{OID: declaration.OID, Phase: dump.Rows}
		graph[event] = append([]dump.Event{rowTail}, rowSources[declaration.OID]...)
		rowTail = event
	}
	invalidTail := rowTail
	for _, declaration := range declarations {
		t := byOID[declaration.OID]
		if t == nil || !t.hasInvalidConstraints() || lateConstraints[declaration.OID] {
			continue
		}
		event := dump.Event{OID: declaration.OID, Phase: dump.InvalidConstraints}
		graph[event] = append([]dump.Event{invalidTail}, constraintSources[declaration.OID]...)
		invalidTail = event
	}
	for _, declaration := range declarations {
		oid := declaration.OID
		t := byOID[oid]
		if t == nil {
			continue
		}
		if lateRows[oid] {
			event := dump.Event{OID: oid, Phase: dump.Rows}
			graph[event] = append([]dump.Event{invalidTail, declaration}, rowSources[oid]...)
			if rowAfterMembership[oid] {
				graph.Add(event, membership(t.descriptor))
			}
		}
		if t.hasInvalidConstraints() && lateConstraints[oid] {
			event := dump.Event{OID: oid, Phase: dump.InvalidConstraints}
			graph[event] = append([]dump.Event{{OID: oid, Phase: dump.Rows}, declaration}, constraintSources[oid]...)
			if checkAfterMembership[oid] {
				graph.Add(event, membership(t.descriptor))
			}
		}
		complete := dump.Event{OID: oid, Phase: dump.TableCompletion}
		graph[complete] = []dump.Event{{OID: oid, Phase: dump.Rows}}
		if t.hasInvalidConstraints() {
			graph.Add(complete, dump.Event{OID: oid, Phase: dump.InvalidConstraints})
		}
		if deferDefaults && len(t.deferredDefinitions) != 0 {
			graph.Add(complete, dump.Event{OID: oid, Phase: dump.Defaults})
		}
		if partitionDumpNeedsAttach(view, t.descriptor) {
			event := membership(t.descriptor)
			graph[event] = []dump.Event{declaration, invalidTail, {OID: t.descriptor.Membership.Parent.ObjectID}}
			if !rowAfterMembership[oid] {
				graph.Add(event, dump.Event{OID: oid, Phase: dump.Rows})
			}
			if t.hasInvalidConstraints() && !checkAfterMembership[oid] {
				graph.Add(event, dump.Event{OID: oid, Phase: dump.InvalidConstraints})
			}
			if t.descriptor.Partition != nil {
				if partitionReplayTopologyReady(view, t.descriptor) {
					for _, child := range t.descriptor.Partition.Children {
						graph.Add(event, membership(view.MustRelation(child)))
					}
				} else {
					graph.Add(event, complete)
				}
			}
			parent := view.MustRelation(t.descriptor.Membership.Parent)
			if t.descriptor.Membership.DefaultBound {
				for _, child := range parent.Partition.Children {
					if child != t.descriptor.Address() && !partitionExclusionDefaultBefore(view, t.descriptor, view.MustRelation(child)) {
						graph.Add(event, membership(view.MustRelation(child)))
					}
				}
			}
			for indexOID := range indexIndices {
				index := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: indexOID})
				backing := partitionDumpIndexBacking(view, index.Address(), t.descriptor.Address())
				before := !lateIndexes[indexOID] || !index.SQLIndex.Valid && !partitionIndexPreservedInvalid(view, index) && backing != nil && backing.SQLIndex.Valid
				if partitionReplayOnlyExclusion(index) && backing == nil {
					before = false
				}
				if index.SQLIndex.Parent == t.descriptor.Address() && !partitionPreservedReplayBacking(view, index) && !partitionPreservedReplayLocal(view, index) || index.SQLIndex.Parent == parent.Address() && before {
					graph.Add(event, dump.Event{OID: indexOID})
				}
			}
			for _, constraint := range t.descriptor.Constraints.Unique {
				index := view.MustRelation(constraint.Index)
				// This local provider does not exist until after the preserving
				// reattachment. Its terminal name remains a consumer predecessor,
				// but cannot be a prerequisite of the initial table membership.
				if partitionPreservedReplayLocal(view, index) || partitionReplayVirtualUniqueRoot(view, index) != nil {
					continue
				}
				if _, exists := replay.temporary[constraint.Index]; exists {
					graph.Add(event, dump.Event{OID: constraint.Index.ObjectID, Phase: dump.Rename})
				}
			}
		}
		if t.descriptor.Partition != nil {
			for _, child := range t.descriptor.Partition.Children {
				d := view.MustRelation(child)
				graph.Add(complete, membership(d))
				graph.Add(complete, dump.Event{OID: child.ObjectID, Phase: dump.TableCompletion})
			}
		}
	}
	for indexOID := range indexIndices {
		index := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: indexOID})
		table := view.MustRelation(index.SQLIndex.Parent)
		graph.Add(dump.Event{OID: table.Address().ObjectID, Phase: dump.TableCompletion}, dump.Event{OID: indexOID})
	}
	for _, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.PartitionParent == (ids.ObjectAddress{}) {
			continue
		}
		parent := view.MustRelation(index.SQLIndex.PartitionParent)
		table := view.MustRelation(index.SQLIndex.Parent)
		if parent.SQLIndex.Constraint != nil || partitionDumpNeedsAttach(view, table) && !lateIndexes[parent.Address().ObjectID] {
			continue // Ordinary ATTACH links this captured backing itself.
		}
		event := dump.Event{OID: index.Address().ObjectID, Phase: dump.IndexAttachment}
		graph[event] = []dump.Event{membership(table)}
		for _, address := range []ids.ObjectAddress{index.Address(), parent.Address()} {
			source, exists, err := nameReady(address)
			if err != nil {
				return err
			}
			if exists {
				graph.Add(event, source)
			}
		}
		if table.Partition != nil {
			graph.Add(event, dump.Event{OID: table.Address().ObjectID, Phase: dump.TableCompletion})
		}
		graph.Add(dump.Event{OID: parent.SQLIndex.Parent.ObjectID, Phase: dump.TableCompletion}, event)
	}
	// Apply ONLY topology edges after membership events have their final
	// predecessors; earlier attachment graph initialization is provisional.
	for oid := range indexIndices {
		index := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
		if !partitionReplayOnlyExclusion(index) {
			continue
		}
		table := view.MustRelation(index.SQLIndex.Parent)
		for _, address := range table.Partition.Children {
			child := membership(view.MustRelation(address))
			declaration := dump.Event{OID: oid}
			if partitionDumpIndexBacking(view, index.Address(), address) == nil {
				graph.Add(declaration, child)
			} else {
				graph.Add(child, declaration)
			}
		}
	}
	// Captured attachment history can mint inherited indexes after independent
	// local trees, whereas replay creates them inline. OID priority therefore
	// cannot order their terminal names. Finish the affected creation events,
	// then rename in captured logical order, including coexisting local trees.
	onlyTables := make(map[ids.ObjectAddress]bool)
	for _, index := range renameOrder {
		if root := partitionReplayVirtualUniqueRoot(view, index); root != nil && partitionReplayOnlyExclusion(root) {
			onlyTables[index.SQLIndex.Parent] = true
		}
	}
	var names []dump.Event
	var creators []dump.Event
	for _, index := range renameOrder {
		if !onlyTables[index.SQLIndex.Parent] || !lateNames[index.Address().ObjectID] {
			continue
		}
		names = append(names, dump.Event{OID: index.Address().ObjectID, Phase: dump.Rename})
		creator := partitionConstraintCreator(view, index)
		if !slices.Contains(creators, creator) {
			creators = append(creators, creator)
		}
	}
	for i, event := range names {
		for _, creator := range creators {
			graph.Add(event, creator)
		}
		if i != 0 {
			graph.Add(event, names[i-1])
		}
	}
	for _, index := range renameOrder {
		if !onlyTables[index.SQLIndex.Parent] {
			continue
		}
		rows := dump.Event{OID: index.SQLIndex.Parent.ObjectID, Phase: dump.Rows}
		name := dump.Event{OID: index.Address().ObjectID, Phase: dump.Rename}
		clear(seen)
		if !graph.DependsOn(name, rows, seen) {
			graph.Add(rows, name)
		}
	}
	if err := inheritanceRecipeEvents(ctx, view, graph, declarations, tables, nameReady); err != nil {
		return err
	}
	retiredConstraintEvents(view, graph, tables)
	if err := inheritedForeignKeyEvents(view, graph, tables, nameReady); err != nil {
		return err
	}
	return ctx.Err()
}
