package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
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
	f.parent.Owner = owner.Ref
	f.index.Owner = owner.Ref
	definition := *f.index.SQLIndex
	definition.Unique = true
	f.index.SQLIndex = &definition
	f.store.versions = f.store.versions[:1]
	f.store.versions[0].born = 1
	root, err := buildIndexRoot(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, f.parent, &definition, f.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.e.storage.indexRoots[f.index.Address()] = root
	return f
}
func insertConflictUnitScope(t *testing.T, f partialIndexFixture) (*StatementScope, ids.ObjectRef) {
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
	ref := f.e.catalog.Domain.Ref(f.e.catalog.state.Epoch(), f.parent.Address())
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
	value, err := sqltype.Integer(f.parent.Columns[0].Usage.Type, n)
	if err != nil {
		t.Fatal(err)
	}
	return []sqltype.Value{value, sqltype.Boolean(f.parent.Columns[1].Usage.Type, true)}
}
func TestInsertConflictExistingOutputPreservesProposalKey(t *testing.T) {
	for _, test := range []struct {
		name    string
		purpose indexProbePurpose
	}{{"ordinary", indexProbeRuntime}, {"tracked", indexProbeTracked}} {
		t.Run(test.name, func(t *testing.T) {
			purpose := test.purpose
			f := insertConflictUniqueFixture(t)
			scope, ref := insertConflictUnitScope(t, f)
			binding, err := scope.bindUniqueWrite(ref)
			if err != nil {
				t.Fatal(err)
			}
			bound := &binding.keys[0]
			evaluation := EvalContext{Context: scope.op.Context(), Scope: 1, Frame: 1, Input: scope.input, Output: scope.output}
			proposal := insertConflictUnitRow(t, f, 20)
			key, err := bound.load(proposal, &evaluation)
			if err != nil {
				t.Fatal(err)
			}
			own := rowVersion{row: 200, version: 200, values: insertConflictUnitRow(t, f, 10)}
			member, existing, err := scope.existingUniqueOutput(bound, own, &evaluation, purpose)
			if err != nil || !member {
				t.Fatal("existing row lost membership", member, err)
			}
			for _, comparison := range []struct {
				name string
				got  []sqltype.Value
				want sqltype.Value
			}{
				{"proposal", key, proposal[0]},
				{"existing", existing, own.values[0]},
			} {
				cmp, err := bound.ordering.compareKeys(evaluation.Context, logicalIndexEntry{keys: comparison.got}, logicalIndexEntry{keys: []sqltype.Value{comparison.want}})
				if err != nil || cmp != 0 {
					t.Fatal(comparison.name+" key was overwritten", cmp, err)
				}
			}
			if purpose == indexProbeRuntime && scope.existingIndexOutputs != nil {
				t.Fatal("ordinary fast path retained tracked outputs")
			}
		})
	}
}

func TestInsertConflictAdmissionClaimsAndPrefix(t *testing.T) {
	f := insertConflictUniqueFixture(t)
	scope, ref := insertConflictUnitScope(t, f)
	manager := f.e.locks
	first := insertConflictUnitRow(t, f, 20)
	admitted, err := scope.admitInsertRow(ref, &indexWriteCandidate{values: first}, indexWriteBatch{}, nil)
	if err != nil || !admitted {
		t.Fatal(admitted, err)
	}
	baseline := manager.CheckpointUnique(scope.ticket, nil)
	if len(baseline) != 1 {
		t.Fatal("accepted proposal owns one actual key", baseline)
	}
	for _, row := range [][]sqltype.Value{insertConflictUnitRow(t, f, 10), first} {
		admitted, err = scope.admitInsertRow(ref, &indexWriteCandidate{values: row}, indexWriteBatch{values: [][]sqltype.Value{first}}, nil)
		if err != nil || admitted {
			t.Fatal("duplicate was not omitted", admitted, err)
		}
		after := manager.CheckpointUnique(scope.ticket, nil)
		if len(after) != len(baseline) {
			t.Fatal("omission retained a new grant", after)
		}
		for key, mode := range baseline {
			if after[key].Mode != mode.Mode || len(after[key].Multi) != 0 || len(mode.Multi) != 0 {
				t.Fatal("omission lost preexisting grant")
			}
		}
	}
	admitted, err = scope.admitInsertRow(ref, &indexWriteCandidate{values: insertConflictUnitRow(t, f, 30)}, indexWriteBatch{values: [][]sqltype.Value{first}}, nil)
	if err != nil || !admitted {
		t.Fatal(admitted, err)
	}
	if len(manager.CheckpointUnique(scope.ticket, nil)) != 2 {
		t.Fatal("accepted claims were lost")
	}
}
func TestInsertConflictCommittedVisibility(t *testing.T) {
	for _, isolation := range []Isolation{ReadCommitted, RepeatableRead, Serializable} {
		f := insertConflictUniqueFixture(t)
		scope, ref := insertConflictUnitScope(t, f)
		scope.owner.isolation = isolation
		scope.owner.frontier = 0
		admitted, err := scope.admitInsertRow(ref, &indexWriteCandidate{values: insertConflictUnitRow(t, f, 10)}, indexWriteBatch{}, nil)
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
	second.ObjectAddress.ObjectID++
	second.ObjectName = "a_other"
	def := *f.index.SQLIndex
	class, _ := sqltype.DefaultIndexClass(f.parent.Columns[1].Usage.Type)
	def.Keys = []catalog.IndexKey{{Attribute: 20, Class: class}}
	def.Predicate = nil
	second.SQLIndex = &def
	f.e.catalog.state.SetDescriptor(second.Address(), &second)
	root, err := buildIndexRoot(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, f.parent, &def, f.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.e.storage.indexRoots[second.Address()] = root
	scope.uniqueBinding = nil
	scope.insertConflict.catalog = nil
	admitted, err := scope.admitInsertRow(ref, &indexWriteCandidate{values: insertConflictUnitRow(t, f, 10)}, indexWriteBatch{}, nil)
	if err != nil || admitted {
		t.Fatal("selected provider must win", admitted, err)
	}
	admitted, err = scope.admitInsertRow(ref, &indexWriteCandidate{values: insertConflictUnitRow(t, f, 20)}, indexWriteBatch{}, nil)
	var diagnostic *sqlstate.Error
	if admitted || !errors.As(err, &diagnostic) || diagnostic.Code != "23505" {
		t.Fatal("unselected violation", admitted, err)
	}
	scope.insertConflict = nil
	err = scope.checkUniqueRow(ref, &indexWriteCandidate{values: insertConflictUnitRow(t, f, 10)}, nil, indexWriteBatch{})
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23505" {
		t.Fatal("ordinary route changed", err)
	}
}
func TestInsertConflictInferenceInstalledStates(t *testing.T) {
	f := insertConflictUniqueFixture(t)
	ctx := EvalContext{Context: context.Background(), Scope: 1, Frame: 1}
	explicit := &boundInsertConflict{keys: []conflictKey{{attribute: 10}, {attribute: 10}}, predicate: f.predicate}
	targetless := &boundInsertConflict{}
	f.index.SQLIndex.Include = []int32{20}
	if err := explicit.selectArbiters(&ctx, f.parent, f.e.CatalogRead()); err != nil || len(explicit.selected) != 1 {
		t.Fatal("repeated keys / INCLUDE", err)
	}
	f.index.SQLIndex.Valid = false
	if err := targetless.selectArbiters(&ctx, f.parent, f.e.CatalogRead()); err != nil || len(targetless.selected) != 1 {
		t.Fatal("ready invalid provider", err)
	}
	if err := explicit.selectArbiters(&ctx, f.parent, f.e.CatalogRead()); err == nil {
		t.Fatal("explicit inferred an invalid provider")
	}
	f.index.SQLIndex.Ready = false
	if err := targetless.selectArbiters(&ctx, f.parent, f.e.CatalogRead()); err != nil || len(targetless.selected) != 0 {
		t.Fatal("not-ready provider", err)
	}
}
func TestInsertConflictInferenceMixedExpressionsAndAllProviders(t *testing.T) {
	f := insertConflictUniqueFixture(t)
	expression := logicalExpression{Info: expr.Info{Usage: f.predicate.Usage, Property: expr.Properties{RowDependent: true}}, op: parser.LogicalNot, left: f.predicate}
	class, _ := sqltype.DefaultIndexClass(f.predicate.Usage.Type)
	for i := range 2 {
		index := *f.index
		index.ObjectAddress.ObjectID += ids.OID(i + 1)
		index.ObjectName = []string{"mixed_a", "mixed_b"}[i]
		definition := *f.index.SQLIndex
		definition.Keys = []catalog.IndexKey{{Derived: &catalog.DerivedIndexKey{Definition: expression, Execution: expression, Attributes: f.parent.Attributes}, Class: class}, {Attribute: 10, Class: f.index.SQLIndex.Keys[0].Class, Desc: i == 1}}
		definition.Include = []int32{20}
		index.SQLIndex = &definition
		index.Columns = []catalog.Column{f.parent.Columns[1], f.parent.Columns[0]}
		index.Attributes = []int32{1, 2}
		index.ColumnMeta.HighWater = 2
		f.e.catalog.state.SetDescriptor(index.Address(), &index)
		root, err := buildIndexRoot(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, f.parent, &definition, f.store, nil)
		if err != nil {
			t.Fatal(err)
		}
		f.e.storage.indexRoots[index.Address()] = root
	}
	action := &boundInsertConflict{keys: []conflictKey{{attribute: 10}, {expression: expression}, {expression: expression}}, predicate: f.predicate}
	ctx := EvalContext{Context: context.Background(), Scope: 1, Frame: 1}
	if err := action.selectArbiters(&ctx, f.parent, f.e.CatalogRead()); err != nil || len(action.selected) != 2 {
		t.Fatal("complete mixed expression set", action.selected, err)
	}
	unrelated := expression
	unrelated.left = logicalExpression{Info: expression.Info, op: parser.LogicalNot, left: f.predicate}
	action.keys[1].expression = unrelated
	action.keys[2].expression = unrelated
	if err := action.selectArbiters(&ctx, f.parent, f.e.CatalogRead()); err == nil {
		t.Fatal("expression matching performed unrelated algebra")
	}
}
