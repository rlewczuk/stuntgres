package ddl

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// TablespacePhysicalUnsupported is the message CREATE TABLESPACE always ends with.
const TablespacePhysicalUnsupported = "CREATE TABLESPACE physical locations are unsupported in embedded mode"

// CreateTablespace validates CREATE TABLESPACE and then always fails: the
// embedded engine has no physical locations. It never stages anything.
func CreateTablespace(ctx context.Context, view access.View, s *parser.CreateTablespace) error {
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
	return sqlError("0A000", TablespacePhysicalUnsupported)
}

// TablespaceRef resolves a tablespace named by GRANT, ALTER TABLESPACE or
// ALTER TABLE SET TABLESPACE.
func TablespaceRef(view access.View, name string) (ids.ObjectRef, error) {
	address := view.Catalog.NameAddress(ids.NameKey{ClassID: ids.TablespaceClassID, Name: name})
	if address == (ids.ObjectAddress{}) {
		return ids.ObjectRef{}, sqlError("42704", fmt.Sprintf(`tablespace "%s" does not exist`, name))
	}
	return view.Ref(address), nil
}
