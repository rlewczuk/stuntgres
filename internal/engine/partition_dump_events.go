package engine

import (
	"context"
	"slices"
)

func addDumpDependency(graph map[dumpEventKey][]dumpEventKey, event, predecessor dumpEventKey) {
	if !slices.Contains(graph[event], predecessor) {
		graph[event] = append(graph[event], predecessor)
	}
}

func partitionDumpIndexBacking(view CatalogRead, index, table ObjectAddress) *relationDescriptor {
	for _, descriptor := range view.state.descriptors {
		candidate, ok := descriptor.(*relationDescriptor)
		if ok && candidate.sqlIndex != nil && candidate.sqlIndex.parent == table && candidate.sqlIndex.partitionParent == index {
			return candidate
		}
	}
	return nil
}

func dumpEventDependsOn(graph map[dumpEventKey][]dumpEventKey, event, target dumpEventKey, seen map[dumpEventKey]bool) bool {
	if event == target {
		return true
	}
	if seen[event] {
		return false
	}
	seen[event] = true
	for _, predecessor := range graph[event] {
		if dumpEventDependsOn(graph, predecessor, target, seen) {
			return true
		}
	}
	return false
}

// A preexisting incomplete manual index must not be present while ATTACH could
// synthesize a missing child or replace a captured invalid child. Delay all
// manual virtual indexes on that table, including the intended valid tree, so
// physical backings cannot be consumed by the wrong equivalent parent index.
func partitionDumpLateIndexTables(view CatalogRead) map[ObjectAddress]bool {
	late := make(map[ObjectAddress]bool)
	for _, descriptor := range view.state.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil || index.sqlIndex.valid || index.sqlIndex.constraint != nil {
			continue
		}
		table := view.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
		if table.partition == nil || late[table.address] {
			continue
		}
		if partitionIndexPreservedInvalid(view, index) {
			late[table.address] = true
			continue
		}
		for _, child := range table.partition.children {
			candidate := view.state.descriptors[child].(*relationDescriptor)
			if !partitionDumpNeedsAttach(view, candidate) {
				continue
			}
			backing := partitionDumpIndexBacking(view, index.address, child)
			if backing == nil || !backing.sqlIndex.valid {
				late[table.address] = true
				break
			}
		}
	}
	return late
}

// Extend the existing declaration graph in place. The same source-priority
// scheduler owns declarations, names, physical data, constraint origins and
// captured membership/index edges; there is no independent topology sorter.
func partitionDumpEvents(ctx context.Context, view CatalogRead, graph map[dumpEventKey][]dumpEventKey, declarations []dumpEventKey, tables []dumpRelation, indexIndices map[OID]int, replay *partitionConstraintReplay, deferDefaults bool) error {
	lateTables := partitionDumpLateIndexTables(view)
	lateIndexes := make(map[OID]bool)
	for oid := range indexIndices {
		index := view.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: oid}].(*relationDescriptor)
		lateIndexes[oid] = index.sqlIndex.constraint == nil && lateTables[index.sqlIndex.parent] || partitionPreservedReplayBacking(view, index) || partitionPreservedReplayLocal(view, index)
		if root := partitionReplayVirtualUniqueRoot(view, index); root != nil && !partitionReplayOnlyExclusion(root) {
			partitionVirtualUniqueMembershipReady(view, graph, dumpEventKey{oid: oid}, view.state.descriptors[index.sqlIndex.parent].(*relationDescriptor))
		}
	}
	membership := func(table *relationDescriptor) dumpEventKey {
		if partitionDumpNeedsAttach(view, table) {
			return dumpEventKey{oid: table.address.ObjectID, phase: dumpTableAttachment}
		}
		return dumpEventKey{oid: table.address.ObjectID}
	}
	for _, t := range tables {
		d := t.descriptor
		event := membership(d)
		if event.phase != dumpTableAttachment {
			continue
		}
		graph[event] = []dumpEventKey{{oid: d.address.ObjectID}, {oid: d.membership.parent.ObjectID}}
		if d.partition != nil {
			for _, child := range d.partition.children {
				addDumpDependency(graph, event, membership(view.state.descriptors[child].(*relationDescriptor)))
			}
		}
		if d.membership.defaultBound {
			parent := view.state.descriptors[d.membership.parent].(*relationDescriptor)
			for _, child := range parent.partition.children {
				if child != d.address && !partitionExclusionDefaultBefore(view, d, view.state.descriptors[child].(*relationDescriptor)) {
					addDumpDependency(graph, event, membership(view.state.descriptors[child].(*relationDescriptor)))
				}
			}
		}
	}
	for oid := range indexIndices {
		if !lateIndexes[oid] {
			continue
		}
		index := view.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: oid}].(*relationDescriptor)
		table := view.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
		if table.partition == nil {
			continue
		}
		for _, child := range table.partition.children {
			backing := partitionDumpIndexBacking(view, index.address, child)
			if index.sqlIndex.valid || partitionIndexPreservedInvalid(view, index) || backing == nil || !backing.sqlIndex.valid {
				addDumpDependency(graph, dumpEventKey{oid: oid}, membership(view.state.descriptors[child].(*relationDescriptor)))
			}
		}
	}
	lateDeclarations := make(map[OID]bool)
	for _, event := range declarations {
		lateDeclarations[event.oid] = lateIndexes[event.oid]
		if _, exists := indexIndices[event.oid]; exists {
			index := view.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: event.oid}].(*relationDescriptor)
			if partitionReplayOnlyExclusion(index) {
				lateDeclarations[event.oid] = true
			}
		}
		if descriptor, ok := view.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: event.oid}].(*relationDescriptor); ok && descriptor.membership != nil && !partitionDumpNeedsAttach(view, descriptor) {
			parent := view.state.descriptors[descriptor.membership.parent].(*relationDescriptor)
			for _, index := range partitionIndexDeclarationOrder(view, parent) {
				if root := partitionReplayVirtualUniqueRoot(view, index); root != nil && partitionReplayOnlyExclusion(root) && partitionDumpIndexBacking(view, index.address, descriptor.address) != nil {
					lateDeclarations[event.oid] = true
				}
			}
		}
		for _, predecessor := range graph[event] {
			if predecessor.phase == dumpDeclaration {
				lateDeclarations[event.oid] = lateDeclarations[event.oid] || lateDeclarations[predecessor.oid]
			}
		}
	}
	virtualNameReady := make(map[ObjectAddress]dumpEventKey)
	nameReady := func(address ObjectAddress) (dumpEventKey, bool, error) {
		address.SubID = 0
		if _, exists := replay.temporary[address]; exists {
			index := view.state.descriptors[address].(*relationDescriptor)
			if root := partitionReplayVirtualUniqueRoot(view, index); root != nil && !partitionReplayOnlyExclusion(root) {
				return virtualNameReady[root.address], true, nil
			}
			return dumpEventKey{oid: address.ObjectID, phase: dumpRename}, true, nil
		}
		oid, err := dumpPredecessor(view, address, ObjectAddress{})
		return dumpEventKey{oid: oid}, oid != 0, err
	}
	lateNames := make(map[OID]bool)

	// Keep the old definition-only source ordering exactly. Late declarations
	// leave that chain and wait for real child memberships instead.
	var earlyTail dumpEventKey
	for _, event := range declarations {
		if lateDeclarations[event.oid] {
			continue
		}
		if earlyTail != (dumpEventKey{}) {
			addDumpDependency(graph, event, earlyTail)
		}
		earlyTail = event
	}
	if earlyTail == (dumpEventKey{}) {
		return nil
	}
	ready := earlyTail
	seen := make(map[dumpEventKey]bool)
	renameOrder := replay.finalRenameOrder()
	for _, index := range renameOrder {
		provider, err := dumpPredecessor(view, index.address, ObjectAddress{})
		if err != nil {
			return err
		}
		event := dumpEventKey{oid: index.address.ObjectID, phase: dumpRename}
		creator := partitionConstraintCreator(view, index)
		graph[event] = []dumpEventKey{earlyTail, creator}
		lateNames[index.address.ObjectID] = lateDeclarations[provider] || creator.phase == dumpTableAttachment
		if root := partitionReplayVirtualUniqueRoot(view, index); root != nil && !partitionReplayOnlyExclusion(root) {
			// A definition creates the whole local tree. Complete its terminal
			// names in captured logical order before any original-name consumer,
			// independent of reminted root/leaf allocation order.
			if previous := virtualNameReady[root.address]; previous != (dumpEventKey{}) {
				addDumpDependency(graph, event, previous)
			}
			virtualNameReady[root.address] = event
		}
		if root := partitionReplayVirtualUniqueRoot(view, index); root != nil && partitionReplayOnlyExclusion(root) {
			lateNames[index.address.ObjectID] = true
		}
		if lateNames[index.address.ObjectID] {
			// Late definitions still bind the captured private spelling. Keep
			// that spelling alive through their declarations, then rename before
			// original-name row/default replay consumers.
			for _, declaration := range declarations {
				if declaration.oid == provider || !lateDeclarations[declaration.oid] {
					continue
				}
				clear(seen)
				if dumpEventDependsOn(graph, declaration, dumpEventKey{oid: provider}, seen) {
					addDumpDependency(graph, event, declaration)
				}
			}
		}
		if !lateNames[index.address.ObjectID] {
			addDumpDependency(graph, event, ready)
			ready = event
		}
	}

	lateRows := make(map[OID]bool)
	lateConstraints := make(map[OID]bool)
	rowSources := make(map[OID][]dumpEventKey)
	constraintSources := make(map[OID][]dumpEventKey)
	defaultSources := make(map[OID][]dumpEventKey)
	lateDefaults := make(map[OID]bool)
	rowAfterMembership := make(map[OID]bool)
	checkAfterMembership := make(map[OID]bool)
	for i := range tables {
		t := &tables[i]
		oid := t.descriptor.address.ObjectID
		collect := func(destination *[]dumpEventKey, late *bool) func(ObjectAddress) error {
			return func(address ObjectAddress) error {
				event, exists, err := nameReady(address)
				if err != nil {
					return err
				}
				if exists && !slices.Contains(*destination, event) {
					*destination = append(*destination, event)
				}
				*late = *late || event.phase == dumpDeclaration && lateDeclarations[event.oid] || event.phase == dumpRename && lateNames[event.oid]
				return nil
			}
		}
		var sources []dumpEventKey
		late := lateDeclarations[oid]
		if err := t.rowReplayReferences(ctx, view, collect(&sources, &late)); err != nil {
			return err
		}
		// Rerouted hash rows can land in any descendant, not just their captured
		// physical leaf. All relevant memberships must precede this row event.
		if target := partitionHashReplayTarget(view, t.descriptor); target != t.descriptor {
			var requireTree func(*relationDescriptor)
			requireTree = func(d *relationDescriptor) {
				event := membership(d)
				late = late || event.phase == dumpTableAttachment
				if !slices.Contains(sources, event) {
					sources = append(sources, event)
				}
				if d.partition != nil {
					for _, address := range d.partition.children {
						requireTree(view.state.descriptors[address].(*relationDescriptor))
					}
				}
			}
			requireTree(target)
		}
		rowSources[oid], lateRows[oid] = sources, late
		target := membership(t.descriptor)
		clear(seen)
		after := false
		if target.phase == dumpTableAttachment {
			after = dumpEventDependsOn(graph, dumpEventKey{oid: oid}, target, seen)
			for _, source := range sources {
				after = after || dumpEventDependsOn(graph, source, target, seen)
			}
		}
		rowAfterMembership[oid] = target.phase == dumpTableAttachment && after
		sources, late = nil, lateRows[oid]
		for _, check := range t.descriptor.checkConstraints {
			if !check.NotValid {
				continue
			}
			add := collect(&sources, &late)
			if err := regclassReplayReferences(view, check.expression, add); err != nil {
				return err
			}
			for _, dependency := range check.dependencies {
				if err := add(dependency.ref.Address); err != nil {
					return err
				}
			}
		}
		constraintSources[oid], lateConstraints[oid] = sources, late
		after = rowAfterMembership[oid]
		if target.phase == dumpTableAttachment {
			for _, source := range sources {
				after = after || dumpEventDependsOn(graph, source, target, seen)
			}
		}
		checkAfterMembership[oid] = target.phase == dumpTableAttachment && after
		sources, late = nil, lateDeclarations[oid]
		if deferDefaults {
			for attribute, definition := range t.defaults {
				if !t.deferredDefinitions[attribute] {
					continue
				}
				add := collect(&sources, &late)
				if err := regclassReplayReferences(view, definition.expression, add); err != nil {
					return err
				}
				for _, dependency := range definition.dependencies {
					if err := add(dependency.Address); err != nil {
						return err
					}
				}
			}
		}
		defaultSources[oid], lateDefaults[oid] = sources, late
	}

	byOID := make(map[OID]*dumpRelation, len(tables))
	for i := range tables {
		byOID[tables[i].descriptor.address.ObjectID] = &tables[i]
	}
	for _, declaration := range declarations {
		t := byOID[declaration.oid]
		if t == nil || !deferDefaults || len(t.deferredDefinitions) == 0 {
			continue
		}
		event := dumpEventKey{oid: declaration.oid, phase: dumpDefaults}
		graph[event] = append([]dumpEventKey{ready, declaration}, defaultSources[declaration.oid]...)
		if !lateDefaults[declaration.oid] {
			ready = event
		}
	}
	rowTail := ready
	for _, declaration := range declarations {
		if byOID[declaration.oid] == nil || lateRows[declaration.oid] {
			continue
		}
		event := dumpEventKey{oid: declaration.oid, phase: dumpRows}
		graph[event] = append([]dumpEventKey{rowTail}, rowSources[declaration.oid]...)
		rowTail = event
	}
	invalidTail := rowTail
	for _, declaration := range declarations {
		t := byOID[declaration.oid]
		if t == nil || !t.hasInvalidConstraints() || lateConstraints[declaration.oid] {
			continue
		}
		event := dumpEventKey{oid: declaration.oid, phase: dumpInvalidConstraints}
		graph[event] = append([]dumpEventKey{invalidTail}, constraintSources[declaration.oid]...)
		invalidTail = event
	}
	for _, declaration := range declarations {
		oid := declaration.oid
		t := byOID[oid]
		if t == nil {
			continue
		}
		if lateRows[oid] {
			event := dumpEventKey{oid: oid, phase: dumpRows}
			graph[event] = append([]dumpEventKey{invalidTail, declaration}, rowSources[oid]...)
			if rowAfterMembership[oid] {
				addDumpDependency(graph, event, membership(t.descriptor))
			}
		}
		if t.hasInvalidConstraints() && lateConstraints[oid] {
			event := dumpEventKey{oid: oid, phase: dumpInvalidConstraints}
			graph[event] = append([]dumpEventKey{{oid: oid, phase: dumpRows}, declaration}, constraintSources[oid]...)
			if checkAfterMembership[oid] {
				addDumpDependency(graph, event, membership(t.descriptor))
			}
		}
		complete := dumpEventKey{oid: oid, phase: dumpTableCompletion}
		graph[complete] = []dumpEventKey{{oid: oid, phase: dumpRows}}
		if t.hasInvalidConstraints() {
			addDumpDependency(graph, complete, dumpEventKey{oid: oid, phase: dumpInvalidConstraints})
		}
		if deferDefaults && len(t.deferredDefinitions) != 0 {
			addDumpDependency(graph, complete, dumpEventKey{oid: oid, phase: dumpDefaults})
		}
		if partitionDumpNeedsAttach(view, t.descriptor) {
			event := membership(t.descriptor)
			graph[event] = []dumpEventKey{declaration, invalidTail, {oid: t.descriptor.membership.parent.ObjectID}}
			if !rowAfterMembership[oid] {
				addDumpDependency(graph, event, dumpEventKey{oid: oid, phase: dumpRows})
			}
			if t.hasInvalidConstraints() && !checkAfterMembership[oid] {
				addDumpDependency(graph, event, dumpEventKey{oid: oid, phase: dumpInvalidConstraints})
			}
			if t.descriptor.partition != nil {
				if partitionReplayTopologyReady(view, t.descriptor) {
					for _, child := range t.descriptor.partition.children {
						addDumpDependency(graph, event, membership(view.state.descriptors[child].(*relationDescriptor)))
					}
				} else {
					addDumpDependency(graph, event, complete)
				}
			}
			parent := view.state.descriptors[t.descriptor.membership.parent].(*relationDescriptor)
			if t.descriptor.membership.defaultBound {
				for _, child := range parent.partition.children {
					if child != t.descriptor.address && !partitionExclusionDefaultBefore(view, t.descriptor, view.state.descriptors[child].(*relationDescriptor)) {
						addDumpDependency(graph, event, membership(view.state.descriptors[child].(*relationDescriptor)))
					}
				}
			}
			for indexOID := range indexIndices {
				index := view.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: indexOID}].(*relationDescriptor)
				backing := partitionDumpIndexBacking(view, index.address, t.descriptor.address)
				before := !lateIndexes[indexOID] || !index.sqlIndex.valid && !partitionIndexPreservedInvalid(view, index) && backing != nil && backing.sqlIndex.valid
				if partitionReplayOnlyExclusion(index) && backing == nil {
					before = false
				}
				if index.sqlIndex.parent == t.descriptor.address && !partitionPreservedReplayBacking(view, index) && !partitionPreservedReplayLocal(view, index) || index.sqlIndex.parent == parent.address && before {
					addDumpDependency(graph, event, dumpEventKey{oid: indexOID})
				}
			}
			for _, constraint := range t.descriptor.uniqueConstraints {
				index := view.state.descriptors[constraint.Index].(*relationDescriptor)
				// This local provider does not exist until after the preserving
				// reattachment. Its terminal name remains a consumer predecessor,
				// but cannot be a prerequisite of the initial table membership.
				if partitionPreservedReplayLocal(view, index) || partitionReplayVirtualUniqueRoot(view, index) != nil {
					continue
				}
				if _, exists := replay.temporary[constraint.Index]; exists {
					addDumpDependency(graph, event, dumpEventKey{oid: constraint.Index.ObjectID, phase: dumpRename})
				}
			}
		}
		if t.descriptor.partition != nil {
			for _, child := range t.descriptor.partition.children {
				d := view.state.descriptors[child].(*relationDescriptor)
				addDumpDependency(graph, complete, membership(d))
				addDumpDependency(graph, complete, dumpEventKey{oid: child.ObjectID, phase: dumpTableCompletion})
			}
		}
	}
	for indexOID := range indexIndices {
		index := view.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: indexOID}].(*relationDescriptor)
		table := view.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
		addDumpDependency(graph, dumpEventKey{oid: table.address.ObjectID, phase: dumpTableCompletion}, dumpEventKey{oid: indexOID})
	}
	for _, descriptor := range view.state.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil || index.sqlIndex.partitionParent == (ObjectAddress{}) {
			continue
		}
		parent := view.state.descriptors[index.sqlIndex.partitionParent].(*relationDescriptor)
		table := view.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
		if parent.sqlIndex.constraint != nil || partitionDumpNeedsAttach(view, table) && !lateIndexes[parent.address.ObjectID] {
			continue // Ordinary ATTACH links this captured backing itself.
		}
		event := dumpEventKey{oid: index.address.ObjectID, phase: dumpIndexAttachment}
		graph[event] = []dumpEventKey{membership(table)}
		for _, address := range []ObjectAddress{index.address, parent.address} {
			source, exists, err := nameReady(address)
			if err != nil {
				return err
			}
			if exists {
				addDumpDependency(graph, event, source)
			}
		}
		if table.partition != nil {
			addDumpDependency(graph, event, dumpEventKey{oid: table.address.ObjectID, phase: dumpTableCompletion})
		}
		addDumpDependency(graph, dumpEventKey{oid: parent.sqlIndex.parent.ObjectID, phase: dumpTableCompletion}, event)
	}
	// Apply ONLY topology edges after membership events have their final
	// predecessors; earlier attachment graph initialization is provisional.
	for oid := range indexIndices {
		index := view.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: oid}].(*relationDescriptor)
		if !partitionReplayOnlyExclusion(index) {
			continue
		}
		table := view.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
		for _, address := range table.partition.children {
			child := membership(view.state.descriptors[address].(*relationDescriptor))
			declaration := dumpEventKey{oid: oid}
			if partitionDumpIndexBacking(view, index.address, address) == nil {
				addDumpDependency(graph, declaration, child)
			} else {
				addDumpDependency(graph, child, declaration)
			}
		}
	}
	// Captured attachment history can mint inherited indexes after independent
	// local trees, whereas replay creates them inline. OID priority therefore
	// cannot order their terminal names. Finish the affected creation events,
	// then rename in captured logical order, including coexisting local trees.
	onlyTables := make(map[ObjectAddress]bool)
	for _, index := range renameOrder {
		if root := partitionReplayVirtualUniqueRoot(view, index); root != nil && partitionReplayOnlyExclusion(root) {
			onlyTables[index.sqlIndex.parent] = true
		}
	}
	var names []dumpEventKey
	var creators []dumpEventKey
	for _, index := range renameOrder {
		if !onlyTables[index.sqlIndex.parent] || !lateNames[index.address.ObjectID] {
			continue
		}
		names = append(names, dumpEventKey{oid: index.address.ObjectID, phase: dumpRename})
		creator := partitionConstraintCreator(view, index)
		if !slices.Contains(creators, creator) {
			creators = append(creators, creator)
		}
	}
	for i, event := range names {
		for _, creator := range creators {
			addDumpDependency(graph, event, creator)
		}
		if i != 0 {
			addDumpDependency(graph, event, names[i-1])
		}
	}
	for _, index := range renameOrder {
		if !onlyTables[index.sqlIndex.parent] {
			continue
		}
		rows := dumpEventKey{oid: index.sqlIndex.parent.ObjectID, phase: dumpRows}
		name := dumpEventKey{oid: index.address.ObjectID, phase: dumpRename}
		clear(seen)
		if !dumpEventDependsOn(graph, name, rows, seen) {
			addDumpDependency(graph, rows, name)
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
