package engine

import (
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// One statement-owned stage carries every candidate; the ordinary provider
// pipeline still owns cooking, row/index rewrites and physical validation.
// Nothing is installed in the transaction owner or live catalog between nodes.
type inheritanceAlterState struct {
	stage      *CatalogStage
	changes    *catalogChanges
	root       *relationDescriptor
	targets    []*relationDescriptor
	selected   map[ids.ObjectAddress]bool
	graph      *tableInheritance
	descendant bool
	only       bool
	actions    []parser.AlterTableAction
	events     map[ids.ObjectAddress][]inheritanceAlterEvent
	validating map[string]bool
	preparing  bool
	definition inheritanceDefinitionReader
	prepared   map[ids.ObjectAddress]*inheritancePreparedNode
	tablespace ids.ObjectRef          // Prepared root-only destination; never propagated.
	types      []alterTypePreparation // Original root TYPE occurrences, consumed on root re-entry.
}

func cloneInheritance(graph *tableInheritance) *tableInheritance {
	if graph == nil {
		return nil
	}
	next := *graph
	next.parents = slices.Clone(graph.parents)
	next.columns = maps.Clone(graph.columns)
	next.checks = maps.Clone(graph.checks)
	next.notNull = maps.Clone(graph.notNull)
	return &next
}

// Unlike the OF-type entry, ordinary propagation includes its named root.
func inheritanceAlterOrder(catalog CatalogRead, root *relationDescriptor) []*relationDescriptor {
	selected := map[ids.ObjectAddress]*relationDescriptor{}
	var collect func(*relationDescriptor)
	collect = func(d *relationDescriptor) {
		if selected[d.address] != nil {
			return
		}
		selected[d.address] = d
		for _, child := range inheritanceChildren(catalog, d.address) {
			collect(child)
		}
	}
	collect(root)
	var result []*relationDescriptor
	seen := map[ids.ObjectAddress]bool{}
	var visit func(*relationDescriptor)
	visit = func(d *relationDescriptor) {
		if seen[d.address] {
			return
		}
		seen[d.address] = true
		if d.inheritance != nil {
			for _, parent := range d.inheritance.parents {
				if p := selected[parent]; p != nil {
					visit(p)
				}
			}
		}
		result = append(result, d)
	}
	addresses := make([]ids.ObjectAddress, 0, len(selected))
	for address := range selected {
		addresses = append(addresses, address)
	}
	slices.SortFunc(addresses, func(a, b ids.ObjectAddress) int {
		if a.ObjectID < b.ObjectID {
			return -1
		}
		if a.ObjectID > b.ObjectID {
			return 1
		}
		return 0
	})
	for _, address := range addresses {
		visit(selected[address])
	}
	return result
}

func inheritanceRecursiveAction(root *relationDescriptor, a parser.AlterTableAction, only bool) bool {
	switch a.Action {
	case parser.SetTablespace:
		return false // Assignment is always root-only, independent of ONLY/star.
	case parser.AddColumn, parser.DropColumn, parser.AlterColumnType:
		return true
	case parser.SetExpression, parser.DropExpression:
		return !only
	case parser.SetNotNull, parser.AddNotNull:
		if a.NoInherit {
			return false
		}
		if slot, ok := root.index[a.Column.Text]; ok {
			for _, c := range root.notNullConstraints {
				if c.Attribute == root.attributes[slot] && (!c.NotValid || a.Action == parser.AddNotNull) {
					return false
				}
			}
		}
		return true
	case parser.DropNotNull:
		if slot, ok := root.index[a.Column.Text]; ok {
			for _, c := range root.notNullConstraints {
				if c.Attribute == root.attributes[slot] {
					return !c.NoInherit
				}
			}
		}
	case parser.DropConstraint:
		for _, c := range root.notNullConstraints {
			if c.Name == a.Constraint.Text {
				return !c.NoInherit
			}
		}
		for _, c := range root.checkConstraints {
			if c.Name == a.Constraint.Text {
				return !c.NoInherit
			}
		}
	case parser.AddCheck:
		for _, c := range root.checkConstraints {
			if a.Check.Name.Text != "" && c.Name == a.Check.Name.Text {
				return false
			}
		}
		return !a.Check.NoInherit
	case parser.AddUsingIndex:
		return a.UsingIndex.Primary
	case parser.AddUnique:
		if a.Unique.Primary {
			for _, column := range a.Unique.Columns {
				if slot, ok := root.index[column.Text]; !ok || root.columns[slot].Nullable {
					return true
				}
			}
		}
	}
	return false
}

func inheritanceOnlyActions(root *relationDescriptor, children bool, actions []parser.AlterTableAction) ([]parser.AlterTableAction, error) {
	if !children {
		return actions, nil
	}
	next := slices.Clone(actions)
	for i, a := range next {
		switch a.Action {
		case parser.AddColumn:
			return nil, sqlError("42P16", "column must be added to child tables too")
		case parser.AlterColumnType:
			return nil, sqlError("42P16", fmt.Sprintf(`type of inherited column "%s" must be changed in child tables too`, a.Column.Text))
		case parser.AddCheck:
			existing := slices.ContainsFunc(root.checkConstraints, func(c checkConstraint) bool { return c.Name == a.Check.Name.Text })
			if !a.Check.NoInherit && !existing {
				return nil, sqlError("42P16", "constraint must be added to child tables too")
			}
		case parser.AddNotNull:
			existing := false
			if slot, ok := root.index[a.Column.Text]; ok {
				existing = slices.ContainsFunc(root.notNullConstraints, func(c notNullConstraint) bool { return c.Attribute == root.attributes[slot] })
			}
			if !a.NoInherit && !existing {
				return nil, sqlError("42P16", "constraint must be added to child tables too")
			}
		case parser.SetNotNull:
			// Retain ONLY intent for an existing invalid fact too; validation cannot
			// silently stop above children. Existing valid facts remain genuine no-ops.
			next[i].NoInherit = true
		case parser.DropExpression:
			return nil, sqlError("0A000", "ALTER TABLE / DROP EXPRESSION must be applied to child tables too")
		}
	}
	return next, nil
}

func (e *Engine) alterInheritanceLocked(scope *StatementScope, op *Operation, auth AuthorizationView, raw *parser.AlterTable, count int, tablespace ids.ObjectRef, types []alterTypePreparation) (Result, error) {
	root := scope.lockedDescriptor.(*relationDescriptor)
	actions := raw.Actions
	if err := inheritanceAlterActionsGuard(auth.catalog, root, actions); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	children := inheritanceChildren(auth.catalog, root.address)
	if raw.Only {
		for _, a := range raw.Actions {
			if a.Action == parser.AddUnique && a.Unique.Primary && len(children) != 0 {
				for _, column := range a.Unique.Columns {
					if slot, ok := root.index[column.Text]; ok && root.columns[slot].Nullable {
						e.mu.Unlock()
						return Result{}, sqlError("XX000", fmt.Sprintf(`column "%s" of table "%s" is not marked NOT NULL`, column.Text, children[0].name))
					}
				}
			}
		}
		var err error
		actions, err = inheritanceOnlyActions(root, len(children) != 0, actions)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	preparatory := inheritancePreparatoryLocks(actions, raw.Only)
	recurse := preparatory || slices.ContainsFunc(actions, func(a parser.AlterTableAction) bool {
		return inheritanceRecursiveAction(root, a, raw.Only) || a.Action == parser.DropColumn || a.Action == parser.DropNotNull || a.Action == parser.DropConstraint
	})
	targets := []*relationDescriptor{root}
	if recurse {
		targets = inheritanceAlterOrder(auth.catalog, root)
	}
	original := scope.statementMutation
	var stage *CatalogStage
	state := &inheritanceAlterState{changes: cloneCatalogChanges(scope.owner.catalog), root: root, targets: targets, selected: map[ids.ObjectAddress]bool{}, only: raw.Only, actions: actions, events: make(map[ids.ObjectAddress][]inheritanceAlterEvent)}
	state.tablespace = tablespace
	state.types = types
	scope.inheritanceAlter = state
	scope.catalog = state.changes
	e.mu.Unlock()
	defer func() { scope.statementMutation = original; scope.inheritanceAlter = nil; e.DiscardCatalogStage(stage) }()
	// Definition preparation and every required physical grant precede the sole
	// stage. Preparatory grants remain distinct from actually visited owners.
	if err := e.admitInheritanceAlter(scope, op, auth, raw, preparatory); err != nil {
		return Result{}, err
	}
	if preparatory && inheritancePureDefinitionPreparation(actions) {
		if err := e.admitInheritanceAlter(scope, op, auth, raw, false); err != nil {
			return Result{}, err
		}
	}
	state.events = make(map[ids.ObjectAddress][]inheritanceAlterEvent)
	state.selected = make(map[ids.ObjectAddress]bool)
	e.mu.Lock()
	current := e.privateCatalogLocked(scope.owner.catalog)
	stage = e.BeginCatalogStage(current)
	stage.authorizeLocked(auth, op.session)
	state.stage = stage
	e.mu.Unlock()
	for index, old := range state.targets {
		if index != 0 && len(inheritanceParentActions(state, old)) == 0 {
			continue
		}
		e.mu.Lock()
		current := e.privateCatalogLocked(scope.owner.catalog)
		if current.state.descriptors[old.address] != old {
			e.mu.Unlock()
			return Result{}, ErrCatalogReplaced
		}
		currentAuth, err := e.statementAuthorizationLocked(op, raw)
		refForOwner := original.lockedRef
		refForOwner.Address = old.address
		incoming := actions
		if index != 0 {
			incoming = inheritanceParentActions(state, old)
		}
		if err == nil && (index == 0 || inheritanceEntryOwner(incoming)) {
			err = currentAuth.Check(AccessRequirement{Object: refForOwner, OwnerOnly: true})
		}
		e.mu.Unlock()
		if err != nil {
			return Result{}, err
		}
		state.selected[old.address] = true
		ref := original.lockedRef
		ref.Address = old.address
		state.descendant = index != 0
		state.graph = cloneInheritance(old.inheritance)
		planned := actions
		if cached := state.prepared[old.address]; cached != nil {
			planned = cached.actions
			state.graph = cloneInheritance(cached.graph)
		} else if state.descendant {
			var err error
			planned, state.graph, err = e.inheritedAlterActions(state, old)
			if err != nil {
				return Result{}, err
			}
		}
		if index != 0 && !inheritanceEntryOwner(incoming) && len(planned) != 0 {
			e.mu.Lock()
			err = currentAuth.Check(AccessRequirement{Object: refForOwner, OwnerOnly: true})
			e.mu.Unlock()
			if err != nil {
				return Result{}, err
			}
		}
		scope.lockedRef, scope.lockedDescriptor = ref, old
		e.mu.Lock()
		scope.lockedStorage = e.privateStorageLocked(scope.owner.catalog, old.address)
		e.mu.Unlock()
		if len(planned) == 0 {
			next, err := scope.finishInheritanceAlterCandidate(old, old)
			if err != nil {
				return Result{}, err
			}
			if next != old {
				if err := stage.Replace(next); err != nil {
					return Result{}, err
				}
			}
			continue
		}
		command := *raw
		command.Name = parser.ObjectName{Parts: []parser.Identifier{{Text: auth.catalog.state.descriptors[old.namespace].Name()}, {Text: old.name}}}
		command.Actions = planned
		command.Only = true
		childAuth := auth
		childAuth.catalog = e.stageRead(stage)
		childAuth.recaptureCatalog(childAuth.catalog)
		e.mu.Lock()
		if _, err := e.alterTableLocked(scope, op, childAuth, &command, count); err != nil {
			return Result{}, err
		}
	}
	scope.statementMutation = original
	if err := scope.acquireDefinitionProviders(stage); err != nil {
		return Result{}, err
	}
	if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	if err := scope.validateLockedRelation(e); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	for _, old := range state.targets {
		if state.selected[old.address] && e.privateCatalogLocked(scope.owner.catalog).state.descriptors[old.address] != old {
			e.mu.Unlock()
			return Result{}, ErrCatalogReplaced
		}
	}
	for address, d := range stage.descriptors {
		e.catalogChangeLocked(state.changes, address, d)
	}
	for address := range stage.removals {
		e.catalogChangeLocked(state.changes, address, nil)
	}
	for address, rebound := range stage.rowShapes {
		state.changes.storage[address] = catalogStorageChange{original: rebound.original, root: rebound.original.versions, ends: rebound.observedEnds, generation: rebound.generation, replacement: rebound.replacement, relation: stage.descriptors[address].(*relationDescriptor)}
	}
	scope.catalog = state.changes
	e.mu.Unlock()
	emitViewCascade(op, stage.viewCascadeNames)
	return Result{}, nil
}

func (e *Engine) inheritedAlterActions(state *inheritanceAlterState, old *relationDescriptor) ([]parser.AlterTableAction, *tableInheritance, error) {
	graph := cloneInheritance(old.inheritance)
	view := state.definition.base
	if state.stage != nil {
		view = e.stageRead(state.stage)
	}
	var actions []parser.AlterTableAction
	droppedColumns := make(map[string]bool)
	for _, a := range inheritanceParentActions(state, old) {
		switch a.Action {
		case parser.AddColumn:
			if state.only {
				continue
			}
			name := a.Definition.Name
			_, existing := old.index[name]
			existing = existing && !droppedColumns[name]
			if existing {
				slot := old.index[name]
				for _, parentAddress := range graph.parents {
					parent := state.definitionRelation(view, parentAddress)
					if pslot, ok := parent.index[name]; ok {
						if err := inheritanceColumnConflict(view, old.columns[slot], parent.columns[pslot], true); err != nil {
							return nil, nil, sqlError("42804", fmt.Sprintf(`child table "%s" has different type for column "%s"`, old.name, name))
						}
					}
				}
			}
			definition := *a.Definition
			definition.Constraints = nil
			for _, constraint := range a.Definition.Constraints {
				if constraint.NotNull || constraint.Check != nil || constraint.Unique != nil || constraint.ForeignKey != nil {
					continue
				}
				definition.Constraints = append(definition.Constraints, constraint)
			}
			if !existing {
				a.Definition = &definition
				actions = append(actions, a)
			}
		case parser.DropColumn:
			slot, exists := old.index[a.Column.Text]
			if !exists {
				continue
			}
			attribute := old.attributes[slot]
			provenance := graph.columns[attribute]
			var origins []inheritanceOrigin
			removed := false
			for _, origin := range provenance.origins {
				if inheritanceRemovedOrigin(state, origin, parser.DropColumn) {
					removed = true
				} else {
					origins = append(origins, origin)
				}
			}
			provenance.origins = origins
			if state.only && removed {
				provenance.local = true
			}
			graph.columns[attribute] = provenance
			if provenance.local || len(origins) != 0 {
				inheritRetiredColumnConstraints(state, old, graph, attribute)
			}
			if !provenance.local && len(origins) == 0 {
				actions = append(actions, a)
				droppedColumns[a.Column.Text] = true
			}
		case parser.AlterColumnType:
			if state.only {
				continue
			}
			slot, exists := old.index[a.Column.Text]
			if !exists {
				continue
			}
			for _, origin := range graph.columns[old.attributes[slot]].origins {
				if !state.selected[origin.parent] {
					return nil, nil, sqlError("42P16", fmt.Sprintf(`cannot alter inherited column "%s" of relation "%s"`, a.Column.Text, old.name))
				}
			}
			actions = append(actions, a)
		case parser.SetExpression, parser.DropExpression:
			if !state.only {
				actions = append(actions, a)
			}
		case parser.SetNotNull, parser.AddNotNull:
			if state.only || a.NoInherit {
				continue
			}
			actions = append(actions, a)
		case parser.DropNotNull, parser.DropConstraint:
			matched := false
			for _, c := range old.notNullConstraints {
				match := a.Action == parser.DropConstraint && c.Name == a.Constraint.Text
				if a.Action == parser.DropNotNull {
					if slot, ok := old.index[a.Column.Text]; ok {
						match = c.Attribute == old.attributes[slot]
					}
				}
				if !match {
					continue
				}
				matched = true
				provenance := graph.notNull[c.Attribute]
				provenance.origins = slices.DeleteFunc(slices.Clone(provenance.origins), func(origin inheritanceOrigin) bool {
					return inheritanceRemovedOrigin(state, origin, parser.DropNotNull)
				})
				if state.only && provenance.count() == 0 {
					provenance.local = true
				}
				graph.notNull[c.Attribute] = provenance
				if !provenance.local && provenance.count() == 0 {
					actions = append(actions, parser.AlterTableAction{Action: parser.DropConstraint, Constraint: parser.Identifier{Text: c.Name}, Mode: a.Mode})
				}
			}
			if matched || a.Action == parser.DropNotNull {
				continue
			}
			for _, c := range old.checkConstraints {
				if c.Name == a.Constraint.Text {
					provenance := graph.checks[c.Name]
					provenance.origins = slices.DeleteFunc(slices.Clone(provenance.origins), func(origin inheritanceOrigin) bool {
						return inheritanceRemovedOrigin(state, origin, parser.DropConstraint)
					})
					if state.only && provenance.count() == 0 {
						provenance.local = true
					}
					graph.checks[c.Name] = provenance
					if !provenance.local && provenance.count() == 0 {
						actions = append(actions, a)
					}
				}
			}
		case parser.AddCheck:
			if state.only || a.Check.NoInherit {
				continue
			}
			copy := *a.Check
			a.Check = &copy
			actions = append(actions, a)
		case parser.AddUnique:
			if !state.only && a.Unique.Primary {
				for _, column := range a.Unique.Columns {
					if c := inheritedNotNull(view, graph.parents, column.Text); c != nil {
						actions = append(actions, parser.AlterTableAction{Action: parser.AddNotNull, Column: column, Constraint: parser.Identifier{Text: c.Name}, NotValid: c.NotValid})
					}
				}
			}
		}
	}
	return actions, graph, nil
}

func inheritedNotNull(view CatalogRead, parents []ids.ObjectAddress, name string) *notNullConstraint {
	for _, address := range parents {
		parent := view.state.descriptors[address].(*relationDescriptor)
		if slot, exists := parent.index[name]; exists {
			for i := range parent.notNullConstraints {
				c := &parent.notNullConstraints[i]
				if c.Attribute == parent.attributes[slot] && !c.NoInherit {
					return c
				}
			}
		}
	}
	return nil
}

func (scope *StatementScope) finishInheritanceAlterCandidate(old, next *relationDescriptor) (*relationDescriptor, error) {
	state := scope.inheritanceAlter
	if state == nil || old.inheritance == nil {
		return next, nil
	}
	graph := state.graph
	if !state.descendant && next.inheritance != nil {
		graph = cloneInheritance(next.inheritance)
	}
	view := scope.op.session.Engine().stageRead(state.stage)
	for i, attribute := range next.attributes {
		provenance, exists := graph.columns[attribute]
		if !exists {
			provenance.local = !state.descendant
		}
		if !state.descendant {
			for _, a := range state.actions {
				if a.Action == parser.AddColumn && a.Definition.Name == next.columns[i].Name {
					for _, constraint := range a.Definition.Constraints {
						provenance.explicitDefault = provenance.explicitDefault || constraint.Default != nil
						provenance.explicitGeneration = provenance.explicitGeneration || constraint.Generation != nil
					}
				}
				if a.Action == parser.SetExpression && a.Column.Text == next.columns[i].Name {
					provenance.explicitGeneration = true
				}
			}
		}
		if state.descendant {
			var origins []inheritanceOrigin
			for _, parentAddress := range graph.parents {
				parent := view.state.descriptors[parentAddress].(*relationDescriptor)
				if slot, ok := parent.index[next.columns[i].Name]; ok {
					origins = append(origins, inheritanceOrigin{parent: parentAddress, attribute: parent.attributes[slot]})
				}
			}
			provenance.origins = origins
		}
		if state.descendant {
			var err error
			provenance, err = retainInheritedRecipes(view, next, attribute, provenance)
			if err != nil {
				return nil, err
			}
		}
		graph.columns[attribute] = provenance
	}
	for attribute := range graph.columns {
		if !next.hasAttribute(attribute) {
			delete(graph.columns, attribute)
			delete(graph.notNull, attribute)
		}
	}
	for _, c := range next.checkConstraints {
		provenance, exists := graph.checks[c.Name]
		if !exists {
			provenance.local = !state.descendant
		}
		if !state.descendant {
			for _, action := range state.actions {
				if action.Action == parser.AddCheck && action.Check.Name.Text == c.Name {
					provenance.local = true
				}
			}
		}
		if state.descendant {
			var origins []inheritanceOrigin
			for _, address := range graph.parents {
				parent := view.state.descriptors[address].(*relationDescriptor)
				for _, pc := range parent.checkConstraints {
					if pc.Name == c.Name && !pc.NoInherit {
						origins = append(origins, inheritanceOrigin{parent: address, constraint: pc.OID})
					}
				}
			}
			provenance.origins = origins
		}
		graph.checks[c.Name] = provenance
	}
	for name := range graph.checks {
		if !slices.ContainsFunc(next.checkConstraints, func(c checkConstraint) bool { return c.Name == name }) {
			delete(graph.checks, name)
		}
	}
	for _, c := range next.notNullConstraints {
		provenance, exists := graph.notNull[c.Attribute]
		if !exists {
			provenance.local = !state.descendant
		}
		if !state.descendant {
			for _, action := range state.actions {
				if (action.Action == parser.SetNotNull || action.Action == parser.AddNotNull) && action.Column.Text == next.columns[next.indexForAttribute(c.Attribute)].Name {
					provenance.local = true
				}
			}
		}
		if state.descendant {
			var origins []inheritanceOrigin
			name := next.columns[next.indexForAttribute(c.Attribute)].Name
			for _, address := range graph.parents {
				parent := view.state.descriptors[address].(*relationDescriptor)
				if slot, ok := parent.index[name]; ok {
					for _, pc := range parent.notNullConstraints {
						if pc.Attribute == parent.attributes[slot] && !pc.NoInherit {
							origins = append(origins, inheritanceOrigin{parent: address, attribute: pc.Attribute, constraint: pc.OID})
						}
					}
				}
			}
			provenance.origins = origins
		}
		graph.notNull[c.Attribute] = provenance
	}
	for attribute := range graph.notNull {
		if !slices.ContainsFunc(next.notNullConstraints, func(c notNullConstraint) bool { return c.Attribute == attribute }) {
			delete(graph.notNull, attribute)
		}
	}
	copy := *next
	copy.inheritance = graph
	if copy.revision == old.revision {
		copy.revision++
	}
	return &copy, nil
}
