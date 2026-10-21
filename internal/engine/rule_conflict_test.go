package engine

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestRuleConflictInstantiation(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	original := ruleUnitInsert(e, src, 1, 10).Binding()
	action := ruleUnitInsert(e, sink, 1, 10)
	recipe := &analyze.InsertConflict{Targets: action.Binding().Targets, Keys: []analyze.ConflictKey{{Attribute: 1}}, Update: &analyze.UpdateBinding{Assignments: []expr.Expr{ruleUnitInt(sink, 20)}}, UpdateTargets: []int{1}, Width: 2}
	action = action.With(func(p *RuleActionParts) { p.Binding.Insert.Conflict = recipe })
	first, err := composeRuleAction(auth, original, "INSERT", ruleDefinition{}, action)
	if err != nil {
		t.Fatal(err)
	}
	// Execution replaces the command's normalized predicate through its state.
	state := newInsertConflictState(first.Insert.Conflict)
	state.Predicate = ruleUnitInt(sink, 30)
	state.markAffected(ids.RowID(42), 0)
	if first.Insert.Conflict.Predicate == nil {
		t.Fatal("conflict state does not execute the command's clause")
	}
	second, err := composeRuleAction(auth, original, "INSERT", ruleDefinition{}, action)
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

func TestRuleConflictAttributeRefresh(t *testing.T) {
	e, auth, _, sink := ruleUnitCatalog(t)
	b := ruleUnitInsert(e, sink, 1, 10).Binding()
	ref := b.Ref
	ref.Address.SubID = sink.Attributes[1]
	target := plan.ColumnExpr{Info: ruleColumnInfo(sink.Columns[1]), Origin: ref, Scope: 1, Frame: 1, Slot: 1}
	excluded := target
	excluded.Slot = 3
	b.Insert.Conflict = &analyze.InsertConflict{Targets: b.Targets, Reads: []int{0}, ActionReads: []int{1}, UpdateTargets: []int{1}, Width: 2, Update: &analyze.UpdateBinding{Assignments: []expr.Expr{excluded}, Predicate: target}}
	current := *sink
	current.Attributes = []int32{2, 1}
	current.Columns = []catalog.Column{sink.Columns[1], sink.Columns[0]}
	current.Index = map[string]int{"v": 0, "id": 1}
	catalog := catalog.CloneState(auth.Catalog.State())
	catalog.SetDescriptor(sink.Address(), &current)
	view := e.catalog.read(catalog)
	check := func(next analyze.Binding) {
		t.Helper()
		if !slices.Equal(next.Targets, []int{1, 0}) || !slices.Equal(next.Insert.Conflict.Targets, []int{1, 0}) || !slices.Equal(next.Insert.Conflict.UpdateTargets, []int{0}) || !slices.Equal(next.Insert.Conflict.Reads, []int{1}) || !slices.Equal(next.Insert.Conflict.ActionReads, []int{0}) {
			t.Fatal("conflict manifests did not follow stable attributes")
		}
		if next.Insert.Conflict.Update.Assignments[0].(plan.ColumnExpr).Slot != 2 || next.Insert.Conflict.Update.Predicate.(plan.ColumnExpr).Slot != 0 || next.Insert.Conflict.Width != 2 {
			t.Fatal("target/EXCLUDED namespaces lost their distinct halves")
		}
	}
	next, err := refreshRuleBinding(view, b, &current)
	if err != nil {
		t.Fatal(err)
	}
	check(next)
	mapped, err := remapRuleBinding(view, b, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }, nil)
	if err != nil {
		t.Fatal(err)
	}
	check(mapped)
	if b.Targets[0] != 0 || b.Insert.Conflict.UpdateTargets[0] != 1 || b.Insert.Conflict.Update.Assignments[0].(plan.ColumnExpr).Slot != 3 || b.Relation != sink {
		t.Fatal("refresh/remap mutated the stored template")
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
			composed, err := composeRuleAction(auth, original, event, ruleDefinition{}, action)
			if err != nil {
				t.Fatal(err)
			}
			join := composed.Query.Root.(*plan.ProjectNode).Child.(*plan.JoinNode)
			if _, ok := join.Left.(*plan.SingletonNode); !ok || len(composed.QuerySources) != 0 {
				t.Fatal("independent action still depends on event rows")
			}
			ref := original.Ref
			ref.Address.SubID = src.Attributes[0]
			action = action.With(func(p *RuleActionParts) {
				p.Binding.Expressions = [][]expr.Expr{{plan.ColumnExpr{Info: ruleColumnInfo(src.Columns[0]), Origin: ref, Scope: analyze.RulePseudoScope, Frame: 1}, ruleUnitInt(sink, 8)}}
			})
			dependent, err := composeRuleAction(auth, original, event, ruleDefinition{}, action)
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
