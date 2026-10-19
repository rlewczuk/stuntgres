package engine

import (
	"context"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func addDumpDependency(graph map[dumpEventKey][]dumpEventKey, event, predecessor dumpEventKey) {
	if !slices.Contains(graph[event], predecessor) {
		graph[event] = append(graph[event], predecessor)
	}
}

func partitionDumpIndexBacking(view catalog.Read, index, table ids.ObjectAddress) *catalog.Relation {
	for _, descriptor := range view.Descriptors() {
		candidate, ok := descriptor.(*catalog.Relation)
		if ok && candidate.SQLIndex != nil && candidate.SQLIndex.Parent == table && candidate.SQLIndex.PartitionParent == index {
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
func partitionDumpEvents(ctx context.Context, view catalog.Read, graph map[dumpEventKey][]dumpEventKey, declarations []dumpEventKey, tables []dumpRelation, indexIndices map[ids.OID]int, replay *partitionConstraintReplay, deferDefaults bool) error {
	lateTables := partitionDumpLateIndexTables(view)
	lateIndexes := make(map[ids.OID]bool)
	for oid := range indexIndices {
		index := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
		lateIndexes[oid] = index.SQLIndex.Constraint == nil && lateTables[index.SQLIndex.Parent] || partitionPreservedReplayBacking(view, index) || partitionPreservedReplayLocal(view, index)
		if root := partitionReplayVirtualUniqueRoot(view, index); root != nil && !partitionReplayOnlyExclusion(root) {
			partitionVirtualUniqueMembershipReady(view, graph, dumpEventKey{oid: oid}, view.MustRelation(index.SQLIndex.Parent))
		}
	}
	membership := func(table *catalog.Relation) dumpEventKey {
		if partitionDumpNeedsAttach(view, table) {
			return dumpEventKey{oid: table.Address().ObjectID, phase: dumpTableAttachment}
		}
		return dumpEventKey{oid: table.Address().ObjectID}
	}
	for _, t := range tables {
		d := t.descriptor
		event := membership(d)
		if event.phase != dumpTableAttachment {
			continue
		}
		graph[event] = []dumpEventKey{{oid: d.Address().ObjectID}, {oid: d.Membership.Parent.ObjectID}}
		if d.Partition != nil {
			for _, child := range d.Partition.Children {
				addDumpDependency(graph, event, membership(view.MustRelation(child)))
			}
		}
		if d.Membership.DefaultBound {
			parent := view.MustRelation(d.Membership.Parent)
			for _, child := range parent.Partition.Children {
				if child != d.Address() && !partitionExclusionDefaultBefore(view, d, view.MustRelation(child)) {
					addDumpDependency(graph, event, membership(view.MustRelation(child)))
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
				addDumpDependency(graph, dumpEventKey{oid: oid}, membership(view.MustRelation(child)))
			}
		}
	}
	lateDeclarations := make(map[ids.OID]bool)
	for _, event := range declarations {
		lateDeclarations[event.oid] = lateIndexes[event.oid]
		if _, exists := indexIndices[event.oid]; exists {
			index := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: event.oid})
			if partitionReplayOnlyExclusion(index) {
				lateDeclarations[event.oid] = true
			}
		}
		if descriptor, ok := view.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: event.oid}); ok && descriptor.Membership != nil && !partitionDumpNeedsAttach(view, descriptor) {
			parent := view.MustRelation(descriptor.Membership.Parent)
			for _, index := range partitionIndexDeclarationOrder(view, parent) {
				if root := partitionReplayVirtualUniqueRoot(view, index); root != nil && partitionReplayOnlyExclusion(root) && partitionDumpIndexBacking(view, index.Address(), descriptor.Address()) != nil {
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
	virtualNameReady := make(map[ids.ObjectAddress]dumpEventKey)
	nameReady := func(address ids.ObjectAddress) (dumpEventKey, bool, error) {
		address.SubID = 0
		if _, exists := replay.temporary[address]; exists {
			index := view.MustRelation(address)
			if root := partitionReplayVirtualUniqueRoot(view, index); root != nil && !partitionReplayOnlyExclusion(root) {
				return virtualNameReady[root.Address()], true, nil
			}
			return dumpEventKey{oid: address.ObjectID, phase: dumpRename}, true, nil
		}
		oid, err := dumpPredecessor(view, address, ids.ObjectAddress{})
		return dumpEventKey{oid: oid}, oid != 0, err
	}
	lateNames := make(map[ids.OID]bool)

	// Retain the existing nonoperator definition chain. Operator declarations
	// interleave through real prerequisites: chaining an unrelated type after
	// an operator could make it wait for links whose endpoint needs that type.
	var earlyTail dumpEventKey
	for _, event := range declarations {
		if event.phase != dumpDeclaration {
			continue
		}
		if _, operator := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: event.oid}); operator {
			continue
		}
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
		provider, err := dumpPredecessor(view, index.Address(), ids.ObjectAddress{})
		if err != nil {
			return err
		}
		event := dumpEventKey{oid: index.Address().ObjectID, phase: dumpRename}
		creator := partitionConstraintCreator(view, index)
		graph[event] = []dumpEventKey{earlyTail, creator}
		lateNames[index.Address().ObjectID] = lateDeclarations[provider] || creator.phase == dumpTableAttachment
		if root := partitionReplayVirtualUniqueRoot(view, index); root != nil && !partitionReplayOnlyExclusion(root) {
			// A definition creates the whole local tree. Complete its terminal
			// names in captured logical order before any original-name consumer,
			// independent of reminted root/leaf allocation order.
			if previous := virtualNameReady[root.Address()]; previous != (dumpEventKey{}) {
				addDumpDependency(graph, event, previous)
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
				if declaration.oid == provider || !lateDeclarations[declaration.oid] {
					continue
				}
				clear(seen)
				if dumpEventDependsOn(graph, declaration, dumpEventKey{oid: provider}, seen) {
					addDumpDependency(graph, event, declaration)
				}
			}
		}
		if !lateNames[index.Address().ObjectID] {
			addDumpDependency(graph, event, ready)
			ready = event
		}
	}

	lateRows := make(map[ids.OID]bool)
	lateConstraints := make(map[ids.OID]bool)
	rowSources := make(map[ids.OID][]dumpEventKey)
	constraintSources := make(map[ids.OID][]dumpEventKey)
	defaultSources := make(map[ids.OID][]dumpEventKey)
	lateDefaults := make(map[ids.OID]bool)
	rowAfterMembership := make(map[ids.OID]bool)
	checkAfterMembership := make(map[ids.OID]bool)
	for i := range tables {
		t := &tables[i]
		oid := t.descriptor.Address().ObjectID
		collect := func(destination *[]dumpEventKey, late *bool) func(ids.ObjectAddress) error {
			return func(address ids.ObjectAddress) error {
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
			var requireTree func(*catalog.Relation)
			requireTree = func(d *catalog.Relation) {
				event := membership(d)
				late = late || event.phase == dumpTableAttachment
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
		if target.phase == dumpTableAttachment {
			after = dumpEventDependsOn(graph, dumpEventKey{oid: oid}, target, seen)
			for _, source := range sources {
				after = after || dumpEventDependsOn(graph, source, target, seen)
			}
		}
		rowAfterMembership[oid] = target.phase == dumpTableAttachment && after
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
			graph[event] = []dumpEventKey{declaration, invalidTail, {oid: t.descriptor.Membership.Parent.ObjectID}}
			if !rowAfterMembership[oid] {
				addDumpDependency(graph, event, dumpEventKey{oid: oid, phase: dumpRows})
			}
			if t.hasInvalidConstraints() && !checkAfterMembership[oid] {
				addDumpDependency(graph, event, dumpEventKey{oid: oid, phase: dumpInvalidConstraints})
			}
			if t.descriptor.Partition != nil {
				if partitionReplayTopologyReady(view, t.descriptor) {
					for _, child := range t.descriptor.Partition.Children {
						addDumpDependency(graph, event, membership(view.MustRelation(child)))
					}
				} else {
					addDumpDependency(graph, event, complete)
				}
			}
			parent := view.MustRelation(t.descriptor.Membership.Parent)
			if t.descriptor.Membership.DefaultBound {
				for _, child := range parent.Partition.Children {
					if child != t.descriptor.Address() && !partitionExclusionDefaultBefore(view, t.descriptor, view.MustRelation(child)) {
						addDumpDependency(graph, event, membership(view.MustRelation(child)))
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
					addDumpDependency(graph, event, dumpEventKey{oid: indexOID})
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
					addDumpDependency(graph, event, dumpEventKey{oid: constraint.Index.ObjectID, phase: dumpRename})
				}
			}
		}
		if t.descriptor.Partition != nil {
			for _, child := range t.descriptor.Partition.Children {
				d := view.MustRelation(child)
				addDumpDependency(graph, complete, membership(d))
				addDumpDependency(graph, complete, dumpEventKey{oid: child.ObjectID, phase: dumpTableCompletion})
			}
		}
	}
	for indexOID := range indexIndices {
		index := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: indexOID})
		table := view.MustRelation(index.SQLIndex.Parent)
		addDumpDependency(graph, dumpEventKey{oid: table.Address().ObjectID, phase: dumpTableCompletion}, dumpEventKey{oid: indexOID})
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
		event := dumpEventKey{oid: index.Address().ObjectID, phase: dumpIndexAttachment}
		graph[event] = []dumpEventKey{membership(table)}
		for _, address := range []ids.ObjectAddress{index.Address(), parent.Address()} {
			source, exists, err := nameReady(address)
			if err != nil {
				return err
			}
			if exists {
				addDumpDependency(graph, event, source)
			}
		}
		if table.Partition != nil {
			addDumpDependency(graph, event, dumpEventKey{oid: table.Address().ObjectID, phase: dumpTableCompletion})
		}
		addDumpDependency(graph, dumpEventKey{oid: parent.SQLIndex.Parent.ObjectID, phase: dumpTableCompletion}, event)
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
			declaration := dumpEventKey{oid: oid}
			if partitionDumpIndexBacking(view, index.Address(), address) == nil {
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
	onlyTables := make(map[ids.ObjectAddress]bool)
	for _, index := range renameOrder {
		if root := partitionReplayVirtualUniqueRoot(view, index); root != nil && partitionReplayOnlyExclusion(root) {
			onlyTables[index.SQLIndex.Parent] = true
		}
	}
	var names []dumpEventKey
	var creators []dumpEventKey
	for _, index := range renameOrder {
		if !onlyTables[index.SQLIndex.Parent] || !lateNames[index.Address().ObjectID] {
			continue
		}
		names = append(names, dumpEventKey{oid: index.Address().ObjectID, phase: dumpRename})
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
		if !onlyTables[index.SQLIndex.Parent] {
			continue
		}
		rows := dumpEventKey{oid: index.SQLIndex.Parent.ObjectID, phase: dumpRows}
		name := dumpEventKey{oid: index.Address().ObjectID, phase: dumpRename}
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
