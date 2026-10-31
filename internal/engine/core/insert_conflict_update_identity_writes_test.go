package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestInsertConflictUpdateIdentity(t *testing.T) {
	for _, isolation := range []session.Isolation{session.ReadCommitted, session.ReadUncommitted, session.RepeatableRead, session.Serializable} {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		observer := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE TABLE t(k INT UNIQUE,v INT)")
		enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(9,0),(1,10)")
		enginetest.SessionSQL(t, s, "DELETE FROM t WHERE k=9")
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
		enginetest.TransactionRows(t, observer, "SELECT v FROM t", 2)
		tx := enginetest.TransactionBegin(t, s)
		enginetest.SessionSQL(t, s, "UPDATE t SET v=3 WHERE k=1")
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
		enginetest.TransactionRows(t, s, "SELECT v FROM t", 4)
		enginetest.TransactionRows(t, observer, "SELECT v FROM t", 2)
		enginetest.TransactionEnd(t, s, tx, false)
		enginetest.TransactionRows(t, observer, "SELECT v FROM t", 2)
		enginetest.LockEmpty(t, e)
	}
}

func TestInsertConflictUpdateMixedStage(t *testing.T) {
	for _, cancelFinish := range []bool{false, true} {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		observer := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE TABLE t(k INT UNIQUE,v INT)")
		enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1,10),(3,30)")
		ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
		before := append([]storage.RowVersion(nil), e.Storage.Relations[ref.Address].Versions...)
		tx := enginetest.TransactionBegin(t, s)
		enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(4,40)")
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
			enginetest.TransactionEnd(t, s, tx, false)
			enginetest.TransactionRows(t, observer, "SELECT v FROM t ORDER BY k", 10, 30)
		} else {
			if err != nil {
				t.Fatal(err)
			}
			enginetest.TransactionRows(t, s, "SELECT v FROM t ORDER BY k", 11, 20, 30, 40)
			enginetest.TransactionRows(t, observer, "SELECT v FROM t ORDER BY k", 10, 30)
			enginetest.TransactionEnd(t, s, tx, true)
			enginetest.TransactionRows(t, observer, "SELECT v FROM t ORDER BY k", 11, 20, 30, 40)
		}
		if enginetest.Export(before[0].Values[1]) != int64(10) {
			t.Fatal("retained storage mutated")
		}
		enginetest.LockEmpty(t, e)
	}
	for _, level := range []string{"REPEATABLE READ", "SERIALIZABLE"} {
		t.Run("retained_deleted_snapshot_"+level, func(t *testing.T) {
			e := engine.New()
			worker := enginetest.StartSession(t, e, nil)
			holder := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, holder, "CREATE TABLE t(k INT UNIQUE,v INT)")
			enginetest.SessionSQL(t, holder, "INSERT INTO t VALUES(1,10)")
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			old := e.Storage.Relations[ref.Address].Versions[0]
			tx := enginetest.TransactionBegin(t, worker)
			enginetest.SessionSQL(t, worker, "SET TRANSACTION ISOLATION LEVEL "+level)
			enginetest.TransactionRows(t, worker, "SELECT v FROM t", 10)
			enginetest.SessionSQL(t, holder, "DELETE FROM t WHERE k=1")
			result := enginetest.SessionSQL(t, worker, "INSERT INTO t VALUES(1,11) ON CONFLICT(k) DO UPDATE SET v=excluded.v WHERE FALSE")
			fresh := worker.Transactions.Writes[ref.Address]
			if result.Count != 1 || len(fresh) != 1 || fresh[0].Row == old.Row || fresh[0].Base != 0 {
				t.Fatal("no-hit insertion must not reuse deleted snapshot identity", result, fresh, old)
			}
			enginetest.TransactionRows(t, worker, "SELECT v FROM t ORDER BY v", 10, 11)
			enginetest.TransactionRows(t, holder, "SELECT v FROM t")
			enginetest.TransactionEnd(t, worker, tx, false)
			enginetest.TransactionRows(t, holder, "SELECT v FROM t")
			if enginetest.Export(old.Values[1]) != int64(10) {
				t.Fatal("stable predecessor payload mutated")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
