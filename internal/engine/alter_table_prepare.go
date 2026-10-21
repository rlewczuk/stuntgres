package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// Prepare root actions in their original order against the original row shape.
// Destination resolution is preparation; movement restrictions are execution.
// Engine.mu is held by the caller.
// On completion error only, the result retains the admitted TYPE prefix for
// sequence AS; that error-path result must never reach a descriptor candidate.
func (e *Engine) prepareAlterTableActions(scope *StatementScope, op *session.Operation, auth AuthorizationView, original *catalog.Relation, ref ids.ObjectRef, actions []parser.AlterTableAction) (table.AlterTablePreparation, error) {
	prepared := table.AlterTablePreparation{Actions: actions}
	fail := func(err error) (table.AlterTablePreparation, error) {
		return table.AlterTablePreparation{}, err
	}
	for _, action := range actions {
		if err := alterTableActionKind(original, action.Action); err != nil {
			return fail(err)
		}
		switch action.Action {
		case parser.AlterColumnType:
			admitted, err := e.admitAlterTypeLocked(scope, op, auth, original, ref, action, nil)
			if err != nil {
				return fail(err)
			}
			prepared.Types = append(prepared.Types, admitted)
			captured, err := e.completeAlterTypeLocked(scope, auth, original, action, admitted)
			if err != nil {
				// Sequence AS precedes this error, but not admission failures.
				// Retain only the admitted prefix; later actions are unvisited.
				return prepared, err
			}
			prepared.Types[len(prepared.Types)-1] = captured
		case parser.SetTablespace:
			destination, err := ddl.TablespaceRef(auth.View, action.Tablespace.Text)
			if err != nil {
				return fail(err)
			}
			if err := checkAlterTablespaceCreate(auth, destination); err != nil {
				return fail(err)
			}
			if prepared.Destination.Address.ObjectID != 0 {
				return fail(sqlError("42601", "cannot have multiple SET TABLESPACE subcommands"))
			}
			prepared.Destination = destination
		}
	}
	if prepared.Destination.Address.ObjectID == 0 {
		return prepared, nil
	}
	if len(actions) == 1 {
		prepared.Actions = nil
		return prepared, nil
	}
	prepared.Actions = make([]parser.AlterTableAction, 0, len(actions)-1)
	for _, action := range actions {
		if action.Action != parser.SetTablespace {
			prepared.Actions = append(prepared.Actions, action)
		}
	}
	return prepared, nil
}

func alterTableActionKind(original *catalog.Relation, action parser.AlterTableActionKind) error {
	label := "ALTER COLUMN ... SET NOT NULL"
	switch action {
	case parser.SetTablespace:
		return alterTablespaceKind(original)
	case parser.DropNotNull:
		label = "ALTER COLUMN ... DROP NOT NULL"
	case parser.AddNotNull, parser.AddCheck, parser.AddUnique, parser.AddForeignKey, parser.AddExclusion, parser.AddUsingIndex:
		label = "ADD CONSTRAINT"
	case parser.DropConstraint:
		label = "DROP CONSTRAINT"
	case parser.DropColumn:
		label = "DROP COLUMN"
	case parser.SetNotNull:
	case parser.AddIdentity, parser.DropIdentity, parser.SetIdentity:
		label = "ALTER COLUMN"
	case parser.AlterColumnType:
		label = "ALTER COLUMN ... TYPE"
	case parser.AddColumn:
		label = "ADD COLUMN"
	case parser.SetExpression, parser.DropExpression:
		label = "ALTER COLUMN"
	case parser.EnableRowSecurity, parser.DisableRowSecurity, parser.ForceRowSecurity, parser.NoForceRowSecurity:
		return access.RowSecurityKind(original, action, analyze.TableKindError)
	default:
		return fmt.Errorf("stuntgres: invalid ALTER action")
	}
	return analyze.TableKindError(original, label)
}
