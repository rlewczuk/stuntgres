package schemacmds_test

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestInfoRoleUsageGrantsExclusions(t *testing.T) {
	// A genuine represented reciprocal composite contributes to role_udt_grants,
	// but neither its type USAGE nor its relation SELECT is a usage-grant family.
	f := newRoleUdtGrantsFixture(t)
	composite := *f.udt
	composite.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.UsagePrivilege, authorization.UsagePrivilege)}}
	f.state.SetDescriptor(composite.Address(), &composite)
	enum, err := sqltype.Enum(20544, "usage_enum", "default_schema.usage_enum", []string{"one", "two"})
	if err != nil {
		t.Fatal(err)
	}
	other := &catalog.Type{SQLType: enum, ObjectNamespace: f.f.schema, Owner: f.f.owner, ACL: composite.ACL}
	f.state.SetDescriptor(other.Address(), other)
	array, err := sqltype.Array(20545, "_usage_enum", enum)
	if err != nil {
		t.Fatal(err)
	}
	companion := &catalog.Type{SQLType: array, ObjectNamespace: f.f.schema, Owner: f.f.owner, ACL: composite.ACL}
	f.state.SetDescriptor(companion.Address(), companion)
	in := f.input(t, f.state, f.f.reader)
	in.Source = in.Catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoRoleUsageGrantsOID})
	in.Ref = in.Owner.Domain.Ref(in.Catalog.Epoch(), in.Source.Address())
	rows := roleUsageGrantsRows(t, in)
	for _, name := range []string{composite.Name(), other.Name(), companion.Name(), "usage_privileges", "missing_usage_object"} {
		if got := roleUsageGrantsBag(t, rows, name); len(got) != 0 {
			t.Fatal("unselected or uninstalled family acquired origins", name, got)
		}
	}
}
