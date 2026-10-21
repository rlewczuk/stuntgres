package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Events describe actual provider-phase changes, not the final presence of a
// same-named fact. A DROP followed by ADD must remove the old contribution even
// though the parent's completed candidate already contains its replacement.
type inheritanceAlterEvent struct {
	Action         parser.AlterTableAction
	Attribute      int32
	Constraint     ids.OID
	Validation     bool
	RetiredNotNull []catalog.NotNullConstraint
	RetiredChecks  []catalog.CheckConstraint
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

func InheritanceParentActions(state *inheritanceAlterPlan, old *catalog.Relation) []parser.AlterTableAction {
	var actions []parser.AlterTableAction
	state.Validating = nil
	if old.Inheritance == nil {
		return actions
	}
	for _, parent := range old.Inheritance.Parents {
		for _, event := range state.Events[parent] {
			a := event.Action
			if a.Action == parser.DropColumn || a.Action == parser.DropNotNull || a.Action == parser.DropConstraint {
				var origins []catalog.InheritanceOrigin
				if a.Action == parser.DropConstraint {
					origins = old.Inheritance.Checks[a.Constraint.Text].Origins
				} else if slot, ok := old.Index[a.Column.Text]; ok {
					if a.Action == parser.DropColumn {
						origins = old.Inheritance.Columns[old.Attributes[slot]].Origins
					} else {
						origins = old.Inheritance.NotNull[old.Attributes[slot]].Origins
					}
				}
				reached := slices.ContainsFunc(origins, func(origin catalog.InheritanceOrigin) bool {
					return origin.Parent == parent && ((a.Action == parser.DropColumn && origin.Attribute == event.Attribute) || (a.Action != parser.DropColumn && origin.Constraint == event.Constraint))
				})
				if !reached {
					continue
				}
			}
			if state.Only && a.Action != parser.DropColumn && a.Action != parser.DropConstraint && a.Action != parser.DropNotNull {
				continue
			}
			if !slices.ContainsFunc(actions, func(previous parser.AlterTableAction) bool {
				return previous.Action == a.Action && inheritanceEventName(previous) == inheritanceEventName(a)
			}) {
				actions = append(actions, a)
			}
			if event.Validation {
				if state.Validating == nil {
					state.Validating = make(map[string]bool)
				}
				state.Validating[a.Column.Text] = true
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

func InheritanceRemovedOrigin(state *inheritanceAlterPlan, origin catalog.InheritanceOrigin, kind parser.AlterTableActionKind) bool {
	for _, event := range state.Events[origin.Parent] {
		if event.Action.Action != kind {
			continue
		}
		if kind == parser.DropColumn && event.Attribute == origin.Attribute {
			return true
		}
		if kind == parser.DropNotNull && event.Constraint == origin.Constraint {
			return true
		}
		if kind == parser.DropConstraint && event.Constraint == origin.Constraint {
			return true
		}
	}
	return false
}

func InheritancePreparatoryLocks(actions []parser.AlterTableAction, only bool) bool {
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
