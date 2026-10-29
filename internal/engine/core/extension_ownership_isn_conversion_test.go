package core_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestExtensionOwnershipISNConversionAuthority(t *testing.T) {
	e, ext := ownershipUnitInstall(t, "isn")
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	initial := e.CatalogRead()
	upc := initial.MustType(ext.Installation.Members[21])
	ean := initial.MustType(ext.Installation.Members[0])
	admit := func(view catalog.Read) {
		t.Helper()
		for _, pair := range [][2]*catalog.Type{{view.MustType(upc.Address()), view.MustType(ean.Address())}, {view.MustType(ean.Address()), view.MustType(upc.Address())}} {
			for _, mode := range []sqltype.CoercionContext{sqltype.Explicit, sqltype.Assignment, sqltype.Implicit} {
				// Selection authenticates endpoints independently of whether the
				// intrinsic cast planner permits this particular context.
				if _, err := catalog.SelectCoercion(view, pair[0].SQLType.Usage(), pair[1].SQLType.Usage(), mode); err != nil {
					t.Fatal(err)
				}
				want := mode == sqltype.Explicit || mode == sqltype.Assignment && pair[0].SQLType.IsUpc()
				if err := catalog.CanCoerce(view, pair[0].SQLType.Usage(), pair[1].SQLType.Usage(), mode); (err == nil) != want {
					t.Fatal("scalar coercion context changed", mode, err)
				}
				fromArray := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(pair[0].Array)})
				toArray := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(pair[1].Array)})
				if err := catalog.CanCoerce(view, fromArray.SQLType.Usage(), toArray.SQLType.Usage(), mode); (err == nil) != want {
					t.Fatal("array lifting coercion context changed", mode, err)
				}
			}
		}
	}
	admit(initial)
	for _, q := range []string{
		"ALTER EXTENSION isn DROP TYPE public.upc",
		"ALTER EXTENSION isn DROP TYPE public.ean13",
		`CREATE EXTENSION "uuid-ossp"`,
		`ALTER EXTENSION "uuid-ossp" ADD TYPE public.upc`,
		`ALTER EXTENSION "uuid-ossp" ADD TYPE public.ean13`,
		"ALTER EXTENSION isn DROP FUNCTION public.upc_in(cstring)",
		"ALTER EXTENSION isn DROP FUNCTION public.isn_out(public.upc)",
		"ALTER EXTENSION isn DROP FUNCTION public.ean13_in(cstring)",
		"ALTER EXTENSION isn DROP FUNCTION public.ean13_out(public.ean13)",
		"DROP EXTENSION isn",
	} {
		enginetest.RoleExecute(t, e, op, q)
		admit(e.CatalogRead())
	}
	base := e.CatalogRead()
	upc, ean = base.MustType(upc.Address()), base.MustType(ean.Address())
	if err := new(extension.IsnProvider).ValidateISNConversion(base, upc, ean); err == nil {
		t.Fatal("unregistered provider instance admitted")
	}
	foreignEngine, foreignExt := ownershipUnitInstall(t, "isn")
	foreignOwner := foreignEngine.CatalogRead().MustType(foreignExt.Installation.Members[21]).Owner
	for _, tc := range []struct {
		name   string
		mutate func(*catalog.Type)
	}{
		{"originless", func(d *catalog.Type) { d.Origin = nil }},
		{"copied_installation", func(d *catalog.Type) {
			copy := *d.Origin.Installation
			origin := *d.Origin
			origin.Installation = &copy
			d.Origin = &origin
		}},
		{"wrong_released_role", func(d *catalog.Type) { origin := *d.Origin; origin.Declaration = 0; d.Origin = &origin }},
		{"zero_revision", func(d *catalog.Type) { d.ObjectRevision = 0 }},
		{"foreign_principal", func(d *catalog.Type) { d.Owner = foreignOwner }},
		{"missing_principal", func(d *catalog.Type) { d.Owner = catalog.Type{}.Owner }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := catalog.CloneState(base.State())
			bad := *upc
			tc.mutate(&bad)
			state.SetDescriptor(bad.Address(), &bad)
			if _, err := catalog.SelectCoercion(e.Catalog.Read(state), bad.SQLType.Usage(), ean.SQLType.Usage(), sqltype.Explicit); err == nil {
				t.Fatal("forged endpoint admitted")
			}
		})
	}
	stale, err := sqltype.Upc(upc.SQLType.ID(), upc.Name(), upc.SQLType.SQLName())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.SelectCoercion(base, stale.Usage(), ean.SQLType.Usage(), sqltype.Explicit); err == nil {
		t.Fatal("stale type generation admitted")
	}
	if _, err := catalog.SelectCoercion(base, foreignEngine.CatalogRead().MustType(foreignExt.Installation.Members[21]).SQLType.Usage(), ean.SQLType.Usage(), sqltype.Explicit); err == nil {
		t.Fatal("foreign domain admitted")
	}
	enginetest.RoleExecute(t, e, op, "CREATE SCHEMA isn_second")
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION isn SCHEMA isn_second")
	view := e.CatalogRead()
	second := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "isn"}))
	otherEAN := view.MustType(second.Installation.Members[0])
	if _, err := catalog.SelectCoercion(view, upc.SQLType.Usage(), otherEAN.SQLType.Usage(), sqltype.Explicit); err == nil {
		t.Fatal("independent genuine cohort admitted")
	}
}
