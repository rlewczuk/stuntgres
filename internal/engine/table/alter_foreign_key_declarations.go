package table

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Project only column liveness. Ordinary candidate validation remains the
// execution owner. A duplicate/system column cannot succeed, so defer all
// FK target resolution to preserve that earlier ADD-column diagnostic.
func alterAddedColumnAdmissions(child *catalog.Relation, actions []parser.AlterTableAction, dropped map[int32]bool) ([]bool, bool) {
	if !slices.ContainsFunc(actions, func(action parser.AlterTableAction) bool { return action.Action == parser.AddColumn }) {
		return nil, true
	}
	live := make(map[string]bool, len(child.Columns))
	for name, slot := range child.Index {
		if !dropped[child.Attributes[slot]] {
			live[name] = true
		}
	}
	admitted := make([]bool, len(actions))
	for i, action := range actions {
		if action.Action != parser.AddColumn {
			continue
		}
		name := action.Definition.Name
		if child.RelationKind != catalog.CompositeRelation && catalog.IsSystemColumn(name) {
			return nil, false
		}
		if live[name] {
			if !action.IfNotExists {
				return nil, false
			}
			continue
		}
		live[name], admitted[i] = true, true
	}
	return admitted, true
}

// Equal compares origin, naming and every normalized definition field used by
// cooking. The private parser attachment bookkeeping is immutable syntax only.
func (a *AlterForeignKeyDeclaration) Equal(b *AlterForeignKeyDeclaration) bool {
	x, y := &a.Definition, &b.Definition
	return a.Action == b.Action && a.ConstraintIndex == b.ConstraintIndex && a.Name == b.Name &&
		x.Name == y.Name && slices.Equal(x.Columns, y.Columns) && slices.Equal(x.Target.Parts, y.Target.Parts) &&
		slices.Equal(x.Referenced, y.Referenced) && slices.Equal(x.DeleteColumns, y.DeleteColumns) &&
		x.Full == y.Full && x.NotValid == y.NotValid && x.NotEnforced == y.NotEnforced &&
		x.EnforcementSet == y.EnforcementSet && x.EnforcementPos == y.EnforcementPos &&
		x.Deferrable == y.Deferrable && x.InitiallyDeferred == y.InitiallyDeferred &&
		x.Pos == y.Pos && x.NamedDeclarationPos == y.NamedDeclarationPos &&
		x.UpdateAction == y.UpdateAction && x.DeleteAction == y.DeleteAction
}

// CloneAlterForeignKeyAdditions detaches the retained command definitions too,
// not just the admission slice, for statement checkpoint ownership.
func CloneAlterForeignKeyAdditions(keys []AlterForeignKeyAddition) []AlterForeignKeyAddition {
	owned := slices.Clone(keys)
	for i := range owned {
		owned[i].Definition = cloneForeignKeyDefinition(owned[i].Definition)
	}
	return owned
}

// CheckAlterForeignKeyAdditions fences the captured stream against actual ADD
// outcomes. It does not resolve names or independently choose install order.
func CheckAlterForeignKeyAdditions(actions []parser.AlterTableAction, additions []AddedColumn, keys []AlterForeignKeyAddition) error {
	expected := 0
	for _, action := range actions {
		if action.Action == parser.AddForeignKey {
			expected++
		}
	}
	for _, added := range additions {
		if added.Attribute == 0 {
			continue
		}
		for _, constraint := range added.Column.Constraints {
			if constraint.ForeignKey != nil {
				expected++
			}
		}
	}
	if expected != len(keys) {
		return ids.ErrCatalogReplaced
	}
	lastInlineAction, lastInlineConstraint, lastExplicit := -1, -1, -1
	explicit := false
	for i := range keys {
		key := &keys[i]
		if key.Action < 0 || key.Action >= len(actions) {
			return ids.ErrCatalogReplaced
		}
		action := actions[key.Action]
		if key.ConstraintIndex == -1 {
			if action.Action != parser.AddForeignKey || key.Action <= lastExplicit {
				return ids.ErrCatalogReplaced
			}
			explicit, lastExplicit = true, key.Action
			continue
		}
		if explicit || action.Action != parser.AddColumn || key.ConstraintIndex < 0 || key.ConstraintIndex >= len(action.Definition.Constraints) || action.Definition.Constraints[key.ConstraintIndex].ForeignKey == nil {
			return ids.ErrCatalogReplaced
		}
		if key.Action < lastInlineAction || key.Action == lastInlineAction && key.ConstraintIndex <= lastInlineConstraint {
			return ids.ErrCatalogReplaced
		}
		lastInlineAction, lastInlineConstraint = key.Action, key.ConstraintIndex
		if !slices.ContainsFunc(additions, func(added AddedColumn) bool {
			return added.Attribute != 0 && added.Column == action.Definition
		}) {
			return ids.ErrCatalogReplaced
		}
	}
	return nil
}
