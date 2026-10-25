package schemacmds_test

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
)

func TestInfoRoleUdtGrantsDatabaseOwner(t *testing.T) {
	f := newRoleUdtGrantsFixture(t)
	view := f.f.e.CatalogRead()
	database := *view.MustDatabase(view.Owner().DatabaseAddress())
	database.Owner = f.f.reader
	f.state.SetDescriptor(database.Address(), &database)
	udt := *f.udt
	udt.Owner = f.f.e.Roles().LookupRef("pg_database_owner")
	udt.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: udt.Owner, Grantee: authorization.ACLGrantee{Role: f.f.reader}, Privileges: authorization.UsagePrivilege}}}
	f.state.SetDescriptor(udt.Address(), &udt)
	f.check(t, f.state, f.f.reader, roleUdtGrantsWant("pg_database_owner", "default_reader", "TYPE USAGE", "YES"))
	next := catalog.CloneState(f.state)
	database.Owner = f.f.owner
	next.SetDescriptor(database.Address(), &database)
	f.check(t, next, f.f.reader, roleUdtGrantsWant("pg_database_owner", "default_reader", "TYPE USAGE", "NO"))
}
