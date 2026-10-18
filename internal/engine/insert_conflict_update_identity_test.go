package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestInsertConflictUpdateIdentity(t *testing.T) {
	for _, isolation := range []Isolation{ReadCommitted, ReadUncommitted, RepeatableRead, Serializable} {
		e := New()
		s := sessionStart(t, e, nil)
		observer := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TABLE t(k INT UNIQUE,v INT)")
		sessionSQL(t, s, "INSERT INTO t VALUES(9,0),(1,10)")
		sessionSQL(t, s, "DELETE FROM t WHERE k=9")
		op, scope, b := conflictUpdateAnalyzedScope(t, e, s, "INSERT INTO t VALUES(1,2) ON CONFLICT(k) DO UPDATE SET v=excluded.v")
		scope.owner.isolation = isolation
		scope.owner.frontier = e.commitSeq
		integer := func(n int64) sqltype.Value {
			v, err := sqltype.Integer(b.relation.Columns[0].Usage.Type, n)
			if err != nil {
				t.Fatal(err)
			}
			return v
		}
		proposal := []sqltype.Value{integer(1), integer(2)}
		hit, err := scope.probeUniqueRow(b.ref, &indexWriteCandidate{values: proposal}, nil, indexWriteBatch{}, b.conflict, indexProbeRuntime)
		entry := e.indexRoots[b.conflict.selected[0]].entries[0]
		store := e.storage[b.ref.Address]
		if err != nil || !hit.committed || entry.position != 1 || hit.position != 1 || hit.row.row != entry.row || hit.row.version != entry.version || &hit.row.values[0] != &store.versions[1].values[0] {
			t.Fatal("provider physical hint after real tombstone, without copying", isolation, hit, entry, err)
		}
		old := hit.row
		evaluation := EvalContext{Context: op.Context(), Scope: 1, Frame: 1, Input: scope.input, Output: scope.output, statementScope: scope}
		if admitted, err := scope.admitInsertRow(b.ref, &indexWriteCandidate{values: proposal}, indexWriteBatch{}, &evaluation); admitted || err != nil {
			t.Fatal(admitted, err)
		}
		stage := scope.stage.rows[b.ref.Address]
		replacement, err := scope.probeUniqueRow(b.ref, &indexWriteCandidate{values: proposal}, nil, indexWriteBatch{}, b.conflict, indexProbeRuntime)
		if err != nil || replacement.committed || len(stage) != 1 || replacement.row.row != old.row || replacement.row.version != stage[0].version || replacement.row.base != old.version || replacement.row.version == old.version || testExport(old.values[1]) != int64(10) {
			t.Fatal("current-command replacement overlay/provenance", replacement, stage, old, err)
		}
		if _, affected := b.conflict.affectedRows[replacement.row.row]; !affected {
			t.Fatal("command origin missing")
		}
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		op.Done()
		transactionRows(t, observer, "SELECT v FROM t", 2)
		tx := transactionBegin(t, s)
		sessionSQL(t, s, "UPDATE t SET v=3 WHERE k=1")
		prior := s.transactions.writes[b.ref.Address][0]
		op2, scope2, b2 := conflictUpdateAnalyzedScope(t, e, s, "INSERT INTO t VALUES(1,4) ON CONFLICT(k) DO UPDATE SET v=excluded.v")
		nextProposal := []sqltype.Value{integer(1), integer(4)}
		own, err := scope2.probeUniqueRow(b2.ref, &indexWriteCandidate{values: nextProposal}, nil, indexWriteBatch{}, b2.conflict, indexProbeRuntime)
		if err != nil || own.committed || own.row.row != prior.row || own.row.version != prior.version || len(b2.conflict.affectedRows) != 0 {
			t.Fatal("prior-own origin not current command", own, prior, err)
		}
		nextEvaluation := EvalContext{Context: op2.Context(), Scope: 1, Frame: 1, Input: scope2.input, Output: scope2.output, statementScope: scope2}
		if admitted, err := scope2.admitInsertRow(b2.ref, &indexWriteCandidate{values: nextProposal}, indexWriteBatch{}, &nextEvaluation); admitted || err != nil {
			t.Fatal(admitted, err)
		}
		changed := scope2.stage.rows[b2.ref.Address][0]
		if changed.row != prior.row || changed.base != prior.base || changed.version == prior.version || testExport(prior.values[1]) != int64(3) {
			t.Fatal("original committed base through private predecessor", changed, prior)
		}
		if err := scope2.Finish(nil); err != nil {
			t.Fatal(err)
		}
		op2.Done()
		transactionRows(t, s, "SELECT v FROM t", 4)
		transactionRows(t, observer, "SELECT v FROM t", 2)
		transactionEnd(t, s, tx, false)
		transactionRows(t, observer, "SELECT v FROM t", 2)
		lockEmpty(t, e)
	}
}

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
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(k INT UNIQUE,v INT)")
			sessionSQL(t, s, "INSERT INTO t VALUES(1,10)")
			ref := catalogRef(t, e, e.CatalogRead(), "t")
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
			result, err := scope.Execute(statement(t, mode.query), 0, nil)
			if err != nil || result.Count != 1 || result.Rows != nil {
				t.Fatal(result, err)
			}
			if scope.lockedRef != ref || scope.lockedIdentityOnly ||
				(scope.lockedStorage != nil) != mode.storage ||
				(mode.storage && scope.lockedStorage != e.storage[ref.Address]) {
				t.Fatal("exact target fence", scope.lockedRef, scope.lockedStorage, mode.storage)
			}
			check := func(name string, mutate func(), want error) {
				t.Helper()
				e.mu.Lock()
				catalog, storage := e.catalog.state, e.storage[ref.Address]
				defer func() {
					e.catalog.state = catalog
					e.storage[ref.Address] = storage
					e.mu.Unlock()
				}()
				mutate()
				if err := scope.validateLockedRelation(e); !errors.Is(err, want) {
					t.Fatalf("%s: got %v, want %v", name, err, want)
				}
			}
			replacement := func(shapeChange bool) {
				catalog := cloneCatalog(e.catalog.state)
				relation := *catalog.MustRelation(ref.Address)
				if shapeChange {
					relation.Columns = relation.Columns[:1]
					relation.Attributes = relation.Attributes[:1]
				}
				catalog.SetDescriptor(ref.Address, &relation)
				e.catalog.state = catalog
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
					storage := *e.storage[ref.Address]
					e.storage[ref.Address] = &storage
				}, ids.ErrCatalogReplaced)
			}
			check("catalog epoch replacement", func() {
				catalog := cloneCatalog(e.catalog.state)
				catalog.SetEpoch(catalog.Epoch() + 1)
				e.catalog.state = catalog
			}, ids.ErrCatalogReplaced)
			if err := scope.Finish(rollback); err != rollback {
				t.Fatal(err)
			}
			op.Done()
			transactionRows(t, s, "SELECT v FROM t", 10)
			lockEmpty(t, e)
		})
	}
}
func TestInsertConflictUpdateMixedStage(t *testing.T) {
	for _, cancelFinish := range []bool{false, true} {
		e := New()
		s := sessionStart(t, e, nil)
		observer := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TABLE t(k INT UNIQUE,v INT)")
		sessionSQL(t, s, "INSERT INTO t VALUES(1,10),(3,30)")
		ref := catalogRef(t, e, e.CatalogRead(), "t")
		before := append([]rowVersion(nil), e.storage[ref.Address].versions...)
		tx := transactionBegin(t, s)
		sessionSQL(t, s, "INSERT INTO t VALUES(4,40)")
		ctx, cancel := context.WithCancel(context.Background())
		op, err := s.Acquire(ctx)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		scope, err := e.StartStatement(op)
		if err != nil {
			cancel()
			op.Done()
			t.Fatal(err)
		}
		result, err := scope.Execute(statement(t, "INSERT INTO t VALUES(2,20),(1,11),(3,31) ON CONFLICT(k) DO UPDATE SET v=excluded.v WHERE t.k<>3"), 0, nil)
		if err != nil || result.Count != 2 || result.Rows != nil || len(scope.stage.rows[ref.Address]) != 2 || !scope.stage.replacing || scope.insertConflict.affected != 2 {
			scope.Finish(errors.New("bad mixed stage"))
			cancel()
			op.Done()
			t.Fatal(result, err, scope.stage.rows)
		}
		stage := scope.stage.rows[ref.Address]
		if stage[0].row == before[0].row || stage[0].base != 0 || stage[1].row != before[0].row || stage[1].base != before[0].version || stage[1].version == before[0].version {
			scope.Finish(errors.New("bad identity"))
			cancel()
			op.Done()
			t.Fatal("mixed insertion/replacement provenance", stage, before)
		}
		if cancelFinish {
			cancel()
		}
		err = scope.Finish(nil)
		op.Done()
		cancel()
		if cancelFinish {
			if !errors.Is(err, context.Canceled) || s.TransactionState() != TxAborted {
				t.Fatal("Finish cancellation veto", err, s.TransactionState())
			}
			transactionEnd(t, s, tx, false)
			transactionRows(t, observer, "SELECT v FROM t ORDER BY k", 10, 30)
		} else {
			if err != nil {
				t.Fatal(err)
			}
			transactionRows(t, s, "SELECT v FROM t ORDER BY k", 11, 20, 30, 40)
			transactionRows(t, observer, "SELECT v FROM t ORDER BY k", 10, 30)
			transactionEnd(t, s, tx, true)
			transactionRows(t, observer, "SELECT v FROM t ORDER BY k", 11, 20, 30, 40)
		}
		if testExport(before[0].values[1]) != int64(10) {
			t.Fatal("retained storage mutated")
		}
		lockEmpty(t, e)
	}
	for _, level := range []string{"REPEATABLE READ", "SERIALIZABLE"} {
		t.Run("retained_deleted_snapshot_"+level, func(t *testing.T) {
			e := New()
			worker := sessionStart(t, e, nil)
			holder := sessionStart(t, e, nil)
			sessionSQL(t, holder, "CREATE TABLE t(k INT UNIQUE,v INT)")
			sessionSQL(t, holder, "INSERT INTO t VALUES(1,10)")
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			old := e.storage[ref.Address].versions[0]
			tx := transactionBegin(t, worker)
			sessionSQL(t, worker, "SET TRANSACTION ISOLATION LEVEL "+level)
			transactionRows(t, worker, "SELECT v FROM t", 10)
			sessionSQL(t, holder, "DELETE FROM t WHERE k=1")
			result := sessionSQL(t, worker, "INSERT INTO t VALUES(1,11) ON CONFLICT(k) DO UPDATE SET v=excluded.v WHERE FALSE")
			fresh := worker.transactions.writes[ref.Address]
			if result.Count != 1 || len(fresh) != 1 || fresh[0].row == old.row || fresh[0].base != 0 {
				t.Fatal("no-hit insertion must not reuse deleted snapshot identity", result, fresh, old)
			}
			transactionRows(t, worker, "SELECT v FROM t ORDER BY v", 10, 11)
			transactionRows(t, holder, "SELECT v FROM t")
			transactionEnd(t, worker, tx, false)
			transactionRows(t, holder, "SELECT v FROM t")
			if testExport(old.values[1]) != int64(10) {
				t.Fatal("stable predecessor payload mutated")
			}
			lockEmpty(t, e)
		})
	}
}
