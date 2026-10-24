package engine

import (
	"context"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

func (e *Engine) initializeDatabase(name string) {
	typ, _ := e.catalog.Current().LookupType(sqltype.Name)
	value, err := typ.Input(context.Background(), name, -1)
	// Opaque host keys may be invalid NAME inputs. Preserve connector creation;
	// reject physical session admission with the original typed input error.
	e.databaseName, e.databaseNameError = name, err
	if err == nil {
		e.databaseName, _ = value.Text()
	}
	owner := e.roles.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	db := &catalog.Database{ObjectAddress: e.catalog.DatabaseAddress(), ObjectRevision: 1, ObjectName: e.databaseName, Owner: owner, FrozenXID: 3}
	e.catalog.State.SetDescriptor(db.Address(), db)
	e.catalog.State.BindName(catalog.DescriptorNameKey(db), db.Address())
}
