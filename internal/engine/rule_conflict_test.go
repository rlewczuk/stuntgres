package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestRuleConflictInstantiation(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	original := ruleUnitInsert(e, src, 1, 10).binding
	action := ruleUnitInsert(e, sink, 1, 10)
	recipe := &boundInsertConflict{targets: action.binding.targets, keys: []conflictKey{{attribute: 1}}, update: &boundUpdate{assignments: []expr.Expr{ruleUnitInt(sink, 20)}}, updateTargets: []int{1}, width: 2}
	action.binding.insert.conflict = recipe
	first, err := composeRuleAction(auth, original, "INSERT", ruleDefinition{}, action)
	if err != nil {
		t.Fatal(err)
	}
	first.insert.conflict.markAffected(ids.RowID(42), 0)
	first.insert.conflict.selected = append(first.insert.conflict.selected, sink.Address())
	first.insert.conflict.frame = make([]sqltype.Value, 4)
	first.insert.conflict.catalog = auth.Catalog.State()
	first.insert.conflict.overlays = make(map[*uniqueBoundKey]*insertWriteKeyOverlay)
	second, err := composeRuleAction(auth, original, "INSERT", ruleDefinition{}, action)
	if err != nil {
		t.Fatal(err)
	}
	if first.insert.conflict == second.insert.conflict || second.insert.conflict == recipe || first.insert.conflict == recipe {
		t.Fatal("composed commands share conflict workspace")
	}
	for _, c := range []*boundInsertConflict{recipe, second.insert.conflict} {
		if c.affected != 0 || len(c.affectedRows) != 0 || len(c.selected) != 0 || c.frame != nil || c.catalog != nil || c.overlays != nil || c.ownRowSlots != nil {
			t.Fatal("execution state escaped its composed command")
		}
	}
	if !slices.Equal(second.insert.conflict.updateTargets, []int{1}) {
		t.Fatal("instantiation changed analyzed recipe")
	}
}

func TestRuleConflictAttributeRefresh(t *testing.T) {
	e, auth, _, sink := ruleUnitCatalog(t)
	b := ruleUnitInsert(e, sink, 1, 10).binding
	ref := b.ref
	ref.Address.SubID = sink.Attributes[1]
	target := columnExpression{Info: ruleColumnInfo(sink.Columns[1]), origin: ref, scope: 1, frame: 1, slot: 1}
	excluded := target
	excluded.slot = 3
	b.insert.conflict = &boundInsertConflict{targets: b.targets, reads: []int{0}, actionReads: []int{1}, updateTargets: []int{1}, width: 2, update: &boundUpdate{assignments: []expr.Expr{excluded}, predicate: target}}
	current := *sink
	current.Attributes = []int32{2, 1}
	current.Columns = []catalog.Column{sink.Columns[1], sink.Columns[0]}
	current.Index = map[string]int{"v": 0, "id": 1}
	catalog := catalog.CloneState(auth.Catalog.State())
	catalog.SetDescriptor(sink.Address(), &current)
	view := e.catalog.read(catalog)
	check := func(next binding) {
		t.Helper()
		if !slices.Equal(next.targets, []int{1, 0}) || !slices.Equal(next.insert.conflict.targets, []int{1, 0}) || !slices.Equal(next.insert.conflict.updateTargets, []int{0}) || !slices.Equal(next.insert.conflict.reads, []int{1}) || !slices.Equal(next.insert.conflict.actionReads, []int{0}) {
			t.Fatal("conflict manifests did not follow stable attributes")
		}
		if next.insert.conflict.update.assignments[0].(columnExpression).slot != 2 || next.insert.conflict.update.predicate.(columnExpression).slot != 0 || next.insert.conflict.width != 2 {
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
	if b.targets[0] != 0 || b.insert.conflict.updateTargets[0] != 1 || b.insert.conflict.update.assignments[0].(columnExpression).slot != 3 || b.relation != sink {
		t.Fatal("refresh/remap mutated the stored template")
	}
}

func TestRuleIndependentActionProducer(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	for _, event := range []string{"UPDATE", "DELETE"} {
		t.Run(event, func(t *testing.T) {
			original := ruleUnitInsert(e, src, 1, 10).binding
			if event == "UPDATE" {
				original.update = &boundUpdate{assignments: []expr.Expr{ruleUnitInt(src, 1), ruleUnitInt(src, 10)}}
			}
			action := ruleUnitInsert(e, sink, 7, 8)
			composed, err := composeRuleAction(auth, original, event, ruleDefinition{}, action)
			if err != nil {
				t.Fatal(err)
			}
			join := composed.query.Root.(*projectNode).child.(*joinNode)
			if _, ok := join.left.(*singletonNode); !ok || len(composed.querySources) != 0 {
				t.Fatal("independent action still depends on event rows")
			}
			ref := original.ref
			ref.Address.SubID = src.Attributes[0]
			action.binding.expressions = [][]expr.Expr{{columnExpression{Info: ruleColumnInfo(src.Columns[0]), origin: ref, scope: rulePseudoScope, frame: 1}, ruleUnitInt(sink, 8)}}
			dependent, err := composeRuleAction(auth, original, event, ruleDefinition{}, action)
			if err != nil {
				t.Fatal(err)
			}
			if len(dependent.querySources) == 0 {
				t.Fatal("OLD demand lost event-row authority")
			}
			if action.binding.expressions[0][0].(columnExpression).scope != rulePseudoScope {
				t.Fatal("composition mutated stored action")
			}
		})
	}
}
