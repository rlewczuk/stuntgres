package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestRuleConflictInstantiation(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	original := ruleUnitInsert(e, src, 1, 10).binding
	action := ruleUnitInsert(e, sink, 1, 10)
	recipe := &boundInsertConflict{targets: action.binding.Targets, keys: []conflictKey{{attribute: 1}}, update: &boundUpdate{Assignments: []expr.Expr{ruleUnitInt(sink, 20)}}, updateTargets: []int{1}, width: 2}
	action.binding.Insert.Conflict = insertConflictSlotOf(recipe)
	first, err := composeRuleAction(auth, original, "INSERT", ruleDefinition{}, action)
	if err != nil {
		t.Fatal(err)
	}
	insertConflictOf(first.Insert.Conflict).markAffected(ids.RowID(42), 0)
	insertConflictOf(first.Insert.Conflict).selected = append(insertConflictOf(first.Insert.Conflict).selected, sink.Address())
	insertConflictOf(first.Insert.Conflict).frame = make([]sqltype.Value, 4)
	insertConflictOf(first.Insert.Conflict).catalog = auth.Catalog.State()
	insertConflictOf(first.Insert.Conflict).overlays = make(map[*uniqueBoundKey]*insertWriteKeyOverlay)
	second, err := composeRuleAction(auth, original, "INSERT", ruleDefinition{}, action)
	if err != nil {
		t.Fatal(err)
	}
	if insertConflictOf(first.Insert.Conflict) == insertConflictOf(second.Insert.Conflict) || insertConflictOf(second.Insert.Conflict) == recipe || insertConflictOf(first.Insert.Conflict) == recipe {
		t.Fatal("composed commands share conflict workspace")
	}
	for _, c := range []*boundInsertConflict{recipe, insertConflictOf(second.Insert.Conflict)} {
		if c.affected != 0 || len(c.affectedRows) != 0 || len(c.selected) != 0 || c.frame != nil || c.catalog != nil || c.overlays != nil || c.ownRowSlots != nil {
			t.Fatal("execution state escaped its composed command")
		}
	}
	if !slices.Equal(insertConflictOf(second.Insert.Conflict).updateTargets, []int{1}) {
		t.Fatal("instantiation changed analyzed recipe")
	}
}

func TestRuleConflictAttributeRefresh(t *testing.T) {
	e, auth, _, sink := ruleUnitCatalog(t)
	b := ruleUnitInsert(e, sink, 1, 10).binding
	ref := b.Ref
	ref.Address.SubID = sink.Attributes[1]
	target := plan.ColumnExpr{Info: ruleColumnInfo(sink.Columns[1]), Origin: ref, Scope: 1, Frame: 1, Slot: 1}
	excluded := target
	excluded.Slot = 3
	b.Insert.Conflict = insertConflictSlotOf(&boundInsertConflict{targets: b.Targets, reads: []int{0}, actionReads: []int{1}, updateTargets: []int{1}, width: 2, update: &boundUpdate{Assignments: []expr.Expr{excluded}, Predicate: target}})
	current := *sink
	current.Attributes = []int32{2, 1}
	current.Columns = []catalog.Column{sink.Columns[1], sink.Columns[0]}
	current.Index = map[string]int{"v": 0, "id": 1}
	catalog := catalog.CloneState(auth.Catalog.State())
	catalog.SetDescriptor(sink.Address(), &current)
	view := e.catalog.read(catalog)
	check := func(next binding) {
		t.Helper()
		if !slices.Equal(next.Targets, []int{1, 0}) || !slices.Equal(insertConflictOf(next.Insert.Conflict).targets, []int{1, 0}) || !slices.Equal(insertConflictOf(next.Insert.Conflict).updateTargets, []int{0}) || !slices.Equal(insertConflictOf(next.Insert.Conflict).reads, []int{1}) || !slices.Equal(insertConflictOf(next.Insert.Conflict).actionReads, []int{0}) {
			t.Fatal("conflict manifests did not follow stable attributes")
		}
		if insertConflictOf(next.Insert.Conflict).update.Assignments[0].(plan.ColumnExpr).Slot != 2 || insertConflictOf(next.Insert.Conflict).update.Predicate.(plan.ColumnExpr).Slot != 0 || insertConflictOf(next.Insert.Conflict).width != 2 {
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
	if b.Targets[0] != 0 || insertConflictOf(b.Insert.Conflict).updateTargets[0] != 1 || insertConflictOf(b.Insert.Conflict).update.Assignments[0].(plan.ColumnExpr).Slot != 3 || b.Relation != sink {
		t.Fatal("refresh/remap mutated the stored template")
	}
}

func TestRuleIndependentActionProducer(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	for _, event := range []string{"UPDATE", "DELETE"} {
		t.Run(event, func(t *testing.T) {
			original := ruleUnitInsert(e, src, 1, 10).binding
			if event == "UPDATE" {
				original.Update = &boundUpdate{Assignments: []expr.Expr{ruleUnitInt(src, 1), ruleUnitInt(src, 10)}}
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
			action.binding.Expressions = [][]expr.Expr{{plan.ColumnExpr{Info: ruleColumnInfo(src.Columns[0]), Origin: ref, Scope: rulePseudoScope, Frame: 1}, ruleUnitInt(sink, 8)}}
			dependent, err := composeRuleAction(auth, original, event, ruleDefinition{}, action)
			if err != nil {
				t.Fatal(err)
			}
			if len(dependent.QuerySources) == 0 {
				t.Fatal("OLD demand lost event-row authority")
			}
			if action.binding.Expressions[0][0].(plan.ColumnExpr).Scope != rulePseudoScope {
				t.Fatal("composition mutated stored action")
			}
		})
	}
}
