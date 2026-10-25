package core

import (
	"context"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

func (e *Engine) initializeDatabase(name string) {
	typ, _ := e.Catalog.Current().LookupType(sqltype.Name)
	value, err := typ.Input(context.Background(), name, -1)
	// Opaque host keys may be invalid NAME inputs. Preserve connector creation;
	// reject physical session admission with the original typed input error.
	e.DatabaseName, e.DatabaseNameError = name, err
	if err == nil {
		e.DatabaseName, _ = value.Text()
	}
	owner := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	db := &catalog.Database{ObjectAddress: e.Catalog.DatabaseAddress(), ObjectRevision: 1, ObjectName: e.DatabaseName, Owner: owner, FrozenXID: 3}
	e.Catalog.State.SetDescriptor(db.Address(), db)
	e.Catalog.State.BindName(catalog.DescriptorNameKey(db), db.Address())
}
