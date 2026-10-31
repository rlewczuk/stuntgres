package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestInsertConflictUpdateTargetFence(t *testing.T) {
	for _, mode := range []struct {
		name, query string
		storage     bool
	}{
		{"update", "INSERT INTO t VALUES(1,11) ON CONFLICT(k) DO UPDATE SET v=excluded.v", true},
		{"nothing", "INSERT INTO t VALUES(2,20) ON CONFLICT(k) DO NOTHING", false},
		{"insert", "INSERT INTO t VALUES(2,20)", false},
	} {
		t.Run(mode.name, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE t(k INT UNIQUE,v INT)")
			enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1,10)")
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			op, err := s.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				op.Done()
				t.Fatal(err)
			}
			rollback := errors.New("target fence proof rollback")
			t.Cleanup(func() {
				scope.Finish(rollback)
				op.Done()
			})
			result, err := scope.Execute(enginetest.Statement(t, mode.query), 0, nil)
			if err != nil || result.Count != 1 || result.Rows != nil {
				t.Fatal(result, err)
			}
			if scope.LockedRef != ref || core.MutationTargetLockedIdentityOnly(&scope.StatementMutation.MutationTarget) ||
				(scope.LockedStorage != nil) != mode.storage ||
				(mode.storage && scope.LockedStorage != e.Storage.Relations[ref.Address]) {
				t.Fatal("exact target fence", scope.LockedRef, scope.LockedStorage, mode.storage)
			}
			check := func(name string, mutate func(), want error) {
				t.Helper()
				e.Mu.Lock()
				catalog, storage := e.Catalog.State, e.Storage.Relations[ref.Address]
				defer func() {
					e.Catalog.PublishLocked(catalog)
					e.Storage.InstallRelationLocked(ref.Address, storage)
					e.Mu.Unlock()
				}()
				mutate()
				if err := scope.ValidateLockedRelation(e); !errors.Is(err, want) {
					t.Fatalf("%s: got %v, want %v", name, err, want)
				}
			}
			replacement := func(shapeChange bool) {
				catalog := catalog.CloneState(e.Catalog.State)
				relation := *catalog.MustRelation(ref.Address)
				if shapeChange {
					relation.Columns = relation.Columns[:1]
					relation.Attributes = relation.Attributes[:1]
				}
				catalog.SetDescriptor(ref.Address, &relation)
				e.Catalog.PublishLocked(catalog)
			}
			// AuthorizationAfterWait supplies the real SQL REVOKE witness.
			// Here distinguish its shape-preserving descriptor replacement
			// from genuine physical, row-shape and catalog-epoch replacement.
			var descriptorError error
			if !mode.storage {
				descriptorError = ids.ErrCatalogReplaced
			}
			check("descriptor replacement", func() { replacement(false) }, descriptorError)
			check("row shape replacement", func() { replacement(true) }, ids.ErrCatalogReplaced)
			if mode.storage {
				check("storage replacement", func() {
					storage := *e.Storage.Relations[ref.Address]
					e.Storage.InstallRelationLocked(ref.Address, &storage)
				}, ids.ErrCatalogReplaced)
			}
			check("catalog epoch replacement", func() {
				catalog := catalog.CloneState(e.Catalog.State)
				catalog.SetEpoch(catalog.Epoch() + 1)
				e.Catalog.PublishLocked(catalog)
			}, ids.ErrCatalogReplaced)
			if err := scope.Finish(rollback); err != rollback {
				t.Fatal(err)
			}
			op.Done()
			enginetest.TransactionRows(t, s, "SELECT v FROM t", 10)
			enginetest.LockEmpty(t, e)
		})
	}
}
