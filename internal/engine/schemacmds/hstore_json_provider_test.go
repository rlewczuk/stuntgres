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

func TestHstoreJSONProviderIdentity(t *testing.T) {
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
		t.Fatal("manifest size")
	}
	for i, symbol := range []string{"hstore_to_json", "hstore_to_json_loose"} {
		slot := extension.HstoreJSONProcedureSlot + i
		proc := view.MustProcedure(ext.Members[slot])
		got, err := extension.ProcedureSymbol(view, proc)
		if err != nil || got != symbol || proc.Result != 114 || proc.Signature != extension.HstoreSignature(base.SQLType.ID()) || !proc.Strict || proc.Volatility != expr.Immutable || proc.Owner != base.Owner || proc.Extension != ext.Address() {
			t.Fatal("JSON installed declaration", got, err)
		}
		for _, cause := range []string{"array_result", "jsonb_result", "signature", "symbol", "strict", "volatility", "owner", "member", "class"} {
			t.Run(symbol+"/"+cause, func(t *testing.T) {
				candidate := catalog.CloneState(view.State())
				bad := *ext
				bad.Members = slices.Clone(ext.Members)
				p := *proc
				switch cause {
				case "array_result":
					p.Result = 1009
				case "jsonb_result":
					p.Result = 3802
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

func TestHstoreJSONProviderPublication(t *testing.T) {
	for _, slot := range []int{extension.HstoreJSONProcedureSlot, extension.HstoreJSONLooseProcedureSlot} {
		t.Run([]string{"strict", "loose"}[slot-extension.HstoreJSONProcedureSlot], func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			before := e.CatalogRead().State()
			stage := hstoreArraysStage(t, e, op)
			view := e.StageRead(stage)
			ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "hstore"}))
			view.MustProcedure(ext.Members[slot]).Result = 1009
			if _, err := e.ValidateCatalogStage(context.Background(), stage); err == nil {
				t.Fatal("invalid JSON publication admitted")
			}
			if e.CatalogRead().State() != before {
				t.Fatal("partial catalog publication")
			}
		})
	}
}
