package engine

import (
	"context"
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func insertConflictUniqueFixture(t testing.TB) partialIndexFixture {
	t.Helper()
	f := newPartialIndexFixture(t)
	owner, ok := f.e.Roles().Lookup("postgres")
	if !ok {
		t.Fatal("fixture bootstrap principal is missing")
	}
	f.parent.owner = owner.Ref
	f.index.owner = owner.Ref
	definition := *f.index.sqlIndex
	definition.unique = true
	f.index.sqlIndex = &definition
	f.store.versions = f.store.versions[:1]
	f.store.versions[0].born = 1
	root, err := buildIndexRoot(context.Background(), f.parent, &definition, f.store)
	if err != nil {
		t.Fatal(err)
	}
	f.e.indexRoots[f.index.address] = root
	return f
}
func insertConflictUnitScope(t *testing.T, f partialIndexFixture) (*StatementScope, ObjectRef) {
	t.Helper()
	op, done := roleOperation(t, f.e, "postgres")
	t.Cleanup(done)
	scope, err := f.e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !scope.finished {
			_ = scope.Finish(errors.New("unit rollback"))
		}
	})
	if err := scope.gate(&parser.Insert{}); err != nil {
		t.Fatal(err)
	}
	ref := ObjectRef{owner: f.e.owner, Epoch: f.e.state.epoch, Address: f.parent.address}
	action := &boundInsertConflict{keys: []conflictKey{{attribute: 10}}, predicate: f.predicate, reads: []int{0, 1}, targets: []int{0, 1}}
	ctx := EvalContext{Context: op.Context(), Scope: 1, Frame: 1, Input: scope.input, Output: scope.output}
	if err := action.selectArbiters(&ctx, f.parent, f.e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	scope.insertConflict = action
	return scope, ref
}
func insertConflictUnitRow(t testing.TB, f partialIndexFixture, n int64) []sqltype.Value {
	t.Helper()
	value, err := sqltype.Integer(f.parent.columns[0].Usage.Type, n)
	if err != nil {
		t.Fatal(err)
	}
	return []sqltype.Value{value, sqltype.Boolean(f.parent.columns[1].Usage.Type, true)}
}
func TestInsertConflictAdmissionClaimsAndPrefix(t *testing.T) {
	f := insertConflictUniqueFixture(t)
	scope, ref := insertConflictUnitScope(t, f)
	manager := f.e.locks
	first := insertConflictUnitRow(t, f, 20)
	admitted, err := scope.admitInsertRow(ref, first, nil)
	if err != nil || !admitted {
		t.Fatal(admitted, err)
	}
	baseline := manager.checkpointUnique(scope.ticket, nil)
	if len(baseline) != 1 {
		t.Fatal("accepted proposal owns one actual key", baseline)
	}
	for _, row := range [][]sqltype.Value{insertConflictUnitRow(t, f, 10), first} {
		admitted, err = scope.admitInsertRow(ref, row, [][]sqltype.Value{first})
		if err != nil || admitted {
			t.Fatal("duplicate was not omitted", admitted, err)
		}
		after := manager.checkpointUnique(scope.ticket, nil)
		if len(after) != len(baseline) {
			t.Fatal("omission retained a new grant", after)
		}
		for key, mode := range baseline {
			if after[key] != mode {
				t.Fatal("omission lost preexisting grant")
			}
		}
	}
	admitted, err = scope.admitInsertRow(ref, insertConflictUnitRow(t, f, 30), [][]sqltype.Value{first})
	if err != nil || !admitted {
		t.Fatal(admitted, err)
	}
	if len(manager.checkpointUnique(scope.ticket, nil)) != 2 {
		t.Fatal("accepted claims were lost")
	}
}
func TestInsertConflictCommittedVisibility(t *testing.T) {
	for _, isolation := range []Isolation{ReadCommitted, RepeatableRead, Serializable} {
		f := insertConflictUniqueFixture(t)
		scope, ref := insertConflictUnitScope(t, f)
		scope.owner.isolation = isolation
		scope.owner.frontier = 0
		admitted, err := scope.admitInsertRow(ref, insertConflictUnitRow(t, f, 10), nil)
		if isolation == ReadCommitted {
			if err != nil || admitted {
				t.Fatal("read committed", admitted, err)
			}
		} else {
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "40001" || admitted {
				t.Fatal("post-frontier tuple", admitted, err)
			}
		}
		if err := scope.insertConflictVisible(rowVersion{born: 10}, false); err != nil {
			t.Fatal("own proposal is visible", err)
		}
		if err := scope.insertConflictVisible(rowVersion{born: 0}, true); err != nil {
			t.Fatal("frontier tuple is visible", err)
		}
	}
}
func TestInsertConflictSelectedFirstAndOrdinaryErrors(t *testing.T) {
	f := insertConflictUniqueFixture(t)
	scope, ref := insertConflictUnitScope(t, f)
	// A real second unique provider uses the bool key. Its name sorts before pi,
	// so checking all providers in ordinary order would incorrectly error first.
	second := *f.index
	second.address.ObjectID++
	second.name = "a_other"
	def := *f.index.sqlIndex
	class, _ := sqltype.DefaultIndexClass(f.parent.columns[1].Usage.Type)
	def.keys = []indexKey{{attribute: 20, class: class}}
	def.predicate = nil
	second.sqlIndex = &def
	f.e.state.descriptors[second.address] = &second
	root, err := buildIndexRoot(context.Background(), f.parent, &def, f.store)
	if err != nil {
		t.Fatal(err)
	}
	f.e.indexRoots[second.address] = root
	scope.uniqueBinding = nil
	scope.insertConflict.catalog = nil
	admitted, err := scope.admitInsertRow(ref, insertConflictUnitRow(t, f, 10), nil)
	if err != nil || admitted {
		t.Fatal("selected provider must win", admitted, err)
	}
	admitted, err = scope.admitInsertRow(ref, insertConflictUnitRow(t, f, 20), nil)
	var diagnostic *sqlstate.Error
	if admitted || !errors.As(err, &diagnostic) || diagnostic.Code != "23505" {
		t.Fatal("unselected violation", admitted, err)
	}
	scope.insertConflict = nil
	err = scope.checkUniqueRow(ref, insertConflictUnitRow(t, f, 10), nil, nil)
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23505" {
		t.Fatal("ordinary route changed", err)
	}
}
func TestInsertConflictInferenceInstalledStates(t *testing.T) {
	f := insertConflictUniqueFixture(t)
	ctx := EvalContext{Context: context.Background(), Scope: 1, Frame: 1}
	explicit := &boundInsertConflict{keys: []conflictKey{{attribute: 10}, {attribute: 10}}, predicate: f.predicate}
	targetless := &boundInsertConflict{}
	f.index.sqlIndex.include = []int32{20}
	if err := explicit.selectArbiters(&ctx, f.parent, f.e.CatalogRead()); err != nil || len(explicit.selected) != 1 {
		t.Fatal("repeated keys / INCLUDE", err)
	}
	f.index.sqlIndex.valid = false
	if err := targetless.selectArbiters(&ctx, f.parent, f.e.CatalogRead()); err != nil || len(targetless.selected) != 1 {
		t.Fatal("ready invalid provider", err)
	}
	if err := explicit.selectArbiters(&ctx, f.parent, f.e.CatalogRead()); err == nil {
		t.Fatal("explicit inferred an invalid provider")
	}
	f.index.sqlIndex.ready = false
	if err := targetless.selectArbiters(&ctx, f.parent, f.e.CatalogRead()); err != nil || len(targetless.selected) != 0 {
		t.Fatal("not-ready provider", err)
	}
}
func TestInsertConflictInferenceMixedExpressionsAndAllProviders(t *testing.T) {
	f := insertConflictUniqueFixture(t)
	expression := logicalExpression{expressionInfo: expressionInfo{usage: f.predicate.usage, properties: ExprProperties{RowDependent: true}}, op: parser.LogicalNot, left: f.predicate}
	class, _ := sqltype.DefaultIndexClass(f.predicate.usage.Type)
	for i := range 2 {
		index := *f.index
		index.address.ObjectID += OID(i + 1)
		index.name = []string{"mixed_a", "mixed_b"}[i]
		definition := *f.index.sqlIndex
		definition.keys = []indexKey{{derived: &derivedIndexKey{definition: expression, execution: expression, attributes: f.parent.attributes}, class: class}, {attribute: 10, class: f.index.sqlIndex.keys[0].class, desc: i == 1}}
		definition.include = []int32{20}
		index.sqlIndex = &definition
		index.columns = []ColumnDescriptor{f.parent.columns[1], f.parent.columns[0]}
		index.attributes = []int32{1, 2}
		index.attributeHighWater = 2
		f.e.state.descriptors[index.address] = &index
		root, err := buildIndexRoot(context.Background(), f.parent, &definition, f.store)
		if err != nil {
			t.Fatal(err)
		}
		f.e.indexRoots[index.address] = root
	}
	action := &boundInsertConflict{keys: []conflictKey{{attribute: 10}, {expression: expression}, {expression: expression}}, predicate: f.predicate}
	ctx := EvalContext{Context: context.Background(), Scope: 1, Frame: 1}
	if err := action.selectArbiters(&ctx, f.parent, f.e.CatalogRead()); err != nil || len(action.selected) != 2 {
		t.Fatal("complete mixed expression set", action.selected, err)
	}
	unrelated := expression
	unrelated.left = logicalExpression{expressionInfo: expression.expressionInfo, op: parser.LogicalNot, left: f.predicate}
	action.keys[1].expression = unrelated
	action.keys[2].expression = unrelated
	if err := action.selectArbiters(&ctx, f.parent, f.e.CatalogRead()); err == nil {
		t.Fatal("expression matching performed unrelated algebra")
	}
}
