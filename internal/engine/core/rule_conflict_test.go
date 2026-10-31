package core_test

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
)

func TestRuleConflictInstantiation(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	original := ruleUnitInsert(e, src, 1, 10).Binding()
	action := ruleUnitInsert(e, sink, 1, 10)
	recipe := &analyze.InsertConflict{Targets: action.Binding().Targets, Keys: []analyze.ConflictKey{{Attribute: 1}}, Update: &analyze.UpdateBinding{Assignments: []expr.Expr{ruleUnitInt(sink, 20)}}, UpdateTargets: []int{1}, Width: 2}
	action = action.With(func(p *stored.RuleActionParts) { p.Binding.Insert.Conflict = recipe })
	first, err := view.ComposeRuleAction(auth, original, "INSERT", stored.Rule{}, action)
	if err != nil {
		t.Fatal(err)
	}
	// Execution replaces the command's normalized predicate through its state.
	state := dml.NewInsertConflictState(first.Insert.Conflict)
	state.Predicate = ruleUnitInt(sink, 30)
	state.MarkAffected(ids.RowID(42), 0)
	if first.Insert.Conflict.Predicate == nil {
		t.Fatal("conflict state does not execute the command's clause")
	}
	second, err := view.ComposeRuleAction(auth, original, "INSERT", stored.Rule{}, action)
	if err != nil {
		t.Fatal(err)
	}
	if first.Insert.Conflict == second.Insert.Conflict || second.Insert.Conflict == recipe || first.Insert.Conflict == recipe {
		t.Fatal("composed commands share conflict workspace")
	}
	for _, c := range []*analyze.InsertConflict{recipe, second.Insert.Conflict} {
		if c.Predicate != nil {
			t.Fatal("execution state escaped its composed command")
		}
	}
	if !slices.Equal(second.Insert.Conflict.UpdateTargets, []int{1}) {
		t.Fatal("instantiation changed analyzed recipe")
	}
}

func TestRuleIndependentActionProducer(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	for _, event := range []string{"UPDATE", "DELETE"} {
		t.Run(event, func(t *testing.T) {
			original := ruleUnitInsert(e, src, 1, 10).Binding()
			if event == "UPDATE" {
				original.Update = &analyze.UpdateBinding{Assignments: []expr.Expr{ruleUnitInt(src, 1), ruleUnitInt(src, 10)}}
			}
			action := ruleUnitInsert(e, sink, 7, 8)
			composed, err := view.ComposeRuleAction(auth, original, event, stored.Rule{}, action)
			if err != nil {
				t.Fatal(err)
			}
			join := composed.Query.Root.(*plan.ProjectNode).Child.(*plan.JoinNode)
			if _, ok := join.Left.(*plan.SingletonNode); !ok || len(composed.QuerySources) != 0 {
				t.Fatal("independent action still depends on event rows")
			}
			ref := original.Ref
			ref.Address.SubID = src.Attributes[0]
			action = action.With(func(p *stored.RuleActionParts) {
				p.Binding.Expressions = [][]expr.Expr{{plan.ColumnExpr{Info: view.RuleColumnInfo(src.Columns[0]), Origin: ref, Scope: analyze.RulePseudoScope, Frame: 1}, ruleUnitInt(sink, 8)}}
			})
			dependent, err := view.ComposeRuleAction(auth, original, event, stored.Rule{}, action)
			if err != nil {
				t.Fatal(err)
			}
			if len(dependent.QuerySources) == 0 {
				t.Fatal("OLD demand lost event-row authority")
			}
			if action.Binding().Expressions[0][0].(plan.ColumnExpr).Scope != analyze.RulePseudoScope {
				t.Fatal("composition mutated stored action")
			}
		})
	}
}
