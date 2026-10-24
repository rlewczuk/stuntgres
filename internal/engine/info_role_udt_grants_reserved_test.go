package engine

import (
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestInfoRoleUdtGrantsReservedIdentity(t *testing.T) {
	f := newRoleUdtGrantsFixture(t)
	in := f.input(t, f.state, f.f.e.Roles().LookupRef("postgres"))
	// REGTYPE may install a lookup-only descriptor for this reserved identity,
	// but no represented relation establishes it as an actual composite.
	if typ, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13477}); ok && typ.OwningRelation != (ids.ObjectAddress{}) {
		t.Fatal("reserved control unexpectedly has relation authority")
	}
	foundUsage := false
	for _, row := range roleUdtGrantsRows(t, in) {
		if infoUserMappingsUnitText(t, row[3]) == "information_schema" && infoUserMappingsUnitText(t, row[4]) == "usage_privileges" {
			t.Fatal("uninstalled identity acquired grant origins", row)
		}
		if infoUserMappingsUnitText(t, row[3]) == "information_schema" && infoUserMappingsUnitText(t, row[4]) == "role_usage_grants" {
			foundUsage = true
		}
	}
	if !foundUsage {
		t.Fatal("installed usage grant row lacks UDT grant origins")
	}
	source := in.Catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 16223})
	typ, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13482})
	if !ok || typ.OwningRelation != source.Address() || typ.Array != 13481 {
		t.Fatal("installed usage grant row lacks reciprocal authority", typ)
	}
}
