package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestInsertConflictUpdateIdentity(t *testing.T) {
	for _, isolation := range []session.Isolation{session.ReadCommitted, session.ReadUncommitted, session.RepeatableRead, session.Serializable} {
		e := engine.New()
		s := sessionStart(t, e, nil)
		observer := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TABLE t(k INT UNIQUE,v INT)")
		sessionSQL(t, s, "INSERT INTO t VALUES(9,0),(1,10)")
		sessionSQL(t, s, "DELETE FROM t WHERE k=9")
		op, scope, b := conflictUpdateAnalyzedScope(t, e, s, "INSERT INTO t VALUES(1,2) ON CONFLICT(k) DO UPDATE SET v=excluded.v")
		scope.Owner.Isolation = isolation
		scope.Owner.Frontier = e.Visibility.CommitSeq()
		integer := func(n int64) sqltype.Value {
			v, err := sqltype.Integer(b.Relation.Columns[0].Usage.Type, n)
			if err != nil {
				t.Fatal(err)
			}
			return v
		}
		proposal := []sqltype.Value{integer(1), integer(2)}
		admissionContext := scope.ActivateRuntime(op.Context())
		hit, err := dml.ProbeUniqueRow(admissionContext, scope.WriteHost(), b.Ref, &index.WriteCandidate{Values: proposal}, nil, index.WriteBatch{}, scope.InsertConflict, index.ProbeRuntime)
		entry := e.Storage.IndexRoots[scope.InsertConflict.Selected[0]].Entries[0]
		store := e.Storage.Relations[b.Ref.Address]
		if err != nil || !hit.Committed || entry.Position != 1 || hit.Position != 1 || hit.Row.Row != entry.Row || hit.Row.Version != entry.Version || &hit.Row.Values[0] != &store.Versions[1].Values[0] {
			t.Fatal("provider physical hint after real tombstone, without copying", isolation, hit, entry, err)
		}
		old := hit.Row
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: admissionContext, Input: scope.Input, Output: scope.Output, Host: core.HostOf(scope)}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		if admitted, err := dml.AdmitInsertRow(admissionContext, scope.WriteHost(), b.Ref, &index.WriteCandidate{Values: proposal}, index.WriteBatch{}, &evaluation); admitted || err != nil {
			t.Fatal(admitted, err)
		}
		stage := scope.Stage.Rows[b.Ref.Address]
		replacement, err := dml.ProbeUniqueRow(admissionContext, scope.WriteHost(), b.Ref, &index.WriteCandidate{Values: proposal}, nil, index.WriteBatch{}, scope.InsertConflict, index.ProbeRuntime)
		if err != nil || replacement.Committed || len(stage) != 1 || replacement.Row.Row != old.Row || replacement.Row.Version != stage[0].Version || replacement.Row.Base != old.Version || replacement.Row.Version == old.Version || enginetest.Export(old.Values[1]) != int64(10) {
			t.Fatal("current-command replacement overlay/provenance", replacement, stage, old, err)
		}
		if _, affected := scope.InsertConflict.AffectedRows[replacement.Row.Row]; !affected {
			t.Fatal("command origin missing")
		}
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		op.Done()
		transactionRows(t, observer, "SELECT v FROM t", 2)
		tx := transactionBegin(t, s)
		sessionSQL(t, s, "UPDATE t SET v=3 WHERE k=1")
		prior := s.Transactions.Writes[b.Ref.Address][0]
		op2, scope2, b2 := conflictUpdateAnalyzedScope(t, e, s, "INSERT INTO t VALUES(1,4) ON CONFLICT(k) DO UPDATE SET v=excluded.v")
		nextProposal := []sqltype.Value{integer(1), integer(4)}
		nextContext := scope2.ActivateRuntime(op2.Context())
		own, err := dml.ProbeUniqueRow(nextContext, scope2.WriteHost(), b2.Ref, &index.WriteCandidate{Values: nextProposal}, nil, index.WriteBatch{}, scope2.InsertConflict, index.ProbeRuntime)
		if err != nil || own.Committed || own.Row.Row != prior.Row || own.Row.Version != prior.Version || len(scope2.InsertConflict.AffectedRows) != 0 {
			t.Fatal("prior-own origin not current command", own, prior, err)
		}
		nextEvaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: nextContext, Input: scope2.Input, Output: scope2.Output, Host: core.HostOf(scope2)}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		if admitted, err := dml.AdmitInsertRow(nextContext, scope2.WriteHost(), b2.Ref, &index.WriteCandidate{Values: nextProposal}, index.WriteBatch{}, &nextEvaluation); admitted || err != nil {
			t.Fatal(admitted, err)
		}
		changed := scope2.Stage.Rows[b2.Ref.Address][0]
		if changed.Row != prior.Row || changed.Base != prior.Base || changed.Version == prior.Version || enginetest.Export(prior.Values[1]) != int64(3) {
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
		enginetest.LockEmpty(t, e)
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
			e := engine.New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(k INT UNIQUE,v INT)")
			sessionSQL(t, s, "INSERT INTO t VALUES(1,10)")
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
			transactionRows(t, s, "SELECT v FROM t", 10)
			enginetest.LockEmpty(t, e)
		})
	}
}
func TestInsertConflictUpdateMixedStage(t *testing.T) {
	for _, cancelFinish := range []bool{false, true} {
		e := engine.New()
		s := sessionStart(t, e, nil)
		observer := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TABLE t(k INT UNIQUE,v INT)")
		sessionSQL(t, s, "INSERT INTO t VALUES(1,10),(3,30)")
		ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
		before := append([]storage.RowVersion(nil), e.Storage.Relations[ref.Address].Versions...)
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
		result, err := scope.Execute(enginetest.Statement(t, "INSERT INTO t VALUES(2,20),(1,11),(3,31) ON CONFLICT(k) DO UPDATE SET v=excluded.v WHERE t.k<>3"), 0, nil)
		if err != nil || result.Count != 2 || result.Rows != nil || len(scope.Stage.Rows[ref.Address]) != 2 || !scope.Stage.Replacing || scope.InsertConflict.Affected != 2 {
			scope.Finish(errors.New("bad mixed stage"))
			cancel()
			op.Done()
			t.Fatal(result, err, scope.Stage.Rows)
		}
		stage := scope.Stage.Rows[ref.Address]
		if stage[0].Row == before[0].Row || stage[0].Base != 0 || stage[1].Row != before[0].Row || stage[1].Base != before[0].Version || stage[1].Version == before[0].Version {
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
			if !errors.Is(err, context.Canceled) || s.TransactionState() != session.TxAborted {
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
		if enginetest.Export(before[0].Values[1]) != int64(10) {
			t.Fatal("retained storage mutated")
		}
		enginetest.LockEmpty(t, e)
	}
	for _, level := range []string{"REPEATABLE READ", "SERIALIZABLE"} {
		t.Run("retained_deleted_snapshot_"+level, func(t *testing.T) {
			e := engine.New()
			worker := sessionStart(t, e, nil)
			holder := sessionStart(t, e, nil)
			sessionSQL(t, holder, "CREATE TABLE t(k INT UNIQUE,v INT)")
			sessionSQL(t, holder, "INSERT INTO t VALUES(1,10)")
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			old := e.Storage.Relations[ref.Address].Versions[0]
			tx := transactionBegin(t, worker)
			sessionSQL(t, worker, "SET TRANSACTION ISOLATION LEVEL "+level)
			transactionRows(t, worker, "SELECT v FROM t", 10)
			sessionSQL(t, holder, "DELETE FROM t WHERE k=1")
			result := sessionSQL(t, worker, "INSERT INTO t VALUES(1,11) ON CONFLICT(k) DO UPDATE SET v=excluded.v WHERE FALSE")
			fresh := worker.Transactions.Writes[ref.Address]
			if result.Count != 1 || len(fresh) != 1 || fresh[0].Row == old.Row || fresh[0].Base != 0 {
				t.Fatal("no-hit insertion must not reuse deleted snapshot identity", result, fresh, old)
			}
			transactionRows(t, worker, "SELECT v FROM t ORDER BY v", 10, 11)
			transactionRows(t, holder, "SELECT v FROM t")
			transactionEnd(t, worker, tx, false)
			transactionRows(t, holder, "SELECT v FROM t")
			if enginetest.Export(old.Values[1]) != int64(10) {
				t.Fatal("stable predecessor payload mutated")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
