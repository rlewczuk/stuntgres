package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func validateAlterTablespaceMove(destination ids.ObjectRef) error {
	if destination.Address.ObjectID == 1664 {
		return sqlError("22023", "only shared relations can be placed in pg_global tablespace")
	}
	return nil
}

func checkAlterTablespaceCreate(auth AuthorizationView, ref ids.ObjectRef) error {
	if ref.Address.ObjectID == 1663 {
		return nil
	}
	return auth.CheckCreate(ref)
}

func alterTablespaceKind(d *catalog.Relation) error {
	switch d.RelationKind {
	case catalog.TableRelation, catalog.PartitionedRelation, catalog.IndexRelation, catalog.MaterializedViewRelation:
		return nil
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`ALTER action SET TABLESPACE cannot be performed on relation "%s"`, d.Name()), Detail: fmt.Sprintf("This operation is not supported for %ss.", relationDiagnosticKind(d))}
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
		if _, err := authorization.MembershipRole(auth.Roles, auth.State, role); err != nil {
			return Result{}, err
		}
	}
	source, err := ddl.TablespaceRef(auth.View, s.Source.Text)
	if err != nil {
		return Result{}, err
	}
	destination, err := ddl.TablespaceRef(auth.View, s.Destination.Text)
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
