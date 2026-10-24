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
	if typ, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13482}); ok && typ.OwningRelation != (ids.ObjectAddress{}) {
		t.Fatal("reserved control unexpectedly has relation authority")
	}
	for _, row := range roleUdtGrantsRows(t, in) {
		if infoUserMappingsUnitText(t, row[3]) == "information_schema" && infoUserMappingsUnitText(t, row[4]) == "role_usage_grants" {
			t.Fatal("uninstalled identity acquired grant origins", row)
		}
	}
}
