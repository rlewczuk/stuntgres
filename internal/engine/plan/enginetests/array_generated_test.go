package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestArrayGeneratedPhysicalIOImmutability(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	text, ok := view.LookupType(sqltype.Text)
	if !ok {
		t.Fatal("missing text descriptor")
	}
	for _, id := range []sqltype.ID{1000, 1001, 1003, 1005, 1007, 1009, 1014, 1015, 1016, 1021, 1022, 1028, 1115, 1182, 1183, 1185, 1187, 1231, 1270, 2210, 2951} {
		array, ok := view.LookupType(id)
		if !ok {
			t.Fatal("missing array descriptor", id)
		}
		whole, err := sqltype.Domain(60001, "whole", "public.whole", array.Usage())
		if err != nil {
			t.Fatal(err)
		}
		for _, descriptor := range []*sqltype.Descriptor{array, whole} {
			leaf := plan.ColumnExpr{Info: expr.Info{Usage: descriptor.Usage(), Property: expr.Properties{RowDependent: true}}}
			identityPlan, err := sqltype.PlanCoercion(descriptor.Usage(), descriptor.Usage(), sqltype.Explicit)
			if err != nil {
				t.Fatal(err)
			}
			identity := plan.CoercionExpr{Info: expr.Info{Usage: descriptor.Usage()}, Child: leaf, Plan: identityPlan, Mode: sqltype.Explicit}
			if !plan.GenerationImmutable(identity) {
				t.Fatal("array identity coercion rejected", descriptor.Name())
			}
			for _, input := range []bool{false, true} {
				from, to := descriptor.Usage(), text.Usage()
				if input {
					from, to = to, from
				}
				coercionPlan, err := sqltype.PlanCoercion(from, to, sqltype.Explicit)
				if err != nil {
					t.Fatal(err)
				}
				child := plan.ColumnExpr{Info: expr.Info{Usage: from, Property: expr.Properties{RowDependent: true}}}
				cast := plan.CoercionExpr{Info: expr.Info{Usage: to}, Child: child, Plan: coercionPlan, Mode: sqltype.Explicit}
				if plan.GenerationImmutable(cast) {
					t.Fatal("surviving array IO accepted as immutable", descriptor.Name(), input)
				}
				// The existing consumer follows surviving providers, not inherited
				// volatility from a removed CASE branch. No blanket array ban.
				pruned := plan.CaseExpr{Info: expr.Info{Usage: descriptor.Usage(), Property: expr.Properties{Volatility: expr.Stable, RowDependent: true}}, Otherwise: leaf}
				if !plan.GenerationImmutable(pruned) {
					t.Fatal("pruned IO volatility rejected immutable survivor")
				}
			}
		}
	}
}
