package engine

import (
	"context"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

const tablespacePhysicalUnsupported = "CREATE TABLESPACE physical locations are unsupported in embedded mode"

// createTablespace validates CREATE TABLESPACE and then always fails: the
// embedded engine has no physical locations. It never stages anything.
func createTablespace(ctx context.Context, view access.View, s *parser.CreateTablespace) error {
	actor, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied to create tablespace \"" + s.Name.Text + "\"", Hint: "Must be superuser to create a tablespace."}
	}
	owner := parser.RoleSpec{Identity: "current"}
	if s.Owner != nil {
		owner = *s.Owner
	}
	if _, err := authorization.MembershipRole(view.Roles, view.State, owner); err != nil {
		return err
	}
	if _, err := catalog.ValidateTablespaceLocation(s.Location); err != nil {
		return err
	}
	if err := catalog.ValidateTablespaceName(s.Name.Text, s.Name.Text == "pg_default" || s.Name.Text == "pg_global"); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// WITH is retained structurally but cannot be applied without physical
	// creation. Never allocate an OID, stage a row or pretend data was placed.
	return sqlError("0A000", tablespacePhysicalUnsupported)
}
