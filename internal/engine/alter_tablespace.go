package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/parser"
)

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
	if err := ddl.CheckAlterTablespaceCreate(auth.View, auth, destination); err != nil {
		return Result{}, err
	}
	if source.Address == destination.Address {
		return Result{}, nil
	}
	return Result{}, sqlError("0A000", "physical tablespace relocation is unsupported")
}
