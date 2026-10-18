package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type alterTablePreparation struct {
	actions     []parser.AlterTableAction
	destination ids.ObjectRef
	types       []alterTypePreparation
}

// Prepare root actions in their original order against the original row shape.
// Destination resolution is preparation; movement restrictions are execution.
// Engine.mu is held by the caller.
// On completion error only, the result retains the admitted TYPE prefix for
// sequence AS; that error-path result must never reach a descriptor candidate.
func (e *Engine) prepareAlterTableActions(scope *StatementScope, op *Operation, auth AuthorizationView, original *relationDescriptor, ref ids.ObjectRef, actions []parser.AlterTableAction) (alterTablePreparation, error) {
	prepared := alterTablePreparation{actions: actions}
	fail := func(err error) (alterTablePreparation, error) {
		return alterTablePreparation{}, err
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
			prepared.types = append(prepared.types, admitted)
			captured, err := e.completeAlterTypeLocked(scope, auth, original, action, admitted)
			if err != nil {
				// Sequence AS precedes this error, but not admission failures.
				// Retain only the admitted prefix; later actions are unvisited.
				return prepared, err
			}
			prepared.types[len(prepared.types)-1] = captured
		case parser.SetTablespace:
			destination, err := resolveGrantTablespace(auth, action.Tablespace.Text)
			if err != nil {
				return fail(err)
			}
			if err := checkAlterTablespaceCreate(auth, destination); err != nil {
				return fail(err)
			}
			if prepared.destination.Address.ObjectID != 0 {
				return fail(sqlError("42601", "cannot have multiple SET TABLESPACE subcommands"))
			}
			prepared.destination = destination
		}
	}
	if prepared.destination.Address.ObjectID == 0 {
		return prepared, nil
	}
	if len(actions) == 1 {
		prepared.actions = nil
		return prepared, nil
	}
	prepared.actions = make([]parser.AlterTableAction, 0, len(actions)-1)
	for _, action := range actions {
		if action.Action != parser.SetTablespace {
			prepared.actions = append(prepared.actions, action)
		}
	}
	return prepared, nil
}

func validateAlterTablespaceMove(destination ids.ObjectRef) error {
	if destination.Address.ObjectID == 1664 {
		return sqlError("22023", "only shared relations can be placed in pg_global tablespace")
	}
	return nil
}

func alterTableActionKind(original *relationDescriptor, action parser.AlterTableActionKind) error {
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
		return rowSecurityKind(original, action)
	default:
		return fmt.Errorf("stuntgres: invalid ALTER action")
	}
	return tableKind(original, label)
}

func checkAlterTablespaceCreate(auth AuthorizationView, ref ids.ObjectRef) error {
	if ref.Address.ObjectID == 1663 {
		return nil
	}
	return auth.CheckCreate(ref)
}

func alterTablespaceKind(d *relationDescriptor) error {
	switch d.relationKind {
	case tableRelation, partitionedRelation, indexRelation, materializedViewRelation:
		return nil
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`ALTER action SET TABLESPACE cannot be performed on relation "%s"`, d.name), Detail: fmt.Sprintf("This operation is not supported for %ss.", relationDiagnosticKind(d))}
}

func tablespaceOnlyActions(actions []parser.AlterTableAction) bool {
	if len(actions) == 0 {
		return false
	}
	for _, action := range actions {
		if action.Action != parser.SetTablespace {
			return false
		}
	}
	return true
}

// Bulk same-space assignment returns before relation selection or locking. With
// builtin-only identities every distinct pair includes forbidden pg_global.
func (e *Engine) alterAllTablespaceLocked(scope *StatementScope, auth AuthorizationView, s *parser.AlterTableAllInTablespace, count int) (Result, error) {
	defer e.mu.Unlock()
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	for _, role := range s.Owners {
		if _, err := authorization.MembershipRole(auth.roles, auth.state, role); err != nil {
			return Result{}, err
		}
	}
	source, err := resolveGrantTablespace(auth, s.Source.Text)
	if err != nil {
		return Result{}, err
	}
	destination, err := resolveGrantTablespace(auth, s.Destination.Text)
	if err != nil {
		return Result{}, err
	}
	if source.Address.ObjectID == 1664 || destination.Address.ObjectID == 1664 {
		return Result{}, sqlError("22023", "cannot move relations in to or out of pg_global tablespace")
	}
	if err := checkAlterTablespaceCreate(auth, destination); err != nil {
		return Result{}, err
	}
	if source.Address == destination.Address {
		return Result{}, nil
	}
	return Result{}, sqlError("0A000", "physical tablespace relocation is unsupported")
}
