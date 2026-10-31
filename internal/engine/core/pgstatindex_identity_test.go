package core_test

import (
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestPgstatindexProviderCanonicalRemap(t *testing.T) {
	e := engine.New()
	ext := pgstatindexUnitInstalled(t, e)
	view := e.CatalogRead()
	recipe := ext.Installation
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 940000}, ext.TargetNamespace: {ClassID: ids.NamespaceClassID, ObjectID: 940001}}
	for i, a := range recipe.Members {
		mapping[a] = ids.ObjectAddress{ClassID: a.ClassID, ObjectID: ids.OID(940100 + i)}
	}
	remap := extension.DescriptorRemapper(mapping)
	mapped := remap(ext).(*catalog.Extension)
	next := mapped.Installation
	if next == recipe || !next.Valid() || next.Cohort != mapping[ext.Address()] || next.Implementation != recipe.Implementation || next.Version != "1.5" || len(next.Declarations) != 8 {
		t.Fatal("lost sealed release")
	}
	for i, a := range recipe.Members {
		original := view.MustProcedure(a)
		p := remap(original).(*catalog.Procedure)
		canonical := next.Declarations[i].(*catalog.Procedure)
		member := extension.PgstattupleMembers[i]
		if original.Name() != member.Name || original.Signature != member.Signature || original.Result != member.Result || original.Display.Source != member.Source || canonical.Display.Source != member.Source || canonical.Installed != member.Declaration {
			t.Fatal("canonical remap changed manifest declaration", i)
		}
		if p.Origin.Installation != next || p.Origin.Declaration != i || !p.Origin.Valid(mapping[a]) || next.Members[i] != mapping[a] || canonical.Address() != mapping[a] || canonical.Namespace() != mapping[ext.TargetNamespace] || canonical.Origin != nil {
			t.Fatal("split canonical authority", i)
		}
		if p.Installed != original.Installed || canonical.Installed != original.Installed || canonical.Signature != original.Signature || canonical.Result != original.Result || canonical.Display != original.Display {
			t.Fatal("changed selected signature or OUT authority", i)
		}
		if recipe.Declarations[i].Address() != a || original.Origin.Installation != recipe {
			t.Fatal("mutated predecessor installation")
		}
	}
	copied := *recipe
	if copied.Valid() {
		t.Fatal("copied installation forged a seal")
	}
	foreign := engine.New()
	foreignExt := pgstatindexUnitInstalled(t, foreign)
	original := view.MustProcedure(ext.Members[0])
	foreignProc := foreign.CatalogRead().MustProcedure(foreignExt.Members[0])
	for _, bad := range []*catalog.Procedure{original, foreignProc} {
		ref := foreign.Catalog.Domain.Ref(view.Epoch(), bad.Address())
		if err := extension.ValidatePgstattupleIdentity(view, bad, ref); err == nil {
			t.Fatal("foreign-domain carrier accepted")
		}
	}
	state := catalog.CloneState(view.State())
	bad := *original
	origin := *original.Origin
	forged := *recipe
	origin.Installation = &forged
	bad.Origin = &origin
	state.SetDescriptor(bad.Address(), &bad)
	if err := extension.StockPgstattuple.ValidateProcedure(e.Catalog.Read(state), &bad); err == nil {
		t.Fatal("copied installation accepted")
	}
}
