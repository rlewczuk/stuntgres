package schemacmds_test

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestHstoreRecordProviderAuthority(t *testing.T) {
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
	h := ext.Members[0].ObjectID
	if extension.HstoreRecordConstructorSlot != 51 || extension.HstorePopulateRecordSlot != 52 || extension.HstorePopulateRecordOperatorSlot != 53 || len(ext.Members) != 54 {
		t.Fatal("changed established member frontier")
	}
	for _, tc := range []struct {
		slot              int
		signature, symbol string
		result            ids.OID
	}{{51, "2249", "hstore_from_record", h}, {52, fmt.Sprintf("2283,%d", h), "hstore_populate_record", 2283}} {
		p := view.MustProcedure(ext.Members[tc.slot])
		symbol, err := extension.ProcedureSymbol(view, p)
		if err != nil || symbol != tc.symbol || string(p.Signature) != tc.signature || p.Result != tc.result || p.Strict || p.Volatility != expr.Immutable || p.RoutineKind != catalog.FunctionRoutine {
			t.Fatal("declared record provider", p, err)
		}
		for _, mutate := range []func(*catalog.Procedure){func(p *catalog.Procedure) { p.Signature = "25" }, func(p *catalog.Procedure) { p.Result = 25 }, func(p *catalog.Procedure) { p.Strict = true }, func(p *catalog.Procedure) { p.Display.Source = "hstore_from_text" }} {
			state := catalog.CloneState(view.State())
			bad := *p
			mutate(&bad)
			state.SetDescriptor(bad.Address(), &bad)
			if err := extension.StockHstore.ValidateExtension(e.Catalog.Read(state), ext); err == nil {
				t.Fatal("forged record declaration accepted")
			}
		}
	}
	operator, _ := view.Operator(ext.Members[53])
	if operator.Left != 2283 || operator.Right != h || operator.Result != 2283 || operator.Function != ext.Members[52].ObjectID || operator.Commutator != 0 || operator.Negator != 0 || operator.Restriction != 0 || operator.Join != 0 {
		t.Fatal("operator declaration/backer")
	}
	for _, mutate := range []func(*catalog.Operator){func(o *catalog.Operator) { o.Left = h }, func(o *catalog.Operator) { o.Result = h }, func(o *catalog.Operator) { o.Function = ext.Members[51].ObjectID }} {
		state := catalog.CloneState(view.State())
		bad := *operator
		mutate(&bad)
		state.SetDescriptor(bad.Address(), &bad)
		if err := extension.StockHstore.ValidateExtension(e.Catalog.Read(state), ext); err == nil {
			t.Fatal("forged record operator accepted")
		}
	}
	metadata, err := extension.OwnerMetadata(view, e.RoleStore.PublishedRead(&e.Catalog.Owner, view), ext)
	if err != nil {
		t.Fatal(err)
	}
	owners, found, err := extension.DecodeOwners(strings.TrimSpace(strings.TrimPrefix(metadata, "-- ")))
	if err != nil || !found || len(owners.Members) != 53 {
		t.Fatal("owner inventory", err)
	}
	for _, tc := range []struct {
		slot      int
		signature string
	}{{51, "2249"}, {52, "2283,hstore"}, {53, "2283,hstore"}} {
		found := false
		for _, m := range owners.Members {
			if m.Slot == tc.slot {
				found = true
				if m.Signature != tc.signature {
					t.Fatal("dynamic OID in symbolic record signature", m)
				}
			}
		}
		if !found {
			t.Fatal("missing record owner role", tc.slot)
		}
	}
}
