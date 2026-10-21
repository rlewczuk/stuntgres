package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
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
	f.store.Versions = f.store.Versions[:1]
	f.store.Versions[0].Born = 1
	root, err := index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.parent, &definition, f.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.e.mu.Lock()
	f.e.storage.InstallIndexRootLocked(f.index.Address(), root)
	f.e.mu.Unlock()
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
	action := dml.NewInsertConflictState(&analyze.InsertConflict{Keys: []analyze.ConflictKey{{Attribute: 10}}, Predicate: f.predicate, Reads: []int{0, 1}, Targets: []int{0, 1}})
	ctx := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Input: scope.input, Output: scope.output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	if err := action.SelectArbiters(&ctx, f.parent, f.e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	scope.InsertConflict = action
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
		purpose index.ProbePurpose
	}{{"ordinary", index.ProbeRuntime}, {"tracked", index.ProbeTracked}} {
		t.Run(test.name, func(t *testing.T) {
			purpose := test.purpose
			f := insertConflictUniqueFixture(t)
			scope, ref := insertConflictUnitScope(t, f)
			binding, err := scope.bindUniqueWrite(ref)
			if err != nil {
				t.Fatal(err)
			}
			bound := &binding.Keys[0]
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.op.Context(), Input: scope.input, Output: scope.output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
			proposal := insertConflictUnitRow(t, f, 20)
			key, err := bound.Load(proposal, &evaluation)
			if err != nil {
				t.Fatal(err)
			}
			own := storage.RowVersion{Row: 200, Version: 200, Values: insertConflictUnitRow(t, f, 10)}
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
				{"existing", existing, own.Values[0]},
			} {
				cmp, err := bound.Ordering.CompareKeys(evaluation.Context, storage.IndexEntry{Keys: comparison.got}, storage.IndexEntry{Keys: []sqltype.Value{comparison.want}})
				if err != nil || cmp != 0 {
					t.Fatal(comparison.name+" key was overwritten", cmp, err)
				}
			}
			if purpose == index.ProbeRuntime && scope.indexesOrZero().ExistingOutputs != nil {
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
	admitted, err := scope.admitInsertRow(ref, &index.WriteCandidate{Values: first}, index.WriteBatch{}, nil)
	if err != nil || !admitted {
		t.Fatal(admitted, err)
	}
	baseline := manager.CheckpointUnique(scope.ticket, nil)
	if len(baseline) != 1 {
		t.Fatal("accepted proposal owns one actual key", baseline)
	}
	for _, row := range [][]sqltype.Value{insertConflictUnitRow(t, f, 10), first} {
		admitted, err = scope.admitInsertRow(ref, &index.WriteCandidate{Values: row}, index.WriteBatch{Values: [][]sqltype.Value{first}}, nil)
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
	admitted, err = scope.admitInsertRow(ref, &index.WriteCandidate{Values: insertConflictUnitRow(t, f, 30)}, index.WriteBatch{Values: [][]sqltype.Value{first}}, nil)
	if err != nil || !admitted {
		t.Fatal(admitted, err)
	}
	if len(manager.CheckpointUnique(scope.ticket, nil)) != 2 {
		t.Fatal("accepted claims were lost")
	}
}
func TestInsertConflictCommittedVisibility(t *testing.T) {
	for _, isolation := range []session.Isolation{session.ReadCommitted, session.RepeatableRead, session.Serializable} {
		f := insertConflictUniqueFixture(t)
		scope, ref := insertConflictUnitScope(t, f)
		scope.owner.Isolation = isolation
		scope.owner.Frontier = 0
		admitted, err := scope.admitInsertRow(ref, &index.WriteCandidate{Values: insertConflictUnitRow(t, f, 10)}, index.WriteBatch{}, nil)
		if isolation == session.ReadCommitted {
			if err != nil || admitted {
				t.Fatal("read committed", admitted, err)
			}
		} else {
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "40001" || admitted {
				t.Fatal("post-frontier tuple", admitted, err)
			}
		}
		if err := scope.insertConflictVisible(storage.RowVersion{Born: 10}, false); err != nil {
			t.Fatal("own proposal is visible", err)
		}
		if err := scope.insertConflictVisible(storage.RowVersion{Born: 0}, true); err != nil {
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
	root, err := index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.parent, &def, f.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.e.mu.Lock()
	f.e.storage.InstallIndexRootLocked(second.Address(), root)
	f.e.mu.Unlock()
	scope.UniqueBinding = nil
	scope.InsertConflict.Catalog = nil
	admitted, err := scope.admitInsertRow(ref, &index.WriteCandidate{Values: insertConflictUnitRow(t, f, 10)}, index.WriteBatch{}, nil)
	if err != nil || admitted {
		t.Fatal("selected provider must win", admitted, err)
	}
	admitted, err = scope.admitInsertRow(ref, &index.WriteCandidate{Values: insertConflictUnitRow(t, f, 20)}, index.WriteBatch{}, nil)
	var diagnostic *sqlstate.Error
	if admitted || !errors.As(err, &diagnostic) || diagnostic.Code != "23505" {
		t.Fatal("unselected violation", admitted, err)
	}
	scope.InsertConflict = nil
	err = scope.checkUniqueRow(ref, &index.WriteCandidate{Values: insertConflictUnitRow(t, f, 10)}, nil, index.WriteBatch{})
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23505" {
		t.Fatal("ordinary route changed", err)
	}
}
func TestInsertConflictInferenceInstalledStates(t *testing.T) {
	f := insertConflictUniqueFixture(t)
	ctx := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	explicit := dml.NewInsertConflictState(&analyze.InsertConflict{Keys: []analyze.ConflictKey{{Attribute: 10}, {Attribute: 10}}, Predicate: f.predicate})
	targetless := dml.NewInsertConflictState(&analyze.InsertConflict{})
	f.index.SQLIndex.Include = []int32{20}
	if err := explicit.SelectArbiters(&ctx, f.parent, f.e.CatalogRead()); err != nil || len(explicit.Selected) != 1 {
		t.Fatal("repeated keys / INCLUDE", err)
	}
	f.index.SQLIndex.Valid = false
	if err := targetless.SelectArbiters(&ctx, f.parent, f.e.CatalogRead()); err != nil || len(targetless.Selected) != 1 {
		t.Fatal("ready invalid provider", err)
	}
	if err := explicit.SelectArbiters(&ctx, f.parent, f.e.CatalogRead()); err == nil {
		t.Fatal("explicit inferred an invalid provider")
	}
	f.index.SQLIndex.Ready = false
	if err := targetless.SelectArbiters(&ctx, f.parent, f.e.CatalogRead()); err != nil || len(targetless.Selected) != 0 {
		t.Fatal("not-ready provider", err)
	}
}
func TestInsertConflictInferenceMixedExpressionsAndAllProviders(t *testing.T) {
	f := insertConflictUniqueFixture(t)
	expression := plan.LogicalExpr{Info: expr.Info{Usage: f.predicate.Usage, Property: expr.Properties{RowDependent: true}}, Op: parser.LogicalNot, Left: f.predicate}
	class, _ := sqltype.DefaultIndexClass(f.predicate.Usage.Type)
	for i := range 2 {
		idx := *f.index
		idx.ObjectAddress.ObjectID += ids.OID(i + 1)
		idx.ObjectName = []string{"mixed_a", "mixed_b"}[i]
		definition := *f.index.SQLIndex
		definition.Keys = []catalog.IndexKey{{Derived: &catalog.DerivedIndexKey{Definition: expression, Execution: expression, Attributes: f.parent.Attributes}, Class: class}, {Attribute: 10, Class: f.index.SQLIndex.Keys[0].Class, Desc: i == 1}}
		definition.Include = []int32{20}
		idx.SQLIndex = &definition
		idx.Columns = []catalog.Column{f.parent.Columns[1], f.parent.Columns[0]}
		idx.Attributes = []int32{1, 2}
		idx.ColumnMeta.HighWater = 2
		f.e.catalog.state.SetDescriptor(idx.Address(), &idx)
		root, err := index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.parent, &definition, f.store, nil)
		if err != nil {
			t.Fatal(err)
		}
		f.e.mu.Lock()
		f.e.storage.InstallIndexRootLocked(idx.Address(), root)
		f.e.mu.Unlock()
	}
	action := dml.NewInsertConflictState(&analyze.InsertConflict{Keys: []analyze.ConflictKey{{Attribute: 10}, {Expression: expression}, {Expression: expression}}, Predicate: f.predicate})
	ctx := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	if err := action.SelectArbiters(&ctx, f.parent, f.e.CatalogRead()); err != nil || len(action.Selected) != 2 {
		t.Fatal("complete mixed expression set", action.Selected, err)
	}
	unrelated := expression
	unrelated.Left = plan.LogicalExpr{Info: expression.Info, Op: parser.LogicalNot, Left: f.predicate}
	action.Keys[1].Expression = unrelated
	action.Keys[2].Expression = unrelated
	if err := action.SelectArbiters(&ctx, f.parent, f.e.CatalogRead()); err == nil {
		t.Fatal("expression matching performed unrelated algebra")
	}
}
