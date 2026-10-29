package partition

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/table"
)

// Delay manual virtual indexes when membership must precede their declarations.
// A valid root must not exist while CREATE TABLE PARTITION OF could synthesize
// a captured child index that the script declares separately. Existing incomplete
// roots also stay absent while ATTACH could consume the wrong physical backing.
func dumpLateIndexTables(view catalog.Read) map[ids.ObjectAddress]bool {
	late := make(map[ids.ObjectAddress]bool)
	for _, descriptor := range view.Descriptors() {
		idx, ok := descriptor.(*catalog.Relation)
		if !ok || idx.SQLIndex == nil || idx.SQLIndex.Constraint != nil {
			continue
		}
		table := view.MustRelation(idx.SQLIndex.Parent)
		if table.Partition == nil || late[table.Address()] {
			continue
		}
		if idx.SQLIndex.Valid {
			for _, child := range table.Partition.Children {
				if !DumpNeedsAttach(view, view.MustRelation(child)) {
					late[table.Address()] = true
					break
				}
			}
			continue
		}
		if index.PartitionIndexPreservedInvalid(view, idx) {
			late[table.Address()] = true
			continue
		}
		for _, child := range table.Partition.Children {
			candidate := view.MustRelation(child)
			if !DumpNeedsAttach(view, candidate) {
				continue
			}
			backing := index.PartitionDumpIndexBacking(view, idx.Address(), child)
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
func DumpEvents(ctx context.Context, r dump.Resolver, view catalog.Read, graph dump.Graph, declarations []dump.Event, tables []table.DumpRelation, indexIndices map[ids.OID]int, replay *ConstraintReplay, deferDefaults bool) error {
	lateTables := dumpLateIndexTables(view)
	lateIndexes := make(map[ids.OID]bool)
	for oid := range indexIndices {
		idx := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
		lateIndexes[oid] = idx.SQLIndex.Constraint == nil && lateTables[idx.SQLIndex.Parent] || preservedReplayBacking(view, idx) || index.PartitionPreservedReplayLocal(view, idx) || index.PartitionReplayVirtualUniqueRoot(view, idx) != nil || index.PartitionReplayDeferredExclusion(view, idx)
		if root := index.PartitionReplayVirtualUniqueRoot(view, idx); root != nil && !index.PartitionReplayOnlyExclusion(view, root) {
			virtualUniqueMembershipReady(r, view, graph, dump.Event{OID: oid}, view.MustRelation(idx.SQLIndex.Parent), idx)
		}
	}
	membership := func(table *catalog.Relation) dump.Event {
		if DumpNeedsAttach(view, table) {
			return dump.Event{OID: table.Address().ObjectID, Phase: dump.TableAttachment}
		}
		return dump.Event{OID: table.Address().ObjectID}
	}
	for _, t := range tables {
		d := t.Descriptor
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
				if child != d.Address() && !exclusionDefaultBefore(view, d, view.MustRelation(child)) {
					graph.Add(event, membership(view.MustRelation(child)))
				}
			}
		}
	}
	for oid := range indexIndices {
		if !lateIndexes[oid] {
			continue
		}
		idx := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
		table := view.MustRelation(idx.SQLIndex.Parent)
		// ONLY's gap-before/covered-after policy below owns these edges.
		// Recursive all-memberships-before would create the reverse edge for
		// complete-invalid ONLY roots and immediately close a two-node cycle.
		if table.Partition == nil || index.PartitionReplayOnlyExclusion(view, idx) {
			continue
		}
		for _, child := range table.Partition.Children {
			backing := index.PartitionDumpIndexBacking(view, idx.Address(), child)
			if idx.SQLIndex.Valid || index.PartitionIndexPreservedInvalid(view, idx) || backing == nil || !backing.SQLIndex.Valid {
				graph.Add(dump.Event{OID: oid}, membership(view.MustRelation(child)))
			}
		}
	}
	lateDeclarations := make(map[ids.OID]bool)
	for _, event := range declarations {
		lateDeclarations[event.OID] = lateIndexes[event.OID]
		if _, exists := indexIndices[event.OID]; exists {
			idx := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: event.OID})
			if index.PartitionReplayOnlyExclusion(view, idx) {
				lateDeclarations[event.OID] = true
			}
		}
		if descriptor, ok := view.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: event.OID}); ok && descriptor.Membership != nil && !DumpNeedsAttach(view, descriptor) {
			parent := view.MustRelation(descriptor.Membership.Parent)
			if freshReplayDelaysMembership(view, parent) {
				lateDeclarations[event.OID] = true
			}
			for _, idx := range index.PartitionIndexDeclarationOrder(view, parent) {
				if root := index.PartitionReplayVirtualUniqueRoot(view, idx); root != nil && index.PartitionReplayOnlyExclusion(view, root) && index.PartitionDumpIndexBacking(view, idx.Address(), descriptor.Address()) != nil {
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
	freshNameReady := make(map[ids.ObjectAddress]dump.Event)
	nameReady := func(address ids.ObjectAddress) (dump.Event, bool, error) {
		address.SubID = 0
		if _, exists := replay.Temporary[address]; exists {
			idx := view.MustRelation(address)
			if root := index.PartitionReplayVirtualUniqueRoot(view, idx); root != nil && !index.PartitionReplayOnlyExclusion(view, root) {
				return virtualNameReady[root.Address()], true, nil
			}
			return dump.Event{OID: address.ObjectID, Phase: dump.Rename}, true, nil
		}
		oid, err := r.Predecessor(view, address, ids.ObjectAddress{})
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
	renameOrder := replay.FinalRenameOrder()
	for _, idx := range renameOrder {
		provider, err := r.Predecessor(view, idx.Address(), ids.ObjectAddress{})
		if err != nil {
			return err
		}
		event := dump.Event{OID: idx.Address().ObjectID, Phase: dump.Rename}
		creator := constraintCreator(r, view, idx)
		graph[event] = []dump.Event{earlyTail, creator}
		lateNames[idx.Address().ObjectID] = lateDeclarations[provider] || creator.Phase != dump.Declaration
		if origin := index.PartitionReplayFreshOrigin(view, idx); origin != nil {
			if previous := freshNameReady[origin.Address()]; previous != (dump.Event{}) {
				graph.Add(event, previous)
			}
			freshNameReady[origin.Address()] = event
		}
		if root := index.PartitionReplayVirtualUniqueRoot(view, idx); root != nil && !index.PartitionReplayOnlyExclusion(view, root) {
			// A definition creates the whole local tree. Complete its terminal
			// names in captured logical order before any original-name consumer,
			// independent of reminted root/leaf allocation order.
			if previous := virtualNameReady[root.Address()]; previous != (dump.Event{}) {
				graph.Add(event, previous)
			}
			virtualNameReady[root.Address()] = event
		}
		if root := index.PartitionReplayVirtualUniqueRoot(view, idx); root != nil && index.PartitionReplayOnlyExclusion(view, root) {
			lateNames[idx.Address().ObjectID] = true
		}
		if lateNames[idx.Address().ObjectID] {
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
		if !lateNames[idx.Address().ObjectID] {
			graph.Add(event, ready)
			ready = event
		}
	}
	// Independent equivalent locals cannot participate in inline construction.
	// Keep their separate declarations after the existing early definitions and
	// names, not interleaved with them by newly minted backing OIDs.
	for oid := range indexIndices {
		idx := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
		if index.PartitionReplayDeferredExclusion(view, idx) {
			graph.Add(dump.Event{OID: oid}, ready)
		}
	}
	// A later recursive ancestor adopts an already declared local virtual
	// origin. Its terminal name follows that origin's completed naming subtree,
	// not whichever reminted index OID wins the scheduler tie. Early physical
	// provider names remain before their table ATTACH as required above.
	for _, idx := range renameOrder {
		end, exists := virtualNameReady[idx.Address()]
		if !exists || idx.SQLIndex.PartitionParent == (ids.ObjectAddress{}) {
			continue
		}
		c := index.ExclusionDefinition(view.MustRelation(idx.SQLIndex.Parent), idx)
		if c == nil || !c.NoInherit {
			continue
		}
		parent := view.MustRelation(idx.SQLIndex.PartitionParent)
		root := index.PartitionReplayVirtualUniqueRoot(view, parent)
		if root != nil && !index.PartitionReplayOnlyExclusion(view, root) {
			graph.Add(dump.Event{OID: root.Address().ObjectID, Phase: dump.Rename}, end)
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
		oid := t.Descriptor.Address().ObjectID
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
		if err := t.RowReplayReferences(ctx, view, collect(&sources, &late)); err != nil {
			return err
		}
		// Rerouted hash rows can land in any descendant, not just their captured
		// physical leaf. All relevant memberships must precede this row event.
		if target := index.PartitionHashReplayTarget(view, t.Descriptor); target != t.Descriptor {
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
		target := membership(t.Descriptor)
		clear(seen)
		after := false
		if target.Phase == dump.TableAttachment {
			after = graph.DependsOn(dump.Event{OID: oid}, target, seen)
			for _, source := range sources {
				after = after || graph.DependsOn(source, target, seen)
			}
		}
		rowAfterMembership[oid] = target.Phase == dump.TableAttachment && after
		if ancestor := freshReplayAncestor(view, t.Descriptor); ancestor != nil {
			late = true
			rowAfterMembership[oid] = target.Phase == dump.TableAttachment
			sources = append(sources, freshCompletion(view, ancestor))
		}
		for _, constraint := range t.Descriptor.Constraints.Exclusion {
			idx := view.MustRelation(constraint.Index)
			// Scratch creation can mint an ancestor backing after this physical
			// table. Finish that actual origin's terminal naming group before
			// any of its row consumers; table/index allocation order is not a
			// canonical ordering boundary.
			if origin := index.PartitionReplayFreshOrigin(view, idx); origin != nil {
				end, exists := freshNameReady[origin.Address()]
				if !exists {
					return fmt.Errorf("stuntgres: missing fresh exclusion replay terminal name")
				}
				sources = append(sources, end)
			}
			if root := index.PartitionReplayVirtualUniqueRoot(view, idx); root != nil && index.PartitionReplayOnlyExclusion(view, root) {
				late = true
				rowAfterMembership[oid] = target.Phase == dump.TableAttachment
			}
		}
		rowSources[oid], lateRows[oid] = sources, late
		sources, late = nil, lateRows[oid]
		for _, check := range t.Descriptor.Constraints.Check {
			if !check.NotValid {
				continue
			}
			add := collect(&sources, &late)
			if err := regobj.ClassReplayReferences(view, check.Expression, add); err != nil {
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
			for attribute, definition := range t.Defaults {
				if !t.DeferredDefinitions[attribute] {
					continue
				}
				add := collect(&sources, &late)
				if err := regobj.ClassReplayReferences(view, definition.Expression, add); err != nil {
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

	byOID := make(map[ids.OID]*table.DumpRelation, len(tables))
	for i := range tables {
		byOID[tables[i].Descriptor.Address().ObjectID] = &tables[i]
	}
	for _, declaration := range declarations {
		t := byOID[declaration.OID]
		if t == nil || !deferDefaults || len(t.DeferredDefinitions) == 0 {
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
		if t == nil || !t.HasInvalidConstraints() || lateConstraints[declaration.OID] {
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
				graph.Add(event, membership(t.Descriptor))
			}
		}
		if t.HasInvalidConstraints() && lateConstraints[oid] {
			event := dump.Event{OID: oid, Phase: dump.InvalidConstraints}
			graph[event] = append([]dump.Event{{OID: oid, Phase: dump.Rows}, declaration}, constraintSources[oid]...)
			if checkAfterMembership[oid] {
				graph.Add(event, membership(t.Descriptor))
			}
		}
		complete := dump.Event{OID: oid, Phase: dump.TableCompletion}
		graph[complete] = []dump.Event{{OID: oid, Phase: dump.Rows}}
		if t.HasInvalidConstraints() {
			graph.Add(complete, dump.Event{OID: oid, Phase: dump.InvalidConstraints})
		}
		if deferDefaults && len(t.DeferredDefinitions) != 0 {
			graph.Add(complete, dump.Event{OID: oid, Phase: dump.Defaults})
		}
		if DumpNeedsAttach(view, t.Descriptor) {
			event := membership(t.Descriptor)
			graph[event] = []dump.Event{declaration, invalidTail, {OID: t.Descriptor.Membership.Parent.ObjectID}}
			if !rowAfterMembership[oid] {
				graph.Add(event, dump.Event{OID: oid, Phase: dump.Rows})
			}
			if t.HasInvalidConstraints() && !checkAfterMembership[oid] {
				graph.Add(event, dump.Event{OID: oid, Phase: dump.InvalidConstraints})
			}
			if t.Descriptor.Partition != nil {
				if replayTopologyReady(view, t.Descriptor) {
					for _, child := range t.Descriptor.Partition.Children {
						graph.Add(event, membership(view.MustRelation(child)))
					}
				} else {
					graph.Add(event, complete)
				}
			}
			parent := view.MustRelation(t.Descriptor.Membership.Parent)
			if t.Descriptor.Membership.DefaultBound {
				for _, child := range parent.Partition.Children {
					if child != t.Descriptor.Address() && !exclusionDefaultBefore(view, t.Descriptor, view.MustRelation(child)) {
						graph.Add(event, membership(view.MustRelation(child)))
					}
				}
			}
			for indexOID := range indexIndices {
				idx := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: indexOID})
				backing := index.PartitionDumpIndexBacking(view, idx.Address(), t.Descriptor.Address())
				before := !lateIndexes[indexOID] || !idx.SQLIndex.Valid && !index.PartitionIndexPreservedInvalid(view, idx) && backing != nil && backing.SQLIndex.Valid
				if index.PartitionReplayOnlyExclusion(view, idx) && backing == nil {
					before = false
				}
				if idx.SQLIndex.Parent == t.Descriptor.Address() && !preservedReplayBacking(view, idx) && !index.PartitionPreservedReplayLocal(view, idx) && index.PartitionReplayVirtualUniqueRoot(view, idx) == nil || idx.SQLIndex.Parent == parent.Address() && before {
					graph.Add(event, dump.Event{OID: indexOID})
				}
			}
			for _, constraint := range t.Descriptor.Constraints.Unique {
				idx := view.MustRelation(constraint.Index)
				// This local provider does not exist until after the preserving
				// reattachment. Its terminal name remains a consumer predecessor,
				// but cannot be a prerequisite of the initial table membership.
				if index.PartitionPreservedReplayLocal(view, idx) || index.PartitionReplayVirtualUniqueRoot(view, idx) != nil {
					continue
				}
				if _, exists := replay.Temporary[constraint.Index]; exists {
					graph.Add(event, dump.Event{OID: constraint.Index.ObjectID, Phase: dump.Rename})
				}
			}
			for _, constraint := range t.Descriptor.Constraints.Exclusion {
				idx := view.MustRelation(constraint.Index)
				if !constraint.NoInherit || index.PartitionReplayVirtualUniqueRoot(view, idx) != nil {
					continue
				}
				if _, exists := replay.Temporary[constraint.Index]; exists {
					graph.Add(event, dump.Event{OID: constraint.Index.ObjectID, Phase: dump.Rename})
				}
			}
		}
		if t.Descriptor.Partition != nil {
			for _, child := range t.Descriptor.Partition.Children {
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
		idx, ok := descriptor.(*catalog.Relation)
		if !ok || idx.SQLIndex == nil || idx.SQLIndex.PartitionParent == (ids.ObjectAddress{}) {
			continue
		}
		parent := view.MustRelation(idx.SQLIndex.PartitionParent)
		table := view.MustRelation(idx.SQLIndex.Parent)
		explicit := index.PartitionReplayExplicitExclusion(view, idx)
		if !explicit && (parent.SQLIndex.Constraint != nil || DumpNeedsAttach(view, table) && !lateIndexes[parent.Address().ObjectID]) {
			continue // Ordinary ATTACH links this captured backing itself.
		}
		event := dump.Event{OID: idx.Address().ObjectID, Phase: dump.IndexAttachment}
		graph[event] = []dump.Event{membership(table)}
		for _, address := range []ids.ObjectAddress{idx.Address(), parent.Address()} {
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
		idx := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
		if !index.PartitionReplayOnlyExclusion(view, idx) {
			continue
		}
		table := view.MustRelation(idx.SQLIndex.Parent)
		for _, address := range table.Partition.Children {
			child := membership(view.MustRelation(address))
			declaration := dump.Event{OID: oid}
			backing := index.PartitionDumpIndexBacking(view, idx.Address(), address)
			if backing == nil || index.PartitionReplayExplicitExclusion(view, backing) {
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
	for _, idx := range renameOrder {
		if index.PartitionReplayFreshOrigin(view, idx) != nil {
			onlyTables[idx.SQLIndex.Parent] = true
		}
		if root := index.PartitionReplayVirtualUniqueRoot(view, idx); root != nil && index.PartitionReplayOnlyExclusion(view, root) {
			onlyTables[idx.SQLIndex.Parent] = true
		}
	}
	var names []dump.Event
	var creators []dump.Event
	for _, index := range renameOrder {
		if !onlyTables[index.SQLIndex.Parent] || !lateNames[index.Address().ObjectID] {
			continue
		}
		names = append(names, dump.Event{OID: index.Address().ObjectID, Phase: dump.Rename})
		creator := constraintCreator(r, view, index)
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
	if err := table.InheritanceRecipeEvents(ctx, view, graph, declarations, tables, nameReady); err != nil {
		return err
	}
	table.RetiredConstraintEvents(view, graph, tables)
	if err := table.InheritedForeignKeyEvents(view, graph, tables, nameReady); err != nil {
		return err
	}
	return ctx.Err()
}
