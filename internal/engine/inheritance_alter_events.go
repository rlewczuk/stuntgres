package engine

import (
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Events describe actual provider-phase changes, not the final presence of a
// same-named fact. A DROP followed by ADD must remove the old contribution even
// though the parent's completed candidate already contains its replacement.
type inheritanceAlterEvent struct {
	action         parser.AlterTableAction
	attribute      int32
	constraint     ids.OID
	validation     bool
	retiredNotNull []notNullConstraint
	retiredChecks  []checkConstraint
}

func (scope *StatementScope) recordInheritanceAlterAction(before, after *relationDescriptor, a parser.AlterTableAction) {
	state := scope.inheritanceAlter
	if state == nil {
		return
	}
	events := state.events[before.Address()]
	switch a.Action {
	case parser.AddColumn:
		if slot, exists := after.Index[a.Definition.Name]; exists {
			if _, old := before.Index[a.Definition.Name]; !old {
				events = append(events, inheritanceAlterEvent{action: a, attribute: after.Attributes[slot]})
			}
		}
	case parser.DropColumn:
		for slot, attribute := range before.Attributes {
			if after.hasAttribute(attribute) {
				continue
			}
			action := a
			action.Column = parser.Identifier{Text: before.Columns[slot].Name}
			event := inheritanceAlterEvent{action: action, attribute: attribute}
			for _, c := range before.NotNullConstraints {
				if c.Attribute == attribute && !slices.ContainsFunc(after.NotNullConstraints, func(next notNullConstraint) bool { return next.OID == c.OID }) {
					event.retiredNotNull = append(event.retiredNotNull, c)
				}
			}
			for _, c := range before.CheckConstraints {
				if !slices.ContainsFunc(after.CheckConstraints, func(next checkConstraint) bool { return next.OID == c.OID }) && slices.ContainsFunc(c.Dependencies, func(dependency expressionDependency) bool {
					return dependency.Ref.Address == ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: before.Address().ObjectID, SubID: attribute}
				}) {
					event.retiredChecks = append(event.retiredChecks, c)
				}
			}
			events = append(events, event)
		}
	case parser.DropConstraint, parser.DropNotNull:
		for _, old := range before.NotNullConstraints {
			if !old.NoInherit && !slices.ContainsFunc(after.NotNullConstraints, func(c notNullConstraint) bool { return c.Attribute == old.Attribute }) {
				action := a
				action.Action = parser.DropNotNull
				action.Column = parser.Identifier{Text: before.Columns[before.indexForAttribute(old.Attribute)].Name}
				events = append(events, inheritanceAlterEvent{action: action, attribute: old.Attribute, constraint: old.OID})
			}
		}
		for _, old := range before.CheckConstraints {
			if !old.NoInherit && !slices.ContainsFunc(after.CheckConstraints, func(c checkConstraint) bool { return c.OID == old.OID }) {
				action := a
				action.Action = parser.DropConstraint
				action.Constraint = parser.Identifier{Text: old.Name}
				events = append(events, inheritanceAlterEvent{action: action, constraint: old.OID})
			}
		}
	case parser.AlterColumnType, parser.SetExpression, parser.DropExpression:
		if !state.only {
			events = append(events, inheritanceAlterEvent{action: a})
		}
	}
	if !state.only {
		for _, next := range after.NotNullConstraints {
			if next.NoInherit {
				continue
			}
			index := slices.IndexFunc(before.NotNullConstraints, func(old notNullConstraint) bool { return old.Attribute == next.Attribute })
			validation := index >= 0 && before.NotNullConstraints[index].NotValid && !next.NotValid
			if index >= 0 && !validation {
				continue
			}
			action := parser.AlterTableAction{Action: parser.AddNotNull, Column: parser.Identifier{Text: after.Columns[after.indexForAttribute(next.Attribute)].Name}, Constraint: parser.Identifier{Text: next.Name}, NotValid: next.NotValid}
			if validation || a.Action == parser.SetNotNull {
				action.Action = parser.SetNotNull
			}
			events = append(events, inheritanceAlterEvent{action: action, attribute: next.Attribute, validation: validation})
		}
	}
	state.events[before.Address()] = events
}

func (scope *StatementScope) recordInheritanceChecks(before, after *relationDescriptor, raw []*parser.CheckConstraint, start int) {
	state := scope.inheritanceAlter
	if state == nil || state.only {
		return
	}
	for _, c := range after.CheckConstraints {
		if c.NoInherit || c.Ordinal < start || slices.ContainsFunc(before.CheckConstraints, func(old checkConstraint) bool { return old.OID == c.OID }) {
			continue
		}
		index := c.Ordinal - start
		if index < 0 || index >= len(raw) {
			continue
		}
		definition := *raw[index]
		definition.Name = parser.Identifier{Text: c.Name}
		state.events[before.Address()] = append(state.events[before.Address()], inheritanceAlterEvent{action: parser.AlterTableAction{Action: parser.AddCheck, Check: &definition}, constraint: c.OID})
	}
}

func inheritanceEventName(a parser.AlterTableAction) string {
	switch a.Action {
	case parser.AddColumn:
		return a.Definition.Name
	case parser.AddCheck:
		return a.Check.Name.Text
	case parser.DropConstraint:
		return a.Constraint.Text
	}
	return a.Column.Text
}

func inheritanceParentActions(state *inheritanceAlterState, old *relationDescriptor) []parser.AlterTableAction {
	var actions []parser.AlterTableAction
	state.validating = nil
	if old.Inheritance == nil {
		return actions
	}
	for _, parent := range old.Inheritance.Parents {
		for _, event := range state.events[parent] {
			a := event.action
			if a.Action == parser.DropColumn || a.Action == parser.DropNotNull || a.Action == parser.DropConstraint {
				var origins []inheritanceOrigin
				if a.Action == parser.DropConstraint {
					origins = old.Inheritance.Checks[a.Constraint.Text].Origins
				} else if slot, ok := old.Index[a.Column.Text]; ok {
					if a.Action == parser.DropColumn {
						origins = old.Inheritance.Columns[old.Attributes[slot]].Origins
					} else {
						origins = old.Inheritance.NotNull[old.Attributes[slot]].Origins
					}
				}
				reached := slices.ContainsFunc(origins, func(origin inheritanceOrigin) bool {
					return origin.Parent == parent && ((a.Action == parser.DropColumn && origin.Attribute == event.attribute) || (a.Action != parser.DropColumn && origin.Constraint == event.constraint))
				})
				if !reached {
					continue
				}
			}
			if state.only && a.Action != parser.DropColumn && a.Action != parser.DropConstraint && a.Action != parser.DropNotNull {
				continue
			}
			if !slices.ContainsFunc(actions, func(previous parser.AlterTableAction) bool {
				return previous.Action == a.Action && inheritanceEventName(previous) == inheritanceEventName(a)
			}) {
				actions = append(actions, a)
			}
			if event.validation {
				if state.validating == nil {
					state.validating = make(map[string]bool)
				}
				state.validating[a.Column.Text] = true
			}
		}
	}
	// Different parents can have emitted their events in different command lists.
	// Keep the incumbent provider order (including DROP before recreate).
	slices.SortStableFunc(actions, func(a, b parser.AlterTableAction) int {
		return inheritanceActionPass(a.Action) - inheritanceActionPass(b.Action)
	})
	return actions
}

func inheritanceActionPass(action parser.AlterTableActionKind) int {
	switch action {
	case parser.DropIdentity, parser.DropNotNull, parser.DropConstraint, parser.DropColumn, parser.DropExpression:
		return 0
	case parser.AlterColumnType:
		return 1
	case parser.AddColumn:
		return 2
	case parser.SetExpression:
		return 3
	case parser.SetNotNull:
		return 4
	case parser.AddUnique, parser.AddExclusion, parser.AddUsingIndex:
		return 5
	case parser.AddNotNull, parser.AddCheck:
		return 6
	case parser.AddIdentity:
		return 7
	default:
		return 8
	}
}

func inheritanceRemovedOrigin(state *inheritanceAlterState, origin inheritanceOrigin, kind parser.AlterTableActionKind) bool {
	for _, event := range state.events[origin.Parent] {
		if event.action.Action != kind {
			continue
		}
		if kind == parser.DropColumn && event.attribute == origin.Attribute {
			return true
		}
		if kind == parser.DropNotNull && event.constraint == origin.Constraint {
			return true
		}
		if kind == parser.DropConstraint && event.constraint == origin.Constraint {
			return true
		}
	}
	return false
}

func inheritancePreparatoryLocks(actions []parser.AlterTableAction, only bool) bool {
	if only {
		return false
	}
	for _, a := range actions {
		switch a.Action {
		case parser.AddCheck, parser.AddNotNull, parser.AddUnique, parser.AddForeignKey, parser.AddExclusion, parser.AlterColumnType, parser.SetExpression, parser.DropExpression:
			return true
		}
	}
	return false
}

func (scope *StatementScope) inheritanceNotNullMode(column string) notNullRecursion {
	state := scope.inheritanceAlter
	if state != nil && state.descendant {
		if state.validating[column] {
			return notNullValidation
		}
		return notNullInherited
	}
	return notNullDirect
}

// ONLY's fresh-fact prohibition uses the actual phase input, including earlier
// DROP effects. Existing compatible facts can still merge/localize genuinely.
func (scope *StatementScope) inheritanceOnlyCreatedConstraint(before, after *relationDescriptor) error {
	state := scope.inheritanceAlter
	if state == nil || !state.only || state.descendant || len(state.targets) < 2 {
		return nil
	}
	for _, fact := range after.NotNullConstraints {
		if !fact.NoInherit && !slices.ContainsFunc(before.NotNullConstraints, func(old notNullConstraint) bool { return old.Attribute == fact.Attribute }) {
			return sqlError("42P16", "constraint must be added to child tables too")
		}
	}
	for _, fact := range after.CheckConstraints {
		if !fact.NoInherit && !slices.ContainsFunc(before.CheckConstraints, func(old checkConstraint) bool { return old.Name == fact.Name }) {
			return sqlError("42P16", "constraint must be added to child tables too")
		}
	}
	return nil
}
