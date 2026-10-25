package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestIsnProviderAuthority(t *testing.T) {
	for _, family := range []string{"ean13", "isbn", "isbn13", "ismn", "ismn13", "issn", "issn13"} {
		t.Run(family, func(t *testing.T) { testIsnProviderAuthority(t, family) })
	}
}

func testIsnProviderAuthority(t *testing.T, family string) {
	e, ext, _ := isnUnitInstalled(t)
	slot, constructor := 0, sqltype.Ean13
	if family == "isbn" { slot, constructor = 3, sqltype.Isbn }
	if family == "isbn13" { slot, constructor = 6, sqltype.Isbn13 }
	if family == "ismn" { slot, constructor = 9, sqltype.Ismn }
	if family == "ismn13" { slot, constructor = 12, sqltype.Ismn13 }
	if family == "issn" { slot, constructor = 15, sqltype.Issn }
	if family == "issn13" { slot, constructor = 18, sqltype.Issn13 }
	base := e.CatalogRead().MustType(ext.Members[slot])
	unowned := *base
	unowned.Extension = ids.ObjectAddress{}
	if err := (stored.TypeProvider{}).Validate(e.CatalogRead(), &unowned); err == nil {
		t.Fatal("bare installed-family descriptor accepted")
	}
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE SCHEMA isn_authority_dst", "ALTER TYPE public."+family+" RENAME TO moved", "ALTER TYPE public.moved SET SCHEMA isn_authority_dst"} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	base = e.CatalogRead().MustType(base.Address())
	if err := extension.StockIsn.ValidateExtension(e.CatalogRead(), ext); err != nil {
		t.Fatal("genuine renamed/moved manifest rejected", err)
	}
	for _, corrupt := range []func(*catalog.State){
		func(state *catalog.State) {
			proc := *state.MustProcedure(ext.Members[slot+1])
			proc.Result++
			state.SetDescriptor(proc.Address(), &proc)
		},
		func(state *catalog.State) {
			proc := *state.MustProcedure(ext.Members[slot+2])
			proc.Signature = "25"
			state.SetDescriptor(proc.Address(), &proc)
		},
		func(state *catalog.State) {
			proc := *state.MustProcedure(ext.Members[slot+1])
			proc.Implementation = extension.StockCitext
			state.SetDescriptor(proc.Address(), &proc)
		},
		func(state *catalog.State) {
			proc := *state.MustProcedure(ext.Members[slot+1])
			proc.Implementation = new(extension.IsnProvider)
			state.SetDescriptor(proc.Address(), &proc)
		},
		func(state *catalog.State) {
			wrong := *base
			var err error
			wrong.SQLType, err = sqltype.Citext(base.SQLType.ID(), family, "public."+family)
			if err != nil {
				t.Fatal(err)
			}
			state.SetDescriptor(base.Address(), &wrong)
		},
		func(state *catalog.State) {
			proc := *state.MustProcedure(ext.Members[slot+1])
			proc.ObjectNamespace = ids.ObjectAddress{}
			state.SetDescriptor(proc.Address(), &proc)
		},
		func(state *catalog.State) {
			proc := *state.MustProcedure(ext.Members[slot+2])
			proc.Extension = ids.ObjectAddress{}
			state.SetDescriptor(proc.Address(), &proc)
		},
		func(state *catalog.State) {
			array := *state.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)})
			foreign, err := constructor(base.SQLType.ID(), base.Name(), base.SQLType.SQLName())
			if err != nil {
				t.Fatal(err)
			}
			array.SQLType = sqltype.LookupArray(array.SQLType.ID(), array.Name(), foreign)
			state.SetDescriptor(array.Address(), &array)
		},
		func(state *catalog.State) {
			wrong := *base
			wrong.Extension = ids.ObjectAddress{}
			state.SetDescriptor(wrong.Address(), &wrong)
		},
	} {
		candidate := catalog.CloneState(e.catalog.State)
		corrupt(candidate)
		if err := extension.StockIsn.ValidateExtension(e.catalog.Read(candidate), ext); err == nil {
			t.Fatal("forged installed provider accepted")
		}
		view := e.catalog.Read(candidate)
		if _, err := extension.InstalledTypes(view, ext); err == nil {
			t.Fatal("ownership role accepted forged installed provider")
		}
		if _, err := extension.OwnerMetadata(view, e.Roles(), ext); err == nil {
			t.Fatal("owner metadata accepted forged installed provider")
		}
	}
	if err := extension.StockIsn.ValidateExtension(e.CatalogRead(), ext); err != nil {
		t.Fatal("validation mutated original catalog", err)
	}
	array := e.catalog.State.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)})
	if _, err := extension.MemberTypeRole(e.CatalogRead(), ext, array); err == nil {
		t.Fatal("automatic array accepted as a direct ownership member")
	}
}
