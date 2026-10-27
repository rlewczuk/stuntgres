package schemacmds_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

func TestHstoreRowsProviderAuthority(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	stage := hstoreArraysStage(t, e, op)
	publication, err := e.ValidateCatalogStage(t.Context(), stage)
	if err != nil { t.Fatal(err) }
	if _, err := e.PublishCatalogOperation(op, nil, publication, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil { t.Fatal(err) }
	view := e.CatalogRead()
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "hstore"}))
	if extension.HstoreSkeysSlot != 54 || extension.HstoreSvalsSlot != 55 || extension.HstoreEachSlot != 56 || len(ext.Members) != extension.HstoreMemberCount { t.Fatal("append-only row member frontier") }
	for i, symbol := range []string{"hstore_skeys", "hstore_svals", "hstore_each"} {
		p := view.MustProcedure(ext.Members[extension.HstoreSkeysSlot+i])
		actual, err := extension.ProcedureSymbol(view, p)
		if err != nil || actual != symbol || !p.Installed.ReturnsSet || p.Installed.Rows != 1000 || p.Installed.Parallel != 's' || !p.Strict || p.Signature != extension.HstoreSignature(view.MustType(ext.Members[0]).SQLType.ID()) { t.Fatal("selected rows declaration", err) }
		if name, err := extension.ProcedureInstallationName(view, p); err != nil || name != []string{"skeys", "svals", "each"}[i] {
			t.Fatal("selected rows installation name", name, err)
		}
		if i == 2 {
			out := p.OutputDeclaration()
			if p.Result != 2249 || out == nil || out.Inputs() != 1 || out.Len() != 2 || out.Type(0) != 25 || out.Type(1) != 25 || out.Name(0) != "hs" || out.Name(1) != "key" || out.Name(2) != "value" { t.Fatal("exact each OUT declaration") }
		} else if p.Result != 25 || p.OutputDeclaration() != nil { t.Fatal("scalar TEXT declaration") }
		for _, mutate := range []func(*catalog.Procedure){
			func(p *catalog.Procedure) { p.Installed.ReturnsSet = false },
			func(p *catalog.Procedure) { p.Installed.Rows = 0 },
			func(p *catalog.Procedure) { p.Installed.Parallel = 'u' },
			func(p *catalog.Procedure) { p.Installed.ArgumentNames[0] = "forged" },
			func(p *catalog.Procedure) { p.Installed.Outputs = nil },
			func(p *catalog.Procedure) { p.Installed.Outputs = catalog.NewRoutineOutputs([]ids.OID{25, 25}, "hs", "key", "value") },
			func(p *catalog.Procedure) { p.Installed.Outputs = catalog.NewRoutineOutputs([]ids.OID{25, 25}, "hs", "value", "key") },
			func(p *catalog.Procedure) { p.Display.Source = "hstore_out" },
		} {
			state := catalog.CloneState(view.State())
			bad := *p
			mutate(&bad)
			// Removing an already absent OUT declaration leaves skeys/svals
			// unchanged; only each must reject a missing capability.
			if bad.Installed == p.Installed && bad.Display == p.Display {
				continue
			}
			state.SetDescriptor(bad.Address(), &bad)
			for _, identify := range []func(catalog.Read, *catalog.Procedure) (string, error){extension.ProcedureSymbol, extension.ProcedureInstallationName} {
				if _, err := identify(e.Catalog.Read(state), &bad); err == nil {
					t.Fatal("forged rows identity admitted")
				}
			}
			if err := extension.StockHstore.ValidateExtension(e.Catalog.Read(state), ext); err == nil { t.Fatal("forged rows declaration admitted") }
		}
		state := catalog.CloneState(view.State())
		estimate := *p
		estimate.Rows = 17
		state.SetDescriptor(estimate.Address(), &estimate)
		if err := extension.StockHstore.ValidateExtension(e.Catalog.Read(state), ext); err != nil { t.Fatal("mutable estimate changed installed identity", err) }
	}
	// Even the genuine each capability cannot be transplanted to an incumbent
	// scalar member: the finite slot recipe, not provider identity alone, owns it.
	state := catalog.CloneState(view.State())
	scalar := *view.MustProcedure(ext.Members[extension.HstoreKeysSlot])
	scalar.Installed.Outputs = view.MustProcedure(ext.Members[extension.HstoreEachSlot]).Installed.Outputs
	state.SetDescriptor(scalar.Address(), &scalar)
	if _, err := extension.ProcedureSymbol(e.Catalog.Read(state), &scalar); err == nil {
		t.Fatal("each OUT capability admitted on scalar slot")
	}
	if err := extension.StockHstore.ValidateExtension(e.Catalog.Read(state), ext); err == nil {
		t.Fatal("transplanted scalar OUT declaration admitted")
	}
}
