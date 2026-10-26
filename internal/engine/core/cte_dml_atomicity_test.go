package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func cteDMLUnitScope(t *testing.T, session *session.Session, ctx context.Context) (*core.StatementScope, *session.Operation) {
	t.Helper()
	op, err := session.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := core.EngineOf(session).StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !scope.Finished {
			scope.Finish(errors.New("unit cleanup"))
		}
		op.Done()
	})
	return scope, op
}

func TestCTEDMLCancellationBeforeFinish(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "implicit", true: "explicit"}[explicit], func(t *testing.T) {
			e := engine.New()
			worker, observer := sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE a(id INT)", "CREATE TABLE b(id INT)", "INSERT INTO a VALUES(1),(2)", "INSERT INTO b VALUES(9)"} {
				sessionSQL(t, worker, q)
			}
			a, b := enginetest.CatalogRef(t, e, e.CatalogRead(), "a"), enginetest.CatalogRef(t, e, e.CatalogRead(), "b")
			var tx ids.TxRef
			if explicit {
				tx = transactionBegin(t, worker)
				sessionSQL(t, worker, "INSERT INTO b VALUES(7)")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			scope, op := cteDMLUnitScope(t, worker, ctx)
			result, err := scope.Execute(enginetest.Statement(t, "WITH moved AS (DELETE FROM a RETURNING id), copied AS (INSERT INTO b SELECT id FROM moved RETURNING id) SELECT * FROM copied"), 0, nil)
			if err != nil || result.Count != 2 || result.Rows == nil {
				t.Fatal(result, err)
			}
			defer result.Rows.Close()
			if scope.Completed || len(scope.Stage.Rows[b.Address]) != 2 || scope.Stage.Deletes[a.Address] == nil || len(scope.Stage.Deletes[a.Address].Rows) != 2 || core.ScopeCTEInvocation(scope) != nil {
				t.Fatal("not one unpublished complete stage")
			}
			view := scope.View.Load()
			if view == nil || len(view.Committed) < 2 || &view.Committed[a.Address][0].Values[0] != &e.Storage.Relations[a.Address].Versions[0].Values[0] || &view.Committed[b.Address][0].Values[0] != &e.Storage.Relations[b.Address].Versions[0].Values[0] {
				t.Fatal("snapshot did not retain both original roots")
			}
			transactionRows(t, observer, "SELECT id FROM a ORDER BY id", 1, 2)
			transactionRows(t, observer, "SELECT id FROM b ORDER BY id", 9)
			cancel()
			if err := scope.Finish(nil); err != context.Canceled {
				t.Fatal(err)
			}
			result.Rows.Close()
			op.Done()
			if scope.View != nil || scope.LockTicket != nil || scope.Stage.Rows != nil || scope.Stage.Deletes != nil || core.ScopeMutations(scope) != nil || core.ScopeCTEInvocation(scope) != nil {
				t.Fatal("cancel retained statement roots")
			}
			transactionRows(t, observer, "SELECT id FROM a ORDER BY id", 1, 2)
			transactionRows(t, observer, "SELECT id FROM b ORDER BY id", 9)
			want := session.TxIdle
			if explicit {
				want = session.TxAborted
			}
			if worker.TransactionState() != want {
				t.Fatal(worker.TransactionState(), want)
			}
			if explicit {
				transactionEnd(t, worker, tx, false)
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestCTEDMLAtomicPublication(t *testing.T) {
	e := engine.New()
	worker, observer := sessionStart(t, e, nil), sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE a(id INT)", "CREATE TABLE b(id INT)", "INSERT INTO a VALUES(1),(2)"} {
		sessionSQL(t, worker, q)
	}
	scope, op := cteDMLUnitScope(t, worker, context.Background())
	result, err := scope.Execute(enginetest.Statement(t, "WITH d AS (DELETE FROM a RETURNING id), i AS (INSERT INTO b SELECT id FROM d RETURNING id) SELECT * FROM i"), 0, nil)
	if err != nil || result.Rows == nil {
		t.Fatal(result, err)
	}
	defer result.Rows.Close()
	before := e.Visibility.CommitSeq()
	transactionRows(t, observer, "SELECT id FROM a ORDER BY id", 1, 2)
	transactionRows(t, observer, "SELECT id FROM b")
	if scope.Completed {
		t.Fatal("auxiliary published before Finish")
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	op.Done()
	if e.Visibility.CommitSeq() != before+1 {
		t.Fatal("more than one publication frontier", before, e.Visibility.CommitSeq())
	}
	transactionRows(t, observer, "SELECT id FROM a")
	transactionRows(t, observer, "SELECT id FROM b ORDER BY id", 1, 2)
	if scope.View != nil || scope.LockTicket != nil || core.ScopeMutations(scope) != nil {
		t.Fatal("successful Finish retained owner")
	}
	enginetest.LockEmpty(t, e)
}

func TestCTEDMLLateFailureIdentity(t *testing.T) {
	for _, phase := range []string{"output_error", "output_cancel", "close_veto", "close_veto_exec", "primary_error_before_close"} {
		t.Run(phase, func(t *testing.T) {
			e := engine.New()
			worker, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE a(id INT)", "CREATE TABLE b(id INT)", "INSERT INTO b VALUES(1)"} {
				sessionSQL(t, worker, q)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			scope, op := cteDMLUnitScope(t, worker, ctx)
			sql := "WITH first AS (INSERT INTO a VALUES(1) RETURNING id), second AS (UPDATE b SET id=b.id+1 FROM first WHERE b.id=first.id RETURNING b.id) "
			if phase == "close_veto_exec" {
				sql += "INSERT INTO a VALUES(2)"
			} else {
				sql += "SELECT * FROM second"
			}
			ast := enginetest.Statement(t, sql)
			if _, err := scope.Describe(ast); err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			auth, err := e.StatementAuthorizationLocked(op, ast)
			if err == nil {
				err = scope.CaptureIdentityLocked(auth)
			}
			var bound analyze.Binding
			if err == nil {
				bound, err = core.Describe(ctx, e.AnalysisHost(op), ast, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
			}
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			a := enginetest.CatalogRef(t, e, e.CatalogRead(), "a")
			integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
			primary := errors.New("later actual RETURNING failure")
			closeFailure := errors.New("actual producer Close veto")
			calls, closes := 0, 0
			probe := queryProbe(integer.Usage(), func(c *exec.Context) (sqltype.Value, error) {
				calls++
				if len(scope.Stage.Rows[a.Address]) != 1 {
					t.Fatal("later output did not follow accepted earlier private write")
				}
				if phase == "close_veto" || phase == "close_veto_exec" || phase == "primary_error_before_close" {
					producer := &exec.CTEProducer{Opened: true, Inner: exec.Stage{Finish: func() error { closes++; return closeFailure }}}
					core.ScopeCTEInvocation(scope).Order = append(core.ScopeCTEInvocation(scope).Order, producer)
				}
				if phase == "output_cancel" {
					cancel()
					return sqltype.Value{}, context.Canceled
				}
				if phase != "close_veto" && phase != "close_veto_exec" {
					return sqltype.Value{}, primary
				}
				return c.Row[0], nil
			})
			cteMutationOf(bound.CTEs[1]).Returning.Expressions = []expr.Expr{probe}
			folds := exec.FoldCaptureCandidate{}
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output, FoldCaptures: &folds}}
			e.Mu.Lock()
			result, err := core.EngineExecuteCTECommandsLocked(e, scope, ast, bound, auth, nil, evaluation, nil, nil)
			want := primary
			if phase == "output_cancel" {
				want = context.Canceled
			}
			if phase == "close_veto" || phase == "close_veto_exec" {
				want = closeFailure
			}
			if err != want || result.Rows != nil || result.Count != 0 || calls != 1 {
				t.Fatal(result, err, want, calls)
			}
			if (phase == "close_veto" || phase == "close_veto_exec" || phase == "primary_error_before_close") && closes != 1 {
				t.Fatal("producer did not close exactly once", closes)
			}
			if err := scope.Finish(err); err != want {
				t.Fatal(err, want)
			}
			op.Done()
			transactionRows(t, peer, "SELECT id FROM a")
			transactionRows(t, peer, "SELECT id FROM b", 1)
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestCTEDMLCTASPublication(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "cancel_before_finish"}[canceled], func(t *testing.T) {
			e := engine.New()
			worker, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, worker, "CREATE TABLE a(id INT)")
			sessionSQL(t, worker, "INSERT INTO a VALUES(1),(2)")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			scope, op := cteDMLUnitScope(t, worker, ctx)
			result, err := scope.Execute(enginetest.Statement(t, "CREATE TABLE copied AS WITH d AS (DELETE FROM a RETURNING id) SELECT * FROM d"), 0, nil)
			if err != nil || result.Count != 2 || result.Rows != nil || scope.Completed || scope.Catalog == nil {
				t.Fatal("creation did not join the private statement stage", result, err)
			}
			var created ids.ObjectAddress
			for address, descriptor := range scope.Catalog.Descriptors {
				if address.ClassID == ids.RelationClassID && descriptor.Name() == "copied" {
					created = address
				}
			}
			if created == (ids.ObjectAddress{}) || e.Catalog.State.Get(created) != nil || e.Storage.Relations[created] != nil || len(scope.Stage.Rows[created]) != 2 {
				t.Fatal("destination published before Finish")
			}
			transactionRows(t, peer, "SELECT id FROM a ORDER BY id", 1, 2)
			if canceled {
				cancel()
			}
			err = scope.Finish(nil)
			op.Done()
			if canceled {
				if err != context.Canceled || e.Catalog.State.Get(created) != nil || e.Storage.Relations[created] != nil {
					t.Fatal("cancellation published destination", err)
				}
				transactionRows(t, peer, "SELECT id FROM a ORDER BY id", 1, 2)
			} else {
				if err != nil || e.Catalog.State.Get(created) == nil {
					t.Fatal(err)
				}
				transactionRows(t, peer, "SELECT id FROM a")
				transactionRows(t, peer, "SELECT id FROM copied ORDER BY id", 1, 2)
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
