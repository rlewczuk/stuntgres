package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func isnInstallation(t *testing.T) (*core.Engine, *catalog.Extension) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	before := e.CatalogRead()
	result, err := e.Execute(op, &parser.CreateExtension{Name: parser.Identifier{Text: "isn"}}, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != nil {
		result.Rows.Close()
		t.Fatal("extension returned rows")
	}
	if before.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "isn"}) != (ids.ObjectAddress{}) {
		t.Fatal("installation mutated prior snapshot")
	}
	v := e.CatalogRead()
	return e, v.MustExtension(v.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "isn"}))
}

func TestIsnProviderDeclarations(t *testing.T) {
	e, ext := isnInstallation(t)
	v := e.CatalogRead()
	if len(ext.Installation.Members) != 422 || len(ext.Members) != 430 {
		t.Fatal("closed recipe", len(ext.Installation.Members), len(ext.Members))
	}
	counts := map[ids.OID]int{}
	for slot, address := range ext.Installation.Members {
		counts[address.ClassID]++
		if err := v.Owner().Provider(address.ClassID).Validate(v, v.Get(address)); err != nil {
			t.Fatal(slot, err)
		}
		if proc, ok := v.Procedure(address); ok {
			_, _, codecErr := extension.IsnCodecType(v, proc)
			if (codecErr == nil) != (slot < 24) {
				t.Fatal("noncodec selected as codec", slot, codecErr)
			}
			bad := *proc
			bad.Result++
			state := catalog.CloneState(v.State())
			state.SetDescriptor(address, &bad)
			if err := extension.StockIsn.ValidateProcedure(v.WithState(state), &bad); err == nil {
				t.Fatal("corrupt declaration", slot)
			}
		}
	}
	for class, want := range map[ids.OID]int{ids.TypeClassID: 8, ids.ProcedureClassID: 217, ids.OperatorClassID: 168, ids.OperatorFamilyClassID: 1, ids.OperatorClassCatalogID: 8, ids.CastClassID: 20} {
		if counts[class] != want {
			t.Fatal(class, counts[class], want)
		}
	}
	metadata, err := extension.OwnerMetadata(v, e.RoleStore.PublishedRead(v.Owner(), v), ext)
	if err != nil || metadata == "" {
		t.Fatal(err)
	}
	// A rejected candidate cannot publish an altered backer into the live graph.
	state := catalog.CloneState(v.State())
	p := *v.MustProcedure(ext.Installation.Members[extension.IsnComparisonStart])
	p.Display.Source = "foreign"
	state.SetDescriptor(p.Address(), &p)
	if err := extension.StockIsn.ValidateExtension(v.WithState(state), ext); err == nil {
		t.Fatal("rejected declaration published")
	}
	if v.MustProcedure(p.Address()).Display.Source != "int8eq" || e.CatalogRead().MustProcedure(p.Address()) != v.MustProcedure(p.Address()) {
		t.Fatal("candidate mutated committed provider")
	}
}

func TestIsnConversionAuthority(t *testing.T) {
	e, ext := isnInstallation(t)
	v := e.CatalogRead()
	other, foreignExt := isnInstallation(t)
	foreign := other.CatalogRead()
	ean := v.MustType(ext.Installation.Members[0])
	for role := range 8 {
		typ := v.MustType(ext.Installation.Members[role*3])
		if err := extension.StockIsn.ValidateISNConversion(v, typ, ean); err != nil {
			t.Fatal(role, err)
		}
		if err := extension.StockIsn.ValidateISNConversion(v, ean, typ); err != nil {
			t.Fatal(role, err)
		}
		for _, change := range []func(*catalog.Type){
			func(d *catalog.Type) { d.Origin = nil },
			func(d *catalog.Type) { i := *d.Origin.Installation; o := *d.Origin; o.Installation = &i; d.Origin = &o },
			func(d *catalog.Type) { o := *d.Origin; o.Declaration = ((role + 1) % 8) * 3; d.Origin = &o },
			func(d *catalog.Type) { d.Owner = (&authorization.Domain{}).Principal(d.Owner.ID()) },
			func(d *catalog.Type) { d.ObjectRevision = 0 },
		} {
			bad := *typ
			change(&bad)
			state := catalog.CloneState(v.State())
			state.SetDescriptor(bad.Address(), &bad)
			current := v.WithState(state)
			to := ean
			if role == 0 {
				to = &bad
			}
			if err := extension.StockIsn.ValidateISNConversion(current, &bad, to); err == nil {
				t.Fatal("forged role authority", role)
			}
		}
		if err := extension.StockIsn.ValidateISNConversion(v, foreign.MustType(foreignExt.Installation.Members[role*3]), ean); err == nil {
			t.Fatal("foreign equal-ID role", role)
		}
	}
	if err := new(extension.IsnProvider).ValidateISNConversion(v, ean, ean); err == nil {
		t.Fatal("unregistered provider")
	}
	// Retiring the original header and lawful detachment do not revoke origins.
	state := catalog.CloneState(v.State())
	for _, a := range ext.Members {
		d := state.Get(a)
		next, err := catalog.WithExtensionMembership(d, ids.ObjectAddress{}, d.Revision())
		if err != nil {
			t.Fatal(err)
		}
		state.SetDescriptor(a, next)
	}
	state.UnbindName(catalog.DescriptorNameKey(ext))
	state.RemoveDescriptor(ext.Address())
	surviving := v.WithState(state)
	for role := range 8 {
		if err := extension.StockIsn.ValidateISNConversion(surviving, surviving.MustType(ext.Installation.Members[role*3]), surviving.MustType(ean.Address())); err != nil {
			t.Fatal("retired header", role, err)
		}
	}
}

func TestIsnOrderingAuthority(t *testing.T) {
	e, ext := isnInstallation(t)
	v := e.CatalogRead()
	for role := range 8 {
		typ := v.MustType(ext.Installation.Members[role*3]).SQLType
		class, ok := v.OperatorClass(ext.Installation.Members[extension.IsnClassStart+role].ObjectID)
		if !ok {
			t.Fatal("missing class", role)
		}
		identity, ok := v.IndexClass(class.Address().ObjectID)
		if !ok {
			t.Fatal("missing class", role)
		}
		kernel, capture, err := routine.BindCapturedIndexOrdering(v, typ.Usage(), identity)
		if err != nil || capture == nil {
			t.Fatal(role, err)
		}
		s := kernel.Support()
		family, _ := v.OperatorFamily(ext.Installation.Members[extension.IsnFamilySlot])
		member, _ := v.FamilySupport(class.Family, class.InputType, class.InputType, 1)
		proc := v.MustProcedure(ext.Installation.Members[extension.IsnSupportStart+role])
		if s.Provider != sqltype.IsnIndexSupport || s.ClassRevision != uint64(class.Revision()) || s.FamilyRevision != uint64(family.Revision()) || s.Revision != uint64(member.Revision()) || s.RoutineRevision != uint64(proc.Revision()) {
			t.Fatal("four retained revisions", s)
		}
		for i, address := range [...]ids.ObjectAddress{class.Address(), family.Address(), member.Address(), proc.Address()} {
			if capture.References()[i] != v.Owner().Domain.Ref(v.Epoch(), address) {
				t.Fatal("lost exact retained ordering reference", role, i)
			}
		}
		if err := capture.Validate(typ.Usage(), kernel.DatumComparison()); err != nil {
			t.Fatal(err)
		}
		other := v.MustType(ext.Installation.Members[((role+1)%8)*3]).SQLType
		if err := capture.Validate(other.Usage(), kernel.DatumComparison()); err == nil {
			t.Fatal("foreign operand generation paired with retained support")
		}
		for _, mutate := range []func(*catalog.State){
			func(s *catalog.State) { s.RemoveDescriptor(member.Address()) },
			func(s *catalog.State) { bad := *member; bad.Number = 2; s.SetDescriptor(bad.Address(), &bad) },
			func(s *catalog.State) {
				bad := *member
				bad.Function = ext.Installation.Members[extension.IsnSupportStart+(role+1)%8].ObjectID
				s.SetDescriptor(bad.Address(), &bad)
			},
			func(s *catalog.State) { bad := *proc; bad.Origin = nil; s.SetDescriptor(bad.Address(), &bad) },
			func(s *catalog.State) { bad := *class; bad.Origin = nil; s.SetDescriptor(bad.Address(), &bad) },
		} {
			state := catalog.CloneState(v.State())
			mutate(state)
			if _, _, err := routine.BindCapturedIndexOrdering(v.WithState(state), typ.Usage(), identity); err == nil {
				t.Fatal("forged support admitted", role)
			}
		}
		if _, err := sqltype.BindIndexOrdering(typ.Usage(), identity); err == nil {
			t.Fatal("empty/NULL-only ordering bypasses support")
		}
	}
}
