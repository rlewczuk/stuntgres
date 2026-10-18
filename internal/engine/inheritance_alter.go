package engine

import (
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// One statement-owned stage carries every candidate; the ordinary provider
// pipeline still owns cooking, row/index rewrites and physical validation.
// Nothing is installed in the transaction owner or live catalog between nodes.
type inheritanceAlterState struct {
	stage      *CatalogStage
	changes    *catalogChanges
	root       *catalog.Relation
	targets    []*catalog.Relation
	selected   map[ids.ObjectAddress]bool
	graph      *catalog.TableInheritance
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

func cloneInheritance(graph *catalog.TableInheritance) *catalog.TableInheritance {
	if graph == nil {
		return nil
	}
	next := *graph
	next.Parents = slices.Clone(graph.Parents)
	next.Columns = maps.Clone(graph.Columns)
	next.Checks = maps.Clone(graph.Checks)
	next.NotNull = maps.Clone(graph.NotNull)
	return &next
}

// Unlike the OF-type entry, ordinary propagation includes its named root.
func inheritanceAlterOrder(view CatalogRead, root *catalog.Relation) []*catalog.Relation {
	selected := map[ids.ObjectAddress]*catalog.Relation{}
	var collect func(*catalog.Relation)
	collect = func(d *catalog.Relation) {
		if selected[d.Address()] != nil {
			return
		}
		selected[d.Address()] = d
		for _, child := range inheritanceChildren(view, d.Address()) {
			collect(child)
		}
	}
	collect(root)
	var result []*catalog.Relation
	seen := map[ids.ObjectAddress]bool{}
	var visit func(*catalog.Relation)
	visit = func(d *catalog.Relation) {
		if seen[d.Address()] {
			return
		}
		seen[d.Address()] = true
		if d.Inheritance != nil {
			for _, parent := range d.Inheritance.Parents {
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

func inheritanceRecursiveAction(root *catalog.Relation, a parser.AlterTableAction, only bool) bool {
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
		if slot, ok := root.Index[a.Column.Text]; ok {
			for _, c := range root.NotNullConstraints {
				if c.Attribute == root.Attributes[slot] && (!c.NotValid || a.Action == parser.AddNotNull) {
					return false
				}
			}
		}
		return true
	case parser.DropNotNull:
		if slot, ok := root.Index[a.Column.Text]; ok {
			for _, c := range root.NotNullConstraints {
				if c.Attribute == root.Attributes[slot] {
					return !c.NoInherit
				}
			}
		}
	case parser.DropConstraint:
		for _, c := range root.NotNullConstraints {
			if c.Name == a.Constraint.Text {
				return !c.NoInherit
			}
		}
		for _, c := range root.CheckConstraints {
			if c.Name == a.Constraint.Text {
				return !c.NoInherit
			}
		}
	case parser.AddCheck:
		for _, c := range root.CheckConstraints {
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
				if slot, ok := root.Index[column.Text]; !ok || root.Columns[slot].Nullable {
					return true
				}
			}
		}
	}
	return false
}

func inheritanceOnlyActions(root *catalog.Relation, children bool, actions []parser.AlterTableAction) ([]parser.AlterTableAction, error) {
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
			existing := slices.ContainsFunc(root.CheckConstraints, func(c catalog.CheckConstraint) bool { return c.Name == a.Check.Name.Text })
			if !a.Check.NoInherit && !existing {
				return nil, sqlError("42P16", "constraint must be added to child tables too")
			}
		case parser.AddNotNull:
			existing := false
			if slot, ok := root.Index[a.Column.Text]; ok {
				existing = slices.ContainsFunc(root.NotNullConstraints, func(c catalog.NotNullConstraint) bool { return c.Attribute == root.Attributes[slot] })
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
	root := scope.lockedDescriptor.(*catalog.Relation)
	actions := raw.Actions
	if err := inheritanceAlterActionsGuard(auth.catalog, root, actions); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	children := inheritanceChildren(auth.catalog, root.Address())
	if raw.Only {
		for _, a := range raw.Actions {
			if a.Action == parser.AddUnique && a.Unique.Primary && len(children) != 0 {
				for _, column := range a.Unique.Columns {
					if slot, ok := root.Index[column.Text]; ok && root.Columns[slot].Nullable {
						e.mu.Unlock()
						return Result{}, sqlError("XX000", fmt.Sprintf(`column "%s" of table "%s" is not marked NOT NULL`, column.Text, children[0].Name()))
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
	targets := []*catalog.Relation{root}
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
		if current.Get(old.Address()) != old {
			e.mu.Unlock()
			return Result{}, ids.ErrCatalogReplaced
		}
		currentAuth, err := e.statementAuthorizationLocked(op, raw)
		refForOwner := original.lockedRef
		refForOwner.Address = old.Address()
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
		state.selected[old.Address()] = true
		ref := original.lockedRef
		ref.Address = old.Address()
		state.descendant = index != 0
		state.graph = cloneInheritance(old.Inheritance)
		planned := actions
		if cached := state.prepared[old.Address()]; cached != nil {
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
		scope.lockedStorage = e.privateStorageLocked(scope.owner.catalog, old.Address())
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
		command.Name = parser.ObjectName{Parts: []parser.Identifier{{Text: auth.catalog.Get(old.Namespace()).Name()}, {Text: old.Name()}}}
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
		if state.selected[old.Address()] && e.privateCatalogLocked(scope.owner.catalog).Get(old.Address()) != old {
			e.mu.Unlock()
			return Result{}, ids.ErrCatalogReplaced
		}
	}
	for address, d := range stage.Descriptors {
		e.catalogChangeLocked(state.changes, address, d)
	}
	for address := range stage.Removals {
		e.catalogChangeLocked(state.changes, address, nil)
	}
	for address, rebound := range stage.rowShapes {
		state.changes.storage[address] = catalogStorageChange{original: rebound.original, root: rebound.original.versions, ends: rebound.observedEnds, generation: rebound.generation, replacement: rebound.replacement, relation: stage.Descriptors[address].(*catalog.Relation)}
	}
	scope.catalog = state.changes
	e.mu.Unlock()
	emitViewCascade(op, stage.viewCascadeNames)
	return Result{}, nil
}

func (e *Engine) inheritedAlterActions(state *inheritanceAlterState, old *catalog.Relation) ([]parser.AlterTableAction, *catalog.TableInheritance, error) {
	graph := cloneInheritance(old.Inheritance)
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
			_, existing := old.Index[name]
			existing = existing && !droppedColumns[name]
			if existing {
				slot := old.Index[name]
				for _, parentAddress := range graph.Parents {
					parent := state.definitionRelation(view, parentAddress)
					if pslot, ok := parent.Index[name]; ok {
						if err := inheritanceColumnConflict(view, old.Columns[slot], parent.Columns[pslot], true); err != nil {
							return nil, nil, sqlError("42804", fmt.Sprintf(`child table "%s" has different type for column "%s"`, old.Name(), name))
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
			slot, exists := old.Index[a.Column.Text]
			if !exists {
				continue
			}
			attribute := old.Attributes[slot]
			provenance := graph.Columns[attribute]
			var origins []catalog.InheritanceOrigin
			removed := false
			for _, origin := range provenance.Origins {
				if inheritanceRemovedOrigin(state, origin, parser.DropColumn) {
					removed = true
				} else {
					origins = append(origins, origin)
				}
			}
			provenance.Origins = origins
			if state.only && removed {
				provenance.Local = true
			}
			graph.Columns[attribute] = provenance
			if provenance.Local || len(origins) != 0 {
				inheritRetiredColumnConstraints(state, old, graph, attribute)
			}
			if !provenance.Local && len(origins) == 0 {
				actions = append(actions, a)
				droppedColumns[a.Column.Text] = true
			}
		case parser.AlterColumnType:
			if state.only {
				continue
			}
			slot, exists := old.Index[a.Column.Text]
			if !exists {
				continue
			}
			for _, origin := range graph.Columns[old.Attributes[slot]].Origins {
				if !state.selected[origin.Parent] {
					return nil, nil, sqlError("42P16", fmt.Sprintf(`cannot alter inherited column "%s" of relation "%s"`, a.Column.Text, old.Name()))
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
			for _, c := range old.NotNullConstraints {
				match := a.Action == parser.DropConstraint && c.Name == a.Constraint.Text
				if a.Action == parser.DropNotNull {
					if slot, ok := old.Index[a.Column.Text]; ok {
						match = c.Attribute == old.Attributes[slot]
					}
				}
				if !match {
					continue
				}
				matched = true
				provenance := graph.NotNull[c.Attribute]
				provenance.Origins = slices.DeleteFunc(slices.Clone(provenance.Origins), func(origin catalog.InheritanceOrigin) bool {
					return inheritanceRemovedOrigin(state, origin, parser.DropNotNull)
				})
				if state.only && provenance.Count() == 0 {
					provenance.Local = true
				}
				graph.NotNull[c.Attribute] = provenance
				if !provenance.Local && provenance.Count() == 0 {
					actions = append(actions, parser.AlterTableAction{Action: parser.DropConstraint, Constraint: parser.Identifier{Text: c.Name}, Mode: a.Mode})
				}
			}
			if matched || a.Action == parser.DropNotNull {
				continue
			}
			for _, c := range old.CheckConstraints {
				if c.Name == a.Constraint.Text {
					provenance := graph.Checks[c.Name]
					provenance.Origins = slices.DeleteFunc(slices.Clone(provenance.Origins), func(origin catalog.InheritanceOrigin) bool {
						return inheritanceRemovedOrigin(state, origin, parser.DropConstraint)
					})
					if state.only && provenance.Count() == 0 {
						provenance.Local = true
					}
					graph.Checks[c.Name] = provenance
					if !provenance.Local && provenance.Count() == 0 {
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
					if c := inheritedNotNull(view, graph.Parents, column.Text); c != nil {
						actions = append(actions, parser.AlterTableAction{Action: parser.AddNotNull, Column: column, Constraint: parser.Identifier{Text: c.Name}, NotValid: c.NotValid})
					}
				}
			}
		}
	}
	return actions, graph, nil
}

func inheritedNotNull(view CatalogRead, parents []ids.ObjectAddress, name string) *catalog.NotNullConstraint {
	for _, address := range parents {
		parent := view.MustRelation(address)
		if slot, exists := parent.Index[name]; exists {
			for i := range parent.NotNullConstraints {
				c := &parent.NotNullConstraints[i]
				if c.Attribute == parent.Attributes[slot] && !c.NoInherit {
					return c
				}
			}
		}
	}
	return nil
}

func (scope *StatementScope) finishInheritanceAlterCandidate(old, next *catalog.Relation) (*catalog.Relation, error) {
	state := scope.inheritanceAlter
	if state == nil || old.Inheritance == nil {
		return next, nil
	}
	graph := state.graph
	if !state.descendant && next.Inheritance != nil {
		graph = cloneInheritance(next.Inheritance)
	}
	view := scope.op.session.Engine().stageRead(state.stage)
	for i, attribute := range next.Attributes {
		provenance, exists := graph.Columns[attribute]
		if !exists {
			provenance.Local = !state.descendant
		}
		if !state.descendant {
			for _, a := range state.actions {
				if a.Action == parser.AddColumn && a.Definition.Name == next.Columns[i].Name {
					for _, constraint := range a.Definition.Constraints {
						provenance.ExplicitDefault = provenance.ExplicitDefault || constraint.Default != nil
						provenance.ExplicitGeneration = provenance.ExplicitGeneration || constraint.Generation != nil
					}
				}
				if a.Action == parser.SetExpression && a.Column.Text == next.Columns[i].Name {
					provenance.ExplicitGeneration = true
				}
			}
		}
		if state.descendant {
			var origins []catalog.InheritanceOrigin
			for _, parentAddress := range graph.Parents {
				parent := view.MustRelation(parentAddress)
				if slot, ok := parent.Index[next.Columns[i].Name]; ok {
					origins = append(origins, catalog.InheritanceOrigin{Parent: parentAddress, Attribute: parent.Attributes[slot]})
				}
			}
			provenance.Origins = origins
		}
		if state.descendant {
			var err error
			provenance, err = retainInheritedRecipes(view, next, attribute, provenance)
			if err != nil {
				return nil, err
			}
		}
		graph.Columns[attribute] = provenance
	}
	for attribute := range graph.Columns {
		if !next.HasAttribute(attribute) {
			delete(graph.Columns, attribute)
			delete(graph.NotNull, attribute)
		}
	}
	for _, c := range next.CheckConstraints {
		provenance, exists := graph.Checks[c.Name]
		if !exists {
			provenance.Local = !state.descendant
		}
		if !state.descendant {
			for _, action := range state.actions {
				if action.Action == parser.AddCheck && action.Check.Name.Text == c.Name {
					provenance.Local = true
				}
			}
		}
		if state.descendant {
			var origins []catalog.InheritanceOrigin
			for _, address := range graph.Parents {
				parent := view.MustRelation(address)
				for _, pc := range parent.CheckConstraints {
					if pc.Name == c.Name && !pc.NoInherit {
						origins = append(origins, catalog.InheritanceOrigin{Parent: address, Constraint: pc.OID})
					}
				}
			}
			provenance.Origins = origins
		}
		graph.Checks[c.Name] = provenance
	}
	for name := range graph.Checks {
		if !slices.ContainsFunc(next.CheckConstraints, func(c catalog.CheckConstraint) bool { return c.Name == name }) {
			delete(graph.Checks, name)
		}
	}
	for _, c := range next.NotNullConstraints {
		provenance, exists := graph.NotNull[c.Attribute]
		if !exists {
			provenance.Local = !state.descendant
		}
		if !state.descendant {
			for _, action := range state.actions {
				if (action.Action == parser.SetNotNull || action.Action == parser.AddNotNull) && action.Column.Text == next.Columns[next.IndexForAttribute(c.Attribute)].Name {
					provenance.Local = true
				}
			}
		}
		if state.descendant {
			var origins []catalog.InheritanceOrigin
			name := next.Columns[next.IndexForAttribute(c.Attribute)].Name
			for _, address := range graph.Parents {
				parent := view.MustRelation(address)
				if slot, ok := parent.Index[name]; ok {
					for _, pc := range parent.NotNullConstraints {
						if pc.Attribute == parent.Attributes[slot] && !pc.NoInherit {
							origins = append(origins, catalog.InheritanceOrigin{Parent: address, Attribute: pc.Attribute, Constraint: pc.OID})
						}
					}
				}
			}
			provenance.Origins = origins
		}
		graph.NotNull[c.Attribute] = provenance
	}
	for attribute := range graph.NotNull {
		if !slices.ContainsFunc(next.NotNullConstraints, func(c catalog.NotNullConstraint) bool { return c.Attribute == attribute }) {
			delete(graph.NotNull, attribute)
		}
	}
	copy := *next
	copy.Inheritance = graph
	if copy.Revision() == old.Revision() {
		copy.ObjectRevision++
	}
	return &copy, nil
}
