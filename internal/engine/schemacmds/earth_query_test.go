package schemacmds_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestEarthQueryExpression(t *testing.T) {
	earthExpressionOperations(t, []sqltype.EarthOperation{sqltype.EarthDistance, sqltype.EarthBox})
}

func TestEarthQueryReownOrdering(t *testing.T) {
	earthReownOperations(t, []sqltype.EarthOperation{sqltype.EarthDistance, sqltype.EarthBox})
}

func TestEarthQueryProvider(t *testing.T) {
	e, ext, earth := earthUnitInstalled(t)
	view := e.CatalogRead()
	if len(ext.Members) != 12 || extension.EarthPointProcedureSlot != 8 || extension.EarthPointOperatorSlot != 9 || extension.EarthDistanceProcedureSlot != 10 || extension.EarthBoxProcedureSlot != 11 {
		t.Fatal("stable provider roles")
	}
	for _, op := range []sqltype.EarthOperation{sqltype.EarthDistance, sqltype.EarthBox} {
		slot, ok := extension.EarthOperationSlot(op)
		back, reverse := extension.EarthSlotOperation(slot)
		if !ok || !reverse || back != op {
			t.Fatal("operation/slot mapping")
		}
		p := view.MustProcedure(ext.Members[slot])
		symbol, err := extension.ProcedureSymbol(view, p)
		if err != nil || symbol != op.Name() || !p.Strict || p.Volatility != expr.Immutable || p.DeclarationBody == nil {
			t.Fatal("query declaration", symbol, err)
		}
		helper, err := extension.EarthQueryConversionHelper(view, p)
		want := "sec_to_gc"
		if op == sqltype.EarthBox {
			want = "gc_to_sec"
			if p.Result != ids.OID(earth.SQLType.DomainBase().Type.ID()) {
				t.Fatal("box result acquired domain identity")
			}
		}
		if err != nil || helper.Name() != want {
			t.Fatal("exact conversion role", err)
		}
		for _, mutate := range []func(*catalog.Procedure){
			func(p *catalog.Procedure) { p.Strict = false },
			func(p *catalog.Procedure) { p.Result = ids.OID(earth.SQLType.ID()) },
			func(p *catalog.Procedure) { p.Signature = "701,701" },
			func(p *catalog.Procedure) { p.Implementation = extension.StockCube },
			func(p *catalog.Procedure) { p.DeclarationBody = nil },
		} {
			state := catalog.CloneState(view.State())
			bad := *p
			mutate(&bad)
			state.SetDescriptor(bad.Address(), &bad)
			if err := extension.StockEarthdistance.ValidateExtension(view.WithState(state), ext); err == nil {
				t.Fatal("forged query declaration admitted", op)
			}
		}
		badHelper := *helper
		badHelper.Implementation = extension.StockCube
		state := catalog.CloneState(view.State())
		state.SetDescriptor(helper.Address(), &badHelper)
		if _, err := extension.ProcedureSymbol(view.WithState(state), p); err == nil {
			t.Fatal("query accepted foreign helper family")
		}
		// A real duplicate query declaration is rejected by the ordinary stage
		// without publishing any candidate definitions into the owning engine.
		before := e.Catalog.State
		stage := e.BeginCatalogStage(view)
		duplicate := *p
		duplicate.ObjectAddress.ObjectID = 990000 + ids.OID(slot)
		var conflict catalog.Conflict
		err = stage.Create(&duplicate)
		e.DiscardCatalogStage(stage)
		if !errors.As(err, &conflict) || e.Catalog.State != before {
			t.Fatal("query conflict changed publication", op, err)
		}
	}
	if _, ok := extension.EarthSlotOperation(extension.EarthPointOperatorSlot); ok {
		t.Fatal("point operator became a query operation")
	}
}

func TestEarthQueryOwners(t *testing.T) {
	e, ext, _ := earthUnitInstalled(t)
	view := e.CatalogRead()
	roles := e.RoleStore.PublishedRead(&e.Catalog.Owner, view)
	metadata, err := extension.OwnerMetadata(view, roles, ext)
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := extension.DecodeOwners(strings.TrimPrefix(strings.TrimSpace(metadata), "-- "))
	if err != nil || !found {
		t.Fatal(err)
	}
	record = extensionOwnerRecipeOrder(record)
	for slot, signature := range map[int]string{
		extension.EarthDistanceProcedureSlot: "public.earth, public.earth",
		extension.EarthBoxProcedureSlot:      "public.earth, double precision",
	} {
		member := record.Members[slot]
		if member.Kind != "procedure" || member.Signature != signature || member.Declaration == nil || *member.Declaration != slot {
			t.Fatal("query owner identity", member)
		}
		bad := record
		bad.Members = append([]extension.MemberOwner(nil), record.Members...)
		bad.Members[slot].Signature = "double precision, double precision"
		data, err := json.Marshal(bad)
		if err != nil {
			t.Fatal(err)
		}
		if err := extensionOwnerAdmissionError(e, extension.OwnersMarker+" "+base64.StdEncoding.EncodeToString(data)); err == nil {
			t.Fatal("foreign query owner signature admitted")
		}
		state := catalog.CloneState(view.State())
		before := state.MustProcedure(ext.Members[slot])
		if _, err := extension.ImportOwners(context.Background(), view.WithState(state), roles, []extension.OwnerRecord{bad}); err == nil || state.MustProcedure(ext.Members[slot]) != before {
			t.Fatal("late invalid query owner changed candidate", err)
		}
	}
}
