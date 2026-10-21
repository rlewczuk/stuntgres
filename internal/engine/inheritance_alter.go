package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// One statement-owned stage carries every candidate; the ordinary provider
// pipeline still owns cooking, row/index rewrites and physical validation.
// Nothing is installed in the transaction owner or live catalog between nodes.
type inheritanceAlterState struct {
	table.InheritanceAlterPlan
	stage   *CatalogStage
	changes *session.Journal
	// prepared holds the root's prepared nodes, keyed by relation.
	prepared map[ids.ObjectAddress]*inheritancePreparedNode
}

func (e *Engine) alterInheritanceLocked(scope *StatementScope, op *session.Operation, auth AuthorizationView, raw *parser.AlterTable, count int, tablespace ids.ObjectRef, types []table.AlterTypePreparation) (Result, error) {
	root := scope.lockedDescriptor.(*catalog.Relation)
	actions := raw.Actions
	if err := table.InheritanceAlterActionsGuard(auth.Catalog, root, actions); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	children := catalog.InheritanceChildren(auth.Catalog, root.Address())
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
		actions, err = table.InheritanceOnlyActions(root, len(children) != 0, actions)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	preparatory := table.InheritancePreparatoryLocks(actions, raw.Only)
	recurse := preparatory || slices.ContainsFunc(actions, func(a parser.AlterTableAction) bool {
		return table.InheritanceRecursiveAction(root, a, raw.Only) || a.Action == parser.DropColumn || a.Action == parser.DropNotNull || a.Action == parser.DropConstraint
	})
	targets := []*catalog.Relation{root}
	if recurse {
		targets = table.InheritanceAlterOrder(auth.Catalog, root)
	}
	original := scope.statementMutation
	var stage *CatalogStage
	state := &inheritanceAlterState{changes: scope.owner.Catalog.Clone(), InheritanceAlterPlan: table.InheritanceAlterPlan{Root: root, Targets: targets, Selected: map[ids.ObjectAddress]bool{}, Only: raw.Only, Actions: actions, Events: make(map[ids.ObjectAddress][]table.InheritanceAlterEvent)}}
	state.Tablespace = tablespace
	state.Types = types
	scope.inheritance.alter = state
	scope.catalog = state.changes
	e.mu.Unlock()
	defer func() {
		scope.statementMutation = original
		scope.inheritance.alter = nil
		e.DiscardCatalogStage(stage)
	}()
	// Definition preparation and every required physical grant precede the sole
	// stage. Preparatory grants remain distinct from actually visited owners.
	if err := e.admitInheritanceAlter(scope, op, auth, raw, preparatory); err != nil {
		return Result{}, err
	}
	if preparatory && table.InheritancePureDefinitionPreparation(actions) {
		if err := e.admitInheritanceAlter(scope, op, auth, raw, false); err != nil {
			return Result{}, err
		}
	}
	state.Events = make(map[ids.ObjectAddress][]table.InheritanceAlterEvent)
	state.Selected = make(map[ids.ObjectAddress]bool)
	e.mu.Lock()
	current := e.privateCatalogLocked(scope.owner.Catalog)
	stage = e.BeginCatalogStage(current)
	stage.authorizeLocked(auth, op.Session)
	state.stage = stage
	e.mu.Unlock()
	for index, old := range state.Targets {
		if index != 0 && len(table.InheritanceParentActions(&state.InheritanceAlterPlan, old)) == 0 {
			continue
		}
		e.mu.Lock()
		current := e.privateCatalogLocked(scope.owner.Catalog)
		if current.Get(old.Address()) != old {
			e.mu.Unlock()
			return Result{}, ids.ErrCatalogReplaced
		}
		currentAuth, err := e.statementAuthorizationLocked(op, raw)
		refForOwner := original.lockedRef
		refForOwner.Address = old.Address()
		incoming := actions
		if index != 0 {
			incoming = table.InheritanceParentActions(&state.InheritanceAlterPlan, old)
		}
		if err == nil && (index == 0 || table.InheritanceEntryOwner(incoming)) {
			err = currentAuth.Check(access.Requirement{Object: refForOwner, OwnerOnly: true})
		}
		e.mu.Unlock()
		if err != nil {
			return Result{}, err
		}
		state.Selected[old.Address()] = true
		ref := original.lockedRef
		ref.Address = old.Address()
		state.Descendant = index != 0
		state.Graph = catalog.CloneInheritance(old.Inheritance)
		planned := actions
		if cached := state.prepared[old.Address()]; cached != nil {
			planned = cached.actions
			state.Graph = catalog.CloneInheritance(cached.graph)
		} else if state.Descendant {
			var err error
			planned, state.Graph, err = e.inheritedAlterActions(state, old)
			if err != nil {
				return Result{}, err
			}
		}
		if index != 0 && !table.InheritanceEntryOwner(incoming) && len(planned) != 0 {
			e.mu.Lock()
			err = currentAuth.Check(access.Requirement{Object: refForOwner, OwnerOnly: true})
			e.mu.Unlock()
			if err != nil {
				return Result{}, err
			}
		}
		scope.lockedRef, scope.lockedDescriptor = ref, old
		e.mu.Lock()
		scope.lockedStorage = e.privateStorageLocked(scope.owner.Catalog, old.Address())
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
		command.Name = parser.ObjectName{Parts: []parser.Identifier{{Text: auth.Catalog.Get(old.Namespace()).Name()}, {Text: old.Name()}}}
		command.Actions = planned
		command.Only = true
		childAuth := auth
		childAuth.Catalog = e.stageRead(stage)
		childAuth.Recapture(childAuth.Catalog)
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
	for _, old := range state.Targets {
		if state.Selected[old.Address()] && e.privateCatalogLocked(scope.owner.Catalog).Get(old.Address()) != old {
			e.mu.Unlock()
			return Result{}, ids.ErrCatalogReplaced
		}
	}
	for address, d := range stage.Descriptors() {
		e.catalogChangeLocked(state.changes, address, d)
	}
	for address := range stage.Removals() {
		e.catalogChangeLocked(state.changes, address, nil)
	}
	for address, rebound := range stage.rowShapes {
		state.changes.Storage[address] = session.StorageChange{Original: rebound.original, Root: rebound.original.Versions, Ends: rebound.observedEnds, Generation: rebound.generation, Replacement: rebound.replacement, Relation: stage.Get(address).(*catalog.Relation)}
	}
	scope.catalog = state.changes
	e.mu.Unlock()
	emitViewCascade(op, stage.dropCascadeNames)
	return Result{}, nil
}

func (e *Engine) inheritedAlterActions(state *inheritanceAlterState, old *catalog.Relation) ([]parser.AlterTableAction, *catalog.TableInheritance, error) {
	graph := catalog.CloneInheritance(old.Inheritance)
	view := state.Definition.Base
	if state.stage != nil {
		view = e.stageRead(state.stage)
	}
	var actions []parser.AlterTableAction
	droppedColumns := make(map[string]bool)
	for _, a := range table.InheritanceParentActions(&state.InheritanceAlterPlan, old) {
		switch a.Action {
		case parser.AddColumn:
			if state.Only {
				continue
			}
			name := a.Definition.Name
			_, existing := old.Index[name]
			existing = existing && !droppedColumns[name]
			if existing {
				slot := old.Index[name]
				for _, parentAddress := range graph.Parents {
					parent := state.DefinitionRelation(view, parentAddress)
					if pslot, ok := parent.Index[name]; ok {
						if err := analyze.InheritanceColumnConflict(view, old.Columns[slot], parent.Columns[pslot], true); err != nil {
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
				if table.InheritanceRemovedOrigin(&state.InheritanceAlterPlan, origin, parser.DropColumn) {
					removed = true
				} else {
					origins = append(origins, origin)
				}
			}
			provenance.Origins = origins
			if state.Only && removed {
				provenance.Local = true
			}
			graph.Columns[attribute] = provenance
			if provenance.Local || len(origins) != 0 {
				table.InheritRetiredColumnConstraints(&state.InheritanceAlterPlan, state.retiredBase(), old, graph, attribute)
			}
			if !provenance.Local && len(origins) == 0 {
				actions = append(actions, a)
				droppedColumns[a.Column.Text] = true
			}
		case parser.AlterColumnType:
			if state.Only {
				continue
			}
			slot, exists := old.Index[a.Column.Text]
			if !exists {
				continue
			}
			for _, origin := range graph.Columns[old.Attributes[slot]].Origins {
				if !state.Selected[origin.Parent] {
					return nil, nil, sqlError("42P16", fmt.Sprintf(`cannot alter inherited column "%s" of relation "%s"`, a.Column.Text, old.Name()))
				}
			}
			actions = append(actions, a)
		case parser.SetExpression, parser.DropExpression:
			if !state.Only {
				actions = append(actions, a)
			}
		case parser.SetNotNull, parser.AddNotNull:
			if state.Only || a.NoInherit {
				continue
			}
			actions = append(actions, a)
		case parser.DropNotNull, parser.DropConstraint:
			matched := false
			for _, c := range old.Constraints.NotNull {
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
					return table.InheritanceRemovedOrigin(&state.InheritanceAlterPlan, origin, parser.DropNotNull)
				})
				if state.Only && provenance.Count() == 0 {
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
			for _, c := range old.Constraints.Check {
				if c.Name == a.Constraint.Text {
					provenance := graph.Checks[c.Name]
					provenance.Origins = slices.DeleteFunc(slices.Clone(provenance.Origins), func(origin catalog.InheritanceOrigin) bool {
						return table.InheritanceRemovedOrigin(&state.InheritanceAlterPlan, origin, parser.DropConstraint)
					})
					if state.Only && provenance.Count() == 0 {
						provenance.Local = true
					}
					graph.Checks[c.Name] = provenance
					if !provenance.Local && provenance.Count() == 0 {
						actions = append(actions, a)
					}
				}
			}
		case parser.AddCheck:
			if state.Only || a.Check.NoInherit {
				continue
			}
			copy := *a.Check
			a.Check = &copy
			actions = append(actions, a)
		case parser.AddUnique:
			if !state.Only && a.Unique.Primary {
				for _, column := range a.Unique.Columns {
					if c := table.InheritedNotNull(view, graph.Parents, column.Text); c != nil {
						actions = append(actions, parser.AlterTableAction{Action: parser.AddNotNull, Column: column, Constraint: parser.Identifier{Text: c.Name}, NotValid: c.NotValid})
					}
				}
			}
		}
	}
	return actions, graph, nil
}

func (scope *StatementScope) finishInheritanceAlterCandidate(old, next *catalog.Relation) (*catalog.Relation, error) {
	state := scope.inheritance.alter
	if state == nil || old.Inheritance == nil {
		return next, nil
	}
	graph := state.Graph
	if !state.Descendant && next.Inheritance != nil {
		graph = catalog.CloneInheritance(next.Inheritance)
	}
	view := scope.engine().stageRead(state.stage)
	for i, attribute := range next.Attributes {
		provenance, exists := graph.Columns[attribute]
		if !exists {
			provenance.Local = !state.Descendant
		}
		if !state.Descendant {
			for _, a := range state.Actions {
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
		if state.Descendant {
			var origins []catalog.InheritanceOrigin
			for _, parentAddress := range graph.Parents {
				parent := view.MustRelation(parentAddress)
				if slot, ok := parent.Index[next.Columns[i].Name]; ok {
					origins = append(origins, catalog.InheritanceOrigin{Parent: parentAddress, Attribute: parent.Attributes[slot]})
				}
			}
			provenance.Origins = origins
		}
		if state.Descendant {
			var err error
			provenance, err = table.RetainInheritedRecipes(view, next, attribute, provenance)
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
	for _, c := range next.Constraints.Check {
		provenance, exists := graph.Checks[c.Name]
		if !exists {
			provenance.Local = !state.Descendant
		}
		if !state.Descendant {
			for _, action := range state.Actions {
				if action.Action == parser.AddCheck && action.Check.Name.Text == c.Name {
					provenance.Local = true
				}
			}
		}
		if state.Descendant {
			var origins []catalog.InheritanceOrigin
			for _, address := range graph.Parents {
				parent := view.MustRelation(address)
				for _, pc := range parent.Constraints.Check {
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
		if !slices.ContainsFunc(next.Constraints.Check, func(c catalog.CheckConstraint) bool { return c.Name == name }) {
			delete(graph.Checks, name)
		}
	}
	for _, c := range next.Constraints.NotNull {
		provenance, exists := graph.NotNull[c.Attribute]
		if !exists {
			provenance.Local = !state.Descendant
		}
		if !state.Descendant {
			for _, action := range state.Actions {
				if (action.Action == parser.SetNotNull || action.Action == parser.AddNotNull) && action.Column.Text == next.Columns[next.IndexForAttribute(c.Attribute)].Name {
					provenance.Local = true
				}
			}
		}
		if state.Descendant {
			var origins []catalog.InheritanceOrigin
			name := next.Columns[next.IndexForAttribute(c.Attribute)].Name
			for _, address := range graph.Parents {
				parent := view.MustRelation(address)
				if slot, ok := parent.Index[name]; ok {
					for _, pc := range parent.Constraints.NotNull {
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
		if !slices.ContainsFunc(next.Constraints.NotNull, func(c catalog.NotNullConstraint) bool { return c.Attribute == attribute }) {
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
