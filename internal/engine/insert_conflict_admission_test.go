package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
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
	root, err := index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.parent, &definition, f.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.e.Mu.Lock()
	f.e.Storage.InstallIndexRootLocked(f.index.Address(), root)
	f.e.Mu.Unlock()
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
		if !scope.Finished {
			_ = scope.Finish(errors.New("unit rollback"))
		}
	})
	if err := scope.Gate(&parser.Insert{}); err != nil {
		t.Fatal(err)
	}
	ref := f.e.Catalog.Domain.Ref(f.e.Catalog.State.Epoch(), f.parent.Address())
	action := dml.NewInsertConflictState(&analyze.InsertConflict{Keys: []analyze.ConflictKey{{Attribute: 10}}, Predicate: f.predicate, Reads: []int{0, 1}, Targets: []int{0, 1}})
	ctx := exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: op.Context(), Input: scope.Input, Output: scope.Output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
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
