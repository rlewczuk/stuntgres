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

// CheckAlterTablespaceCreate checks CREATE on the destination of ALTER TABLE
// SET TABLESPACE or ALTER TABLE ALL IN TABLESPACE; pg_default needs none.
func CheckAlterTablespaceCreate(view access.View, env access.Environment, ref ids.ObjectRef) error {
	if ref.Address.ObjectID == 1663 {
		return nil
	}
	return view.CheckCreate(env, ref)
}

// CheckDefaultTablespaceCreation consumes the operation's captured setting.
// A trusted startup default may have disappeared since capture; PostgreSQL
// falls back to the database default in that case. Interactive SET validates
// existence separately. No successful builtin selection simulates placement.
func CheckDefaultTablespaceCreation(view access.View, env access.Environment, name, persistence string, partitioned bool) error {
	return checkDefaultTablespaceCreation(view, env, name, persistence, partitioned, true)
}

// CheckClonedIndexTablespaceCreation is DefineIndex's check_rights=false path:
// an inherited default-space index has no explicit destination, but synthesis
// still rejects pg_global. Reusing an existing index must not call this.
func CheckClonedIndexTablespaceCreation(read catalog.Read, name, persistence string, partitioned bool) error {
	return checkDefaultTablespaceCreation(access.View{Catalog: read}, nil, name, persistence, partitioned, false)
}

func checkDefaultTablespaceCreation(view access.View, env access.Environment, name, persistence string, partitioned, checkCreate bool) error {
	if persistence == "temporary" || name == "" {
		return nil
	}
	address := view.Catalog.NameAddress(ids.NameKey{ClassID: ids.TablespaceClassID, Name: name})
	if address == (ids.ObjectAddress{}) {
		return nil
	}
	if address.ObjectID == 1663 {
		if partitioned {
			return sqlError("0A000", "cannot specify default tablespace for partitioned relations")
		}
		return nil
	}
	if checkCreate {
		if err := view.CheckCreate(env, view.Ref(address)); err != nil {
			return err
		}
	}
	if address.ObjectID == 1664 {
		return sqlError("22023", "only shared relations can be placed in pg_global tablespace")
	}
	return nil
}

// AlterAllTablespace decides ALTER ... ALL IN TABLESPACE. Bulk same-space
// assignment returns before relation selection or locking. With builtin-only
// identities every distinct pair includes forbidden pg_global.
func AlterAllTablespace(view access.View, env access.Environment, s *parser.AlterTableAllInTablespace) error {
	for _, role := range s.Owners {
		if _, err := authorization.MembershipRole(view.Roles, view.State, role); err != nil {
			return err
		}
	}
	source, err := TablespaceRef(view, s.Source.Text)
	if err != nil {
		return err
	}
	destination, err := TablespaceRef(view, s.Destination.Text)
	if err != nil {
		return err
	}
	if source.Address.ObjectID == 1664 || destination.Address.ObjectID == 1664 {
		return sqlError("22023", "cannot move relations in to or out of pg_global tablespace")
	}
	if err := CheckAlterTablespaceCreate(view, env, destination); err != nil {
		return err
	}
	if source.Address == destination.Address {
		return nil
	}
	return sqlError("0A000", "physical tablespace relocation is unsupported")
}

// AlterTablespaceKind rejects SET TABLESPACE on relation kinds that have no
// storage of their own.
func AlterTablespaceKind(d *catalog.Relation) error {
	switch d.RelationKind {
	case catalog.TableRelation, catalog.PartitionedRelation, catalog.IndexRelation, catalog.MaterializedViewRelation:
		return nil
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`ALTER action SET TABLESPACE cannot be performed on relation "%s"`, d.Name()), Detail: fmt.Sprintf("This operation is not supported for %ss.", catalog.RelationDiagnosticKind(d))}
}
