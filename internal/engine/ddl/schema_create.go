package ddl

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// StageCreateSchema stages CREATE SCHEMA and its table elements. It returns
// false without error when there is nothing to publish (IF NOT EXISTS on an
// existing schema).
func StageCreateSchema(h SchemaHost, auth Authority, s *parser.CreateSchema, input sqltype.InputEnvironment) (bool, error) {
	owner, name, err := ResolveSchemaCreation(h, auth, s)
	if err != nil {
		return false, err
	}
	view := h.AccessView(auth)
	if _, ok := view.Catalog.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name}); ok {
		if s.IfNotExists {
			Notice(h, "42P06", fmt.Sprintf(`schema "%s" already exists, skipping`, name))
			return false, nil
		}
		return false, sqlError("42P06", fmt.Sprintf(`schema "%s" already exists`, name))
	}
	oid, err := h.AllocateOID()
	if err != nil {
		return false, err
	}
	rev, err := h.Revision()
	if err != nil {
		return false, err
	}
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: oid}, ObjectName: name, ObjectRevision: rev, Owner: owner}
	ns.ACL = h.CreationACL(owner, ids.ObjectAddress{}, authorization.DefaultSchemas)
	if err = CreateCommand(h.Stage(), "CREATE SCHEMA", ns); err != nil {
		return false, err
	}
	for _, table := range s.Tables {
		if table.Persistence == "temporary" {
			return false, sqlError("0A000", "temporary tables in CREATE SCHEMA are unsupported")
		}
		if table.OnCommit != parser.OnCommitUnspecified {
			return false, sqlError("42P16", "ON COMMIT can only be used on temporary tables")
		}
		if len(table.Name.Parts) == 2 && table.Name.Parts[0].Text != name {
			return false, sqlError("42P15", fmt.Sprintf("CREATE specifies a schema (%s) different from the one being created (%s)", table.Name.Parts[0].Text, name))
		}
		if err = h.StageSchemaTable(auth, ns.Address(), owner, table, input); err != nil {
			return false, err
		}
	}
	return true, nil
}

// ResolveSchemaCreation is the effect-free owner/database authority shared by
// initial creation and conditional-name readmission after a real lock wait.
func ResolveSchemaCreation(h Host, auth Authority, s *parser.CreateSchema) (authorization.PrincipalRef, string, error) {
	view := h.AccessView(auth)
	actor, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil {
		return authorization.PrincipalRef{}, "", err
	}
	owner, err := access.SchemaOwner(view, s.Authorization, s.AuthorizationIdentity)
	if err != nil {
		return authorization.PrincipalRef{}, "", err
	}
	name := s.Name
	if name == "" {
		d, err := view.Roles.Descriptor(owner)
		if err != nil {
			return authorization.PrincipalRef{}, "", err
		}
		name = d.Name
	}
	if err = checkDatabaseCreate(view, auth); err != nil {
		return authorization.PrincipalRef{}, "", err
	}
	if !actor.Superuser && owner != actor.Ref {
		return authorization.PrincipalRef{}, "", sqlError("42501", fmt.Sprintf(`must be able to SET ROLE "%s"`, s.Authorization))
	}
	if err = schemaReserved(name); err != nil {
		return authorization.PrincipalRef{}, "", err
	}
	return owner, name, nil
}
