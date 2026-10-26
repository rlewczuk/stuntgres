package schemacmds_test

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestInfoRoleUdtGrantsReservedIdentity(t *testing.T) {
	f := newRoleUdtGrantsFixture(t)
	in := f.input(t, f.state, f.f.e.Roles().LookupRef("postgres"))
	// REGTYPE may install a lookup-only descriptor for this reserved identity,
	// but no represented relation establishes it as an actual composite.
	if typ, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13468}); ok && typ.OwningRelation != (ids.ObjectAddress{}) {
		t.Fatal("reserved control unexpectedly has relation authority")
	}
	foundUsage := false
	foundPrivileges := map[[3]string]int{}
	for _, row := range roleUdtGrantsRows(t, in) {
		if infoUserMappingsUnitText(t, row[3]) == "information_schema" && infoUserMappingsUnitText(t, row[4]) == "udt_privileges" {
			t.Fatal("uninstalled identity acquired grant origins", row)
		}
		if infoUserMappingsUnitText(t, row[3]) == "information_schema" && infoUserMappingsUnitText(t, row[4]) == "usage_privileges" {
			key := [3]string{infoUserMappingsUnitText(t, row[0]), infoUserMappingsUnitText(t, row[1]), ""}
			base, err := row[6].BaseValue()
			if err != nil {
				t.Fatal(err)
			}
			key[2], err = base.Text()
			if err != nil {
				t.Fatal(err)
			}
			privilege, err := row[5].BaseValue()
			if err != nil {
				t.Fatal(err)
			}
			text, err := privilege.Text()
			if err != nil || text != "TYPE USAGE" {
				t.Fatal(text, err)
			}
			foundPrivileges[key]++
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
	installed := in.Catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 16233})
	installedType, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13477})
	if !ok || installedType.OwningRelation != installed.Address() || installedType.Array != 13476 {
		t.Fatal("installed usage privileges row lacks reciprocal authority", installedType)
	}
	array, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13476})
	if !ok || array.OwningRelation != installed.Address() || array.SQLType.Element() != installedType.SQLType {
		t.Fatal("installed usage privileges array lacks reciprocal authority", array)
	}
	owner, err := in.Roles.Descriptor(installedType.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(foundPrivileges) != 2 || foundPrivileges[[3]string{owner.Name, owner.Name, "YES"}] != 1 || foundPrivileges[[3]string{owner.Name, "PUBLIC", "NO"}] != 1 {
		t.Fatal("installed usage privileges UDT origins", foundPrivileges)
	}
}

func infoUserMappingsUnitText(t *testing.T, value sqltype.Value) string {
	t.Helper()
	base, err := value.BaseValue()
	if err != nil || base.Type().ID() != sqltype.Name {
		t.Fatal("NAME payload lost", base, err)
	}
	text, err := base.Text()
	if err != nil {
		t.Fatal("NAME text", err)
	}
	return text
}
