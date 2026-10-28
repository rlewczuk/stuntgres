package table

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// RecordAction records the inherited events of one action applied to before.
// It does nothing when plan is nil (not recursing).
func (plan *InheritanceAlterPlan) RecordAction(before, after *catalog.Relation, a parser.AlterTableAction) {
	state := plan
	if state == nil {
		return
	}
	events := state.Events[before.Address()]
	switch a.Action {
	case parser.SetStorage:
		if !state.Only {
			events = append(events, InheritanceAlterEvent{Action: a, Attribute: after.Attributes[after.Index[a.Column.Text]]})
		}
	case parser.ValidateConstraint:
		selected, err := SelectAlterConstraintValidation(before, a.Constraint.Text)
		if err == nil && !state.Only && (selected.Recursive() || state.Descendant) {
			if selected.Check != nil && !selected.Check.NoInherit {
				events = append(events, InheritanceAlterEvent{Action: a, Constraint: selected.OID})
			}
			if selected.NotNull != nil && !selected.NotNull.NoInherit {
				a.Column = parser.Identifier{Text: before.Columns[before.IndexForAttribute(selected.NotNull.Attribute)].Name}
				events = append(events, InheritanceAlterEvent{Action: a, Attribute: selected.NotNull.Attribute, Constraint: selected.OID})
			}
		}
		state.Events[before.Address()] = events
		return
	case parser.AddColumn:
		if slot, exists := after.Index[a.Definition.Name]; exists {
			if _, old := before.Index[a.Definition.Name]; !old {
				events = append(events, InheritanceAlterEvent{Action: a, Attribute: after.Attributes[slot]})
			}
		}
	case parser.DropColumn:
		for slot, attribute := range before.Attributes {
			if after.HasAttribute(attribute) {
				continue
			}
			action := a
			action.Column = parser.Identifier{Text: before.Columns[slot].Name}
			event := InheritanceAlterEvent{Action: action, Attribute: attribute}
			for _, c := range before.Constraints.NotNull {
				if c.Attribute == attribute && !slices.ContainsFunc(after.Constraints.NotNull, func(next catalog.NotNullConstraint) bool { return next.OID == c.OID }) {
					event.RetiredNotNull = append(event.RetiredNotNull, c)
				}
			}
			for _, c := range before.Constraints.Check {
				if !slices.ContainsFunc(after.Constraints.Check, func(next catalog.CheckConstraint) bool { return next.OID == c.OID }) && slices.ContainsFunc(c.Dependencies, func(dependency catalog.ExpressionDependency) bool {
					return dependency.Ref.Address == ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: before.Address().ObjectID, SubID: attribute}
				}) {
					event.RetiredChecks = append(event.RetiredChecks, c)
				}
			}
			events = append(events, event)
		}
	case parser.DropConstraint, parser.DropNotNull:
		for _, old := range before.Constraints.NotNull {
			if !old.NoInherit && !slices.ContainsFunc(after.Constraints.NotNull, func(c catalog.NotNullConstraint) bool { return c.Attribute == old.Attribute }) {
				action := a
				action.Action = parser.DropNotNull
				action.Column = parser.Identifier{Text: before.Columns[before.IndexForAttribute(old.Attribute)].Name}
				events = append(events, InheritanceAlterEvent{Action: action, Attribute: old.Attribute, Constraint: old.OID})
			}
		}
		for _, old := range before.Constraints.Check {
			if !old.NoInherit && !slices.ContainsFunc(after.Constraints.Check, func(c catalog.CheckConstraint) bool { return c.OID == old.OID }) {
				action := a
				action.Action = parser.DropConstraint
				action.Constraint = parser.Identifier{Text: old.Name}
				events = append(events, InheritanceAlterEvent{Action: action, Constraint: old.OID})
			}
		}
	case parser.AlterColumnType, parser.SetExpression, parser.DropExpression:
		if !state.Only {
			events = append(events, InheritanceAlterEvent{Action: a})
		}
	}
	if !state.Only {
		for _, next := range after.Constraints.NotNull {
			if next.NoInherit {
				continue
			}
			index := slices.IndexFunc(before.Constraints.NotNull, func(old catalog.NotNullConstraint) bool { return old.Attribute == next.Attribute })
			validation := index >= 0 && before.Constraints.NotNull[index].NotValid && !next.NotValid
			if index >= 0 && !validation {
				continue
			}
			action := parser.AlterTableAction{Action: parser.AddNotNull, Column: parser.Identifier{Text: after.Columns[after.IndexForAttribute(next.Attribute)].Name}, Constraint: parser.Identifier{Text: next.Name}, NotValid: next.NotValid}
			if validation || a.Action == parser.SetNotNull {
				action.Action = parser.SetNotNull
			}
			events = append(events, InheritanceAlterEvent{Action: action, Attribute: next.Attribute, Validation: validation})
		}
	}
	state.Events[before.Address()] = events
}

// RecordChecks records the inheritable CHECKs that raw added to before,
// numbered from start. It does nothing when plan is nil or ONLY.
func (plan *InheritanceAlterPlan) RecordChecks(before, after *catalog.Relation, raw []*parser.CheckConstraint, start int) {
	state := plan
	if state == nil || state.Only {
		return
	}
	for _, c := range after.Constraints.Check {
		if c.NoInherit || c.Ordinal < start || slices.ContainsFunc(before.Constraints.Check, func(old catalog.CheckConstraint) bool { return old.OID == c.OID }) {
			continue
		}
		index := c.Ordinal - start
		if index < 0 || index >= len(raw) {
			continue
		}
		definition := *raw[index]
		definition.Name = parser.Identifier{Text: c.Name}
		state.Events[before.Address()] = append(state.Events[before.Address()], InheritanceAlterEvent{Action: parser.AlterTableAction{Action: parser.AddCheck, Check: &definition}, Constraint: c.OID})
	}
}

// NotNullMode is how SET NOT NULL on column recurses at this node; a nil plan
// is direct.
func (plan *InheritanceAlterPlan) NotNullMode(column string) NotNullRecursion {
	state := plan
	if state != nil && state.Descendant {
		if state.Validating[column] {
			return NotNullValidation
		}
		return NotNullInherited
	}
	return NotNullDirect
}

// OnlyCreatedConstraint rejects a fresh inheritable fact added under ONLY to a
// parent with children. ONLY's fresh-fact prohibition uses the actual phase
// input, including earlier DROP effects. Existing compatible facts can still
// merge/localize genuinely. A nil plan allows everything.
func (plan *InheritanceAlterPlan) OnlyCreatedConstraint(before, after *catalog.Relation) error {
	state := plan
	if state == nil || !state.Only || state.Descendant || len(state.Targets) < 2 {
		return nil
	}
	for _, fact := range after.Constraints.NotNull {
		if !fact.NoInherit && !slices.ContainsFunc(before.Constraints.NotNull, func(old catalog.NotNullConstraint) bool { return old.Attribute == fact.Attribute }) {
			return sqlError("42P16", "constraint must be added to child tables too")
		}
	}
	for _, fact := range after.Constraints.Check {
		if !fact.NoInherit && !slices.ContainsFunc(before.Constraints.Check, func(old catalog.CheckConstraint) bool { return old.Name == fact.Name }) {
			return sqlError("42P16", "constraint must be added to child tables too")
		}
	}
	return nil
}
