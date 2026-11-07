package tablecmds

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// defaultActionKind maps an action-list member onto the standalone default
// action whose column guards and hints it shares.
func defaultActionKind(kind parser.AlterTableActionKind) (parser.DefaultAction, bool) {
	switch kind {
	case parser.SetColumnDefault:
		return parser.SetDefault, true
	case parser.DropColumnDefault:
		return parser.DropDefault, true
	}
	return 0, false
}

// columnDefaultSlot resolves one member's column against the running candidate,
// so SET sees earlier ADD COLUMN work and DROP does not.
func columnDefaultSlot(next *catalog.Relation, action parser.AlterTableAction) (int, error) {
	defaultAction, ok := defaultActionKind(action.Action)
	if !ok {
		return 0, fmt.Errorf("stuntgres: invalid ALTER COLUMN default action")
	}
	return table.AlterDefaultSlot(next, action.Column, defaultAction)
}

// defaultCandidate replaces one attribute's default on the single running
// candidate; a recursing inheritance node also records explicit provenance.
func defaultCandidate(scope *core.StatementScope, next *catalog.Relation, slot int, definition catalog.ColumnDefault) (*catalog.Relation, error) {
	if scope.Inheritance.Alter != nil {
		return table.InheritanceDefaultCandidate(next, slot, definition)
	}
	return table.AlterDefaultCandidate(next, next.Attributes[slot], definition)
}

// dropColumnDefaultCandidate clears action's default. A column without one is
// an unchanged candidate, mirroring the standalone early-success path.
func dropColumnDefaultCandidate(scope *core.StatementScope, next *catalog.Relation, action parser.AlterTableAction) (*catalog.Relation, error) {
	slot, err := columnDefaultSlot(next, action)
	if err != nil {
		return nil, err
	}
	if _, present := next.ColumnMeta.Defaults[next.Attributes[slot]]; !present {
		return next, nil
	}
	return defaultCandidate(scope, next, slot, catalog.ColumnDefault{})
}

// setColumnDefaultCandidate cooks action's expression against next and replaces
// the default there. The caller holds core.Engine.mu.
func setColumnDefaultCandidate(e *core.Engine, scope *core.StatementScope, op *session.Operation, auth core.AuthorizationView, next *catalog.Relation, action parser.AlterTableAction) (*catalog.Relation, error) {
	slot, err := columnDefaultSlot(next, action)
	if err != nil {
		return nil, err
	}
	definition, err := CookAlterDefaultLocked(e, scope, op, auth, next, slot, action.Expression)
	if err != nil {
		return nil, err
	}
	if definition.Expression == nil {
		if _, present := next.ColumnMeta.Defaults[next.Attributes[slot]]; !present {
			return next, nil
		}
	}
	return defaultCandidate(scope, next, slot, definition)
}
