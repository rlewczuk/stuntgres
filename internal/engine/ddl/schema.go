package ddl

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func schemaReserved(name string) error {
	if strings.HasPrefix(name, "pg_") {
		return detailError("42939", fmt.Sprintf(`unacceptable schema name "%s"`, name), `The prefix "pg_" is reserved for system schemas.`)
	}
	return nil
}

// lookupSchema finds the schema called name in read.
func lookupSchema(read catalog.Read, name string) (*catalog.Namespace, error) {
	a, ok := read.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name})
	if !ok {
		return nil, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, name))
	}
	d, ok := read.Namespace(a)
	if !ok {
		return nil, fmt.Errorf("stuntgres: invalid namespace descriptor")
	}
	return d, nil
}

// checkSchemaOwner requires auth to own the schema d.
func checkSchemaOwner(h Host, auth Authority, d *catalog.Namespace) error {
	return Check(h, auth, access.Requirement{Object: ObjectRef(h, auth, d.Address()), OwnerOnly: true})
}

// checkDatabaseCreate requires CREATE on the current database, as creating
// or renaming a schema does.
func checkDatabaseCreate(view access.View, auth Authority) error {
	ref := view.Owner.Domain.Ref(view.Catalog.Epoch(), catalog.CurrentDatabaseAddress)
	rights, err := view.Effective(auth, ref, false)
	if err != nil {
		return err
	}
	if rights&authorization.CreatePrivilege == 0 {
		database := view.Catalog.MustDatabase(catalog.CurrentDatabaseAddress)
		name := database.Name()
		if name == "" {
			name = "stuntgres" // The unnamed native Engine's existing database display.
		}
		return sqlError("42501", "permission denied for database "+name)
	}
	return nil
}
