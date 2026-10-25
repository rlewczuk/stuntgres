package enginetests_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func aggregateIdentityCall(name, arg, shape, id string) string {
	switch shape {
	case "all":
		return name + "(ALL " + arg + ")"
	case "distinct":
		return name + "(DISTINCT " + arg + ")"
	case "ordered":
		return name + "(" + arg + " ORDER BY " + arg + " DESC NULLS FIRST)"
	case "filter":
		return name + "(" + arg + ") FILTER(WHERE " + id + "<=2)"
	case "combined":
		return name + "(DISTINCT " + arg + " ORDER BY " + arg + " DESC NULLS FIRST) FILTER(WHERE " + id + "<=3)"
	case "filter_owner":
		return name + "(" + arg + ") FILTER(WHERE o.f)"
	default:
		return name + "(" + arg + ")"
	}
}

func TestAggregateRoutineDeclarationIdentity(t *testing.T) {
	for _, pair := range [][2]string{{"bool_and", "every"}, {"variance", "var_samp"}, {"stddev", "stddev_samp"}} {
		for order := range 2 {
			for _, outer := range []bool{false, true} {
				shapes := []string{"plain", "all", "distinct", "ordered", "filter", "combined"}
				if outer {
					shapes = append(shapes, "filter_owner")
				}
				for _, shape := range shapes {
					name := pair[order] + "/" + shape
					if outer {
						name += "/outer"
					}
					t.Run(name, func(t *testing.T) {
						arg, id, from := "v::double precision", "id", "t"
						if pair[0] == "bool_and" {
							arg = "f"
						}
						if outer {
							arg, id, from = "o."+arg, "o.id", "t o"
						}
						if shape == "filter_owner" {
							arg = "1::double precision"
							if pair[0] == "bool_and" {
								arg = "TRUE"
							}
						}
						calls := make([]string, 3)
						for i, selected := range []string{pair[order], pair[1-order], pair[order]} {
							calls[i] = aggregateIdentityCall(selected, arg, shape, id)
							if outer {
								calls[i] = "(SELECT " + calls[i] + ")"
							}
						}
						e, query, source := queryCoreFixture(t, "SELECT "+strings.Join(calls, ",")+" FROM "+from)
						defer source.Close()
						project := query.Root.(*plan.ProjectNode)
						node := project.Child.(*plan.AggregateNode)
						if len(node.Calls) != 2 {
							t.Fatalf("distinct declarations require two slots, got %d: %#v", len(node.Calls), node.Calls)
						}
						for i, call := range node.Calls {
							selected := pair[order]
							if i == 1 {
								selected = pair[1-order]
							}
							if call.Routine == (ids.ObjectRef{}) || call.Procedure == nil || call.Procedure.Name() != selected || call.Procedure.Address() != call.Routine.Address || e.CatalogRead().MustProcedure(call.Routine.Address) != call.Procedure {
								t.Fatal("not the genuine captured selected declaration", selected, call)
							}
						}
						if node.Calls[0].Routine == node.Calls[1].Routine || node.Calls[0].Procedure == node.Calls[1].Procedure {
							t.Fatal("different declarations have interchangeable proof", node.Calls)
						}
						var slots []plan.GroupExpr
						for _, target := range project.Expressions {
							if outer {
								sub := target.(plan.SubqueryExpr)
								child := sub.QueryPlan.Root.(*plan.ProjectNode)
								target = child.Expressions[0]
							}
							slots = append(slots, target.(plan.GroupExpr))
						}
						if slots[0].Slot != slots[2].Slot || slots[0].Slot == slots[1].Slot || slots[0].Frame != slots[2].Frame || slots[0].Frame != slots[1].Frame {
							t.Fatal("declaration reuse or lexical-owner frame lost", slots)
						}
					})
				}
			}
		}
	}
	t.Run("modifier_and_volatile_boundaries", aggregateRoutineIdentityBoundaries)
}

func aggregateRoutineIdentityBoundaries(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE SEQUENCE ai_identity_sequence")
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	bind := func(call string) plan.AggregateExpr {
		t.Helper()
		raw := enginetest.Statement(t, "SELECT "+call).(*parser.Select).Targets[0]
		bound, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(call, err)
		}
		return bound.(plan.AggregateExpr)
	}
	first := bind("bool_and(TRUE)")
	if repeat := bind("bool_and(ALL TRUE)"); repeat.Slot != first.Slot {
		t.Fatal("same selected declaration no longer reuses", first, repeat)
	}
	for _, call := range []string{
		"bool_and(DISTINCT TRUE)", "bool_and(TRUE) FILTER(WHERE TRUE)", "bool_and(TRUE) FILTER(WHERE FALSE)",
		"bool_and(TRUE ORDER BY TRUE)", "bool_and(TRUE ORDER BY TRUE DESC)", "bool_and(TRUE ORDER BY TRUE NULLS FIRST)",
	} {
		before := len(b.Grouping.Aggregates)
		if got := bind(call); got.Slot != before {
			t.Fatal("different modifier shared a slot", call, got, before)
		}
	}
	for _, call := range []string{
		"bool_and(nextval('ai_identity_sequence')>0)",
		"bool_and(TRUE) FILTER(WHERE nextval('ai_identity_sequence')>0)",
		"bool_and(TRUE ORDER BY nextval('ai_identity_sequence'))",
	} {
		a, z := bind(call), bind(call)
		if a.Slot == z.Slot || a.Properties().Volatility != expr.Volatile || z.Properties().Volatility != expr.Volatile {
			t.Fatal("volatile occurrence reused", call, a, z)
		}
	}
	original := b.Grouping.Aggregates[first.Slot]
	siblingSlot := bind("every(TRUE)")
	sibling := b.Grouping.Aggregates[siblingSlot.Slot]
	if original.Routine == sibling.Routine || original.Procedure == sibling.Procedure {
		t.Fatal("real captured sibling identity is not distinct", original, sibling)
	}
	for _, mutate := range []struct {
		name   string
		change func(*plan.AggregateCall)
	}{
		{"selected reference", func(c *plan.AggregateCall) { c.Routine = sibling.Routine }},
		{"owning procedure", func(c *plan.AggregateCall) { c.Procedure = sibling.Procedure }},
		{"borrowed procedure", func(c *plan.AggregateCall) { borrowed := *c.Procedure; c.Procedure = &borrowed }},
		{"result usage", func(c *plan.AggregateCall) { c.Provider = functions.NewCountProvider(b.Type(sqltype.Int4).Usage()) }},
		{"direct argument", func(c *plan.AggregateCall) { c.Direct = append([]expr.Expr(nil), c.Arguments...) }},
		{"provider operation", func(c *plan.AggregateCall) { c.Operation++ }},
		{"stage width", func(c *plan.AggregateCall) { c.StageWidth++ }},
		{"null policy", func(c *plan.AggregateCall) { c.IncludeNulls = !c.IncludeNulls }},
	} {
		altered := original
		mutate.change(&altered)
		if b.SameAggregateCall(original, altered) {
			t.Fatal(mutate.name, "omitted from identity")
		}
	}
}
