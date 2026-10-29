package schemacmds_test

import (
	"fmt"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"testing"
)

func TestHstoreUpdateProvider(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	stage := hstoreArraysStage(t, e, op)
	publication, err := e.ValidateCatalogStage(t.Context(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PublishCatalogOperation(op, nil, publication, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	view := e.CatalogRead()
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "hstore"}))
	base := ext.Members[0].ObjectID
	if extension.HstoreEachSlot != 56 || extension.HstoreConcatSlot != 57 || extension.HstoreUpdateSliceSlot != 61 || extension.HstoreConcatOperatorSlot != 62 || extension.HstoreDeleteHstoreOperatorSlot != 65 || len(ext.Installation.Members) != 66 || len(ext.Members) != 67 {
		t.Fatal("append-only update frontier and generated companion")
	}
	for _, tc := range []struct {
		slot              int
		symbol, signature string
	}{
		{57, "hstore_concat", fmt.Sprintf("%d,%d", base, base)},
		{58, "hstore_delete", fmt.Sprintf("%d,25", base)},
		{59, "hstore_delete_array", fmt.Sprintf("%d,1009", base)},
		{60, "hstore_delete_hstore", fmt.Sprintf("%d,%d", base, base)},
		{61, "hstore_slice_to_hstore", fmt.Sprintf("%d,1009", base)},
	} {
		p := view.MustProcedure(ext.Members[tc.slot])
		symbol, err := extension.ProcedureSymbol(view, p)
		if err != nil || symbol != tc.symbol || string(p.Signature) != tc.signature || p.Result != base || !p.Strict {
			t.Fatal("local routine result", tc.slot, err)
		}
		for _, mutate := range []func(*catalog.Procedure){func(p *catalog.Procedure) { p.Result = 0 }, func(p *catalog.Procedure) { p.Result = 25 }, func(p *catalog.Procedure) { p.Display.Source = "hstore_out" }, func(p *catalog.Procedure) { p.Strict = false }} {
			state := catalog.CloneState(view.State())
			bad := *p
			mutate(&bad)
			state.SetDescriptor(bad.Address(), &bad)
			if err := extension.StockHstore.ValidateExtension(e.Catalog.Read(state), ext); err == nil {
				t.Fatal("forged routine accepted", tc.slot)
			}
		}
	}
	for _, tc := range []struct {
		slot, backer int
		right        ids.OID
	}{{62, 57, base}, {63, 58, 25}, {64, 59, 1009}, {65, 60, base}} {
		selected, ok := view.Operator(ext.Members[tc.slot])
		if !ok {
			t.Fatal("missing update operator")
		}
		if selected.Left != base || selected.Right != tc.right || selected.Result != base || selected.Result == 0 || selected.Function != ext.Members[tc.backer].ObjectID {
			t.Fatal("destination-local operator result", tc.slot)
		}
		if err := catalog.ValidateInstalledClassOrigin(view, selected); err != nil {
			t.Fatal("genuine recipe operator rejected", tc.slot, err)
		}
		if err := catalog.ValidateExtensionMembership(view, selected); err != nil {
			t.Fatal("genuine current membership rejected", tc.slot, err)
		}
		for _, mutate := range []func(*catalog.Operator){
			func(o *catalog.Operator) { o.Result = 0 },
			func(o *catalog.Operator) { o.Result = 25 },
			func(o *catalog.Operator) { o.Function = ext.Members[2].ObjectID },
			func(o *catalog.Operator) {
				o.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 999999}
			},
			func(o *catalog.Operator) { o.ObjectRevision = 0 },
			func(o *catalog.Operator) {
				o.Extension = ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 999999}
			},
			func(o *catalog.Operator) {
				origin := *o.Origin
				installation := *origin.Installation
				origin.Installation = &installation
				o.Origin = &origin
			},
		} {
			state := catalog.CloneState(view.State())
			bad := *selected
			mutate(&bad)
			state.SetDescriptor(bad.Address(), &bad)
			if err := extension.StockHstore.ValidateExtension(e.Catalog.Read(state), ext); err == nil {
				t.Fatal("forged operator accepted", tc.slot)
			}
			if err := (routine.OperatorProvider{}).Validate(e.Catalog.Read(state), &bad); err == nil {
				t.Fatal("forged operator authority accepted", tc.slot)
			}
			if current, _ := view.Operator(selected.Address()); current != selected {
				t.Fatal("failed validation mutated source")
			}
		}
	}
}
