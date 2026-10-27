package schemacmds_test

import (
	"context"
	"fmt"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestHstoreKeysProvider(t *testing.T) {
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
	if len(ext.Members) != extension.HstoreMemberCount || extension.HstoreRecordConstructorSlot != 51 || extension.HstorePopulateRecordSlot != 52 || extension.HstorePopulateRecordOperatorSlot != 53 {
		t.Fatal("append-only member roles")
	}
	for _, tc := range []struct {
		slot                    int
		name, symbol, signature string
		result                  ids.OID
	}{
		{40, "exists_all", "hstore_exists_all", fmt.Sprintf("%d,1009", base), 16}, {41, "exists_any", "hstore_exists_any", fmt.Sprintf("%d,1009", base), 16}, {42, "hs_contains", "hstore_contains", fmt.Sprintf("%d,%d", base, base), 16}, {43, "hs_contained", "hstore_contained", fmt.Sprintf("%d,%d", base, base), 16}, {44, "akeys", "hstore_akeys", fmt.Sprint(base), 1009}, {45, "avals", "hstore_avals", fmt.Sprint(base), 1009},
	} {
		p := view.MustProcedure(ext.Members[tc.slot])
		symbol, err := extension.ProcedureSymbol(view, p)
		if err != nil || symbol != tc.symbol || p.Name() != tc.name || string(p.Signature) != tc.signature || p.Result != tc.result || !p.Strict {
			t.Fatal("selected declaration", tc.slot, err)
		}
		for _, mutate := range []func(*catalog.Procedure){func(p *catalog.Procedure) { p.Result = 25 }, func(p *catalog.Procedure) { p.Strict = false }, func(p *catalog.Procedure) { p.Signature = "25" }, func(p *catalog.Procedure) { p.Display.Source = "hstore_out" }} {
			candidate := catalog.CloneState(view.State())
			bad := *p
			mutate(&bad)
			candidate.SetDescriptor(bad.Address(), &bad)
			if err := extension.StockHstore.ValidateExtension(e.Catalog.Read(candidate), ext); err == nil {
				t.Fatal("corrupt routine admitted", tc.slot)
			}
			if view.MustProcedure(p.Address()) != p {
				t.Fatal("mutated source procedure")
			}
		}
	}
	for _, tc := range []struct {
		slot, backer, commutator int
		right                    ids.OID
	}{{46, 36, 0, 25}, {47, 40, 0, 1009}, {48, 41, 0, 1009}, {49, 42, 50, base}, {50, 43, 49, base}} {
		selected, ok := view.Operator(ext.Members[tc.slot])
		if !ok {
			t.Fatal("operator missing")
		}
		var comm ids.OID
		if tc.commutator != 0 {
			comm = ext.Members[tc.commutator].ObjectID
		}
		if selected.Left != base || selected.Right != tc.right || selected.Result != 16 || selected.Function != ext.Members[tc.backer].ObjectID || selected.Commutator != comm || selected.Restriction != 5040 || selected.Join != 5041 {
			t.Fatal("operator graph", tc.slot)
		}
		for _, mutate := range []func(*catalog.Operator){func(o *catalog.Operator) {
			o.Commutator = 0
			if comm == 0 {
				o.Commutator = selected.Address().ObjectID
			}
		}, func(o *catalog.Operator) { o.Restriction = 0 }, func(o *catalog.Operator) { o.Join = 0 }, func(o *catalog.Operator) { o.Function = ext.Members[2].ObjectID }, func(o *catalog.Operator) {
			o.Right = 25
			if tc.right == 25 {
				o.Right = 1009
			}
		}} {
			candidate := catalog.CloneState(view.State())
			bad := *selected
			mutate(&bad)
			candidate.SetDescriptor(bad.Address(), &bad)
			if err := extension.StockHstore.ValidateExtension(e.Catalog.Read(candidate), ext); err == nil {
				t.Fatal("corrupt operator admitted", tc.slot)
			}
			if current, _ := view.Operator(selected.Address()); current != selected {
				t.Fatal("mutated source operator")
			}
		}
	}
	// An invalid selected declaration never reaches catalog publication.
	other := engine.New()
	otherOp, otherDone := enginetest.RoleOperation(t, other, "postgres")
	defer otherDone()
	before := other.CatalogRead().State()
	badStage := hstoreArraysStage(t, other, otherOp)
	staged := other.StageRead(badStage)
	installed := staged.MustExtension(staged.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "hstore"}))
	staged.MustProcedure(installed.Members[44]).Result = 25
	if _, err := other.ValidateCatalogStage(context.Background(), badStage); err == nil || other.CatalogRead().State() != before {
		t.Fatal("partial invalid publication", err)
	}
}
