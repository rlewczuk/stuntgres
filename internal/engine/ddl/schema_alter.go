package ddl

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// StageAlterSchema stages ALTER SCHEMA RENAME TO or OWNER TO. It returns
// false without error when there is nothing to publish (the owner is
// already the target).
func StageAlterSchema(h Host, auth Authority, s *parser.AlterSchema) (bool, error) {
	view := h.AccessView(auth)
	old, err := lookupSchema(view.Catalog, s.Name)
	if err != nil {
		return false, err
	}
	next := *old
	if s.NewName != "" {
		if _, ok := view.Catalog.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: s.NewName}); ok {
			return false, sqlError("42P06", fmt.Sprintf(`schema "%s" already exists`, s.NewName))
		}
		if err = checkSchemaOwner(h, auth, old); err != nil {
			return false, err
		}
		if err = checkDatabaseCreate(view, auth); err != nil {
			return false, err
		}
		if err = schemaReserved(s.NewName); err != nil {
			return false, err
		}
		next.ObjectName = s.NewName
	} else {
		target, err := access.SchemaOwner(view, s.Owner, s.OwnerIdentity)
		if err != nil {
			return false, err
		}
		if target == old.Owner {
			AdmitNoopCommand(h.Stage(), "ALTER SCHEMA", old.Address())
			return false, nil
		}
		if err = checkSchemaOwner(h, auth, old); err != nil {
			return false, err
		}
		if err = access.OwnershipSuccessor(view, target); err != nil {
			return false, err
		}
		if err = access.OwnershipDatabaseCreate(view, auth); err != nil {
			return false, err
		}
		next.Owner = target
		next.ACL = authorization.SchemaACLOwner(old.ACL, old.Owner, target)
	}
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return false, err
	}
	if err = ReplaceCommand(h.Stage(), "ALTER SCHEMA", &next); err != nil {
		return false, err
	}
	return true, nil
}
