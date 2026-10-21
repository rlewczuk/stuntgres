package engine

import (
	"context"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

func (e *Engine) initializeDatabase(name string) {
	typ, _ := (e.catalog.read(e.catalog.state)).LookupType(sqltype.Name)
	value, err := typ.Input(context.Background(), name, -1)
	// Opaque host keys may be invalid NAME inputs. Preserve connector creation;
	// reject physical session admission with the original typed input error.
	e.databaseName, e.databaseNameError = name, err
	if err == nil {
		e.databaseName, _ = value.Text()
	}
	owner := e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	db := &catalog.Database{ObjectAddress: catalog.CurrentDatabaseAddress, ObjectRevision: 1, ObjectName: e.databaseName, Owner: owner}
	e.catalog.state.SetDescriptor(db.Address(), db)
	e.catalog.state.BindName(catalog.DescriptorNameKey(db), db.Address())
}

// Attachment identity and ACLs belong to the destination, not portable state.
// Retain them before replacement principal validation and publication.
func (e *Engine) retainDatabaseLocked(next *catalog.State) {
	if old := next.Get(catalog.CurrentDatabaseAddress); old != nil {
		next.UnbindName(catalog.DescriptorNameKey(old))
	}
	database := e.catalog.state.Get(catalog.CurrentDatabaseAddress)
	next.SetDescriptor(catalog.CurrentDatabaseAddress, database)
	next.BindName(catalog.DescriptorNameKey(database), catalog.CurrentDatabaseAddress)
}
