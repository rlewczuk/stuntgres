package stored

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
)

func TestAggregateOrderedFrozenRecipePartitions(t *testing.T) {
	for _, shape := range []string{"zero_direct", "fixed", "paired_any"} {
		t.Run(shape, func(t *testing.T) {
			read, view, raw, path, aggregate := aggregateRecipeFixture(t)
			declaration := *aggregate
			owned := *aggregate.Aggregate
			declaration.Aggregate = &owned
			owned.Definition.Kind, owned.Definition.FinalModify = "o", "w"
			row := raw.Args[0]
			raw.Within = true
			raw.Order = []parser.OrderKey{{Expr: row}}
			switch shape {
			case "zero_direct":
				raw.Args = nil
			case "fixed":
				declaration.Signature = "23,23"
				owned.Definition.Kind, owned.Definition.DirectArgs = "h", 1
			case "paired_any":
				declaration.Signature, declaration.Result = "2276", 20
				owned.Variadic = true
				owned.Definition.Kind, owned.Definition.DirectArgs = "h", 1
				owned.Definition.Functions = [8]ids.OID{3971, 3987}
				owned.Definition.TransitionType = 2281
				owned.Definition.FinalExtra = true
				owned.Definition.Initial, owned.Definition.HasInitial = "", false
				procedures := routine.BuiltinProcedures(declaration.Owner)
				for i := range procedures {
					p := &procedures[i]
					if p.Address().ObjectID == 3971 || p.Address().ObjectID == 3987 {
						read.State().SetDescriptor(p.Address(), p)
					}
				}
				raw.Args = []parser.Expr{row, row}
				raw.Order = append(raw.Order, parser.OrderKey{Expr: row, Desc: true})
			}
			read.State().SetDescriptor(declaration.Address(), &declaration)
			selected, err := view.AggregateRecipeProcedure(read, path, raw)
			if err != nil || selected != &declaration {
				t.Fatal("valid frozen direct/row partition refused", selected, err)
			}
			for name, change := range map[string]func(*parser.AggregateExpr){
				"missing_within": func(a *parser.AggregateExpr) { a.Within = false },
				"missing_order": func(a *parser.AggregateExpr) { a.Order = nil },
				"wrong_direct": func(a *parser.AggregateExpr) { a.Args = append(append([]parser.Expr(nil), a.Args...), row) },
				"window": func(a *parser.AggregateExpr) { a.Window = &parser.WindowSpec{} },
				"distinct": func(a *parser.AggregateExpr) { a.Distinct = true },
				"explicit_variadic": func(a *parser.AggregateExpr) { a.Variadic = true },
			} {
				t.Run(name, func(t *testing.T) {
					changed := raw
					change(&changed)
					if _, err := view.AggregateRecipeProcedure(read, path, changed); err == nil {
						t.Fatal("malformed frozen ordered aggregate admitted")
					}
				})
			}
			ordinary := declaration
			ordinaryOwned := owned
			ordinary.Aggregate = &ordinaryOwned
			ordinaryOwned.Definition.Kind = "n"
			read.State().SetDescriptor(ordinary.Address(), &ordinary)
			if _, err := view.AggregateRecipeProcedure(read, path, raw); err == nil {
				t.Fatal("ordinary replacement accepted frozen WITHIN GROUP syntax")
			}
		})
	}
}
