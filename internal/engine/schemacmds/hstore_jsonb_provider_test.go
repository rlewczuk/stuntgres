package schemacmds_test

import (
	"context"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

func TestHstoreJSONBProviderIdentity(t *testing.T) {
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
	base := view.MustType(ext.Members[extension.HstoreTypeSlot])
	if len(ext.Members) != extension.HstoreMemberCount {
		t.Fatal("incomplete hstore manifest")
	}
	for i, symbol := range []string{"hstore_to_jsonb", "hstore_to_jsonb_loose"} {
		slot := 9 + i
		proc := view.MustProcedure(ext.Members[slot])
		got, err := extension.ProcedureSymbol(view, proc)
		if err != nil || got != symbol || proc.Result != 3802 || proc.Signature != extension.HstoreSignature(base.SQLType.ID()) || !proc.Strict || proc.Volatility != expr.Immutable || proc.Owner != base.Owner || proc.Extension != ext.Address() {
			t.Fatal("JSON installed declaration", got, err)
		}
		for _, cause := range []string{"array_result", "json_result", "signature", "symbol", "strict", "volatility", "owner", "revision", "member", "class"} {
			t.Run(symbol+"/"+cause, func(t *testing.T) {
				candidate := catalog.CloneState(view.State())
				bad := *ext
				bad.Members = slices.Clone(ext.Members)
				p := *proc
				switch cause {
				case "array_result":
					p.Result = 1009
				case "json_result":
					p.Result = 114
				case "signature":
					p.Signature = "25"
				case "symbol":
					p.Display.Source = "hstore_out"
				case "strict":
					p.Strict = false
				case "volatility":
					p.Volatility = expr.Volatile
				case "owner":
					p.Owner = authorization.PrincipalRef{}
				case "revision":
					p.ObjectRevision = 0
				case "member":
					bad.Members[slot] = bad.Members[extension.HstoreArrayProcedureSlot]
				case "class":
					bad.Members[slot].ClassID = ids.TypeClassID
				}
				candidate.SetDescriptor(p.Address(), &p)
				candidate.SetDescriptor(bad.Address(), &bad)
				if err := extension.StockHstore.ValidateExtension(e.Catalog.Read(candidate), &bad); err == nil {
					t.Fatal("corrupt JSON role admitted", cause)
				}
				if view.MustExtension(ext.Address()) != ext || view.MustProcedure(proc.Address()) != proc {
					t.Fatal("source catalog mutated")
				}
			})
		}
	}
}

func TestHstoreJSONBProviderPublication(t *testing.T) {
	for _, slot := range []int{9, 10} {
		t.Run([]string{"strict", "loose"}[slot-9], func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			before := e.CatalogRead().State()
			stage := hstoreArraysStage(t, e, op)
			view := e.StageRead(stage)
			ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "hstore"}))
			if len(ext.Members) != extension.HstoreMemberCount { t.Fatal("missing hstore provider roles") }
			view.MustProcedure(ext.Members[slot]).Result = 1009
			if _, err := e.ValidateCatalogStage(context.Background(), stage); err == nil {
				t.Fatal("invalid JSON publication admitted")
			}
			if e.CatalogRead().State() != before {
				t.Fatal("partial catalog publication")
			}
		})
	}
	for _, cause := range []string{"canceled", "late_cast_collision"} {
		t.Run(cause, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			before := e.CatalogRead().State()
			stage := hstoreArraysStage(t, e, op)
			view := e.StageRead(stage)
			ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "hstore"}))
			base := view.MustType(ext.Members[extension.HstoreTypeSlot])
			cast, ok := view.Cast(base.SQLType.ID(), 3802)
			if !ok { t.Fatal("missing staged cast") }
			if cause == "canceled" {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				if _, err := e.ValidateCatalogStage(ctx, stage); err == nil { t.Fatal("canceled publication admitted") }
			} else {
				duplicate := *cast
				duplicate.OID += 1000
				if err := stage.Create(&duplicate); err == nil { t.Fatal("late same-pair collision overwrote authentic cast") }
				current, ok := e.StageRead(stage).Cast(base.SQLType.ID(), 3802)
				if !ok || current != cast { t.Fatal("failed collision changed staged pair index") }
			}
			e.DiscardCatalogStage(stage)
			if e.CatalogRead().State() != before { t.Fatal("failed stage leaked procedures/cast") }
		})
	}
}
