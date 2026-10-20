package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func cteDMLUnitScope(t *testing.T, session *Session, ctx context.Context) (*StatementScope, *Operation) {
	t.Helper()
	op, err := session.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := session.Engine().StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !scope.finished {
			scope.Finish(errors.New("unit cleanup"))
		}
		op.Done()
	})
	return scope, op
}

func TestCTEDMLCancellationBeforeFinish(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "implicit", true: "explicit"}[explicit], func(t *testing.T) {
			e := New()
			worker, observer := sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE a(id INT)", "CREATE TABLE b(id INT)", "INSERT INTO a VALUES(1),(2)", "INSERT INTO b VALUES(9)"} {
				sessionSQL(t, worker, q)
			}
			a, b := catalogRef(t, e, e.CatalogRead(), "a"), catalogRef(t, e, e.CatalogRead(), "b")
			var tx ids.TxRef
			if explicit {
				tx = transactionBegin(t, worker)
				sessionSQL(t, worker, "INSERT INTO b VALUES(7)")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			scope, op := cteDMLUnitScope(t, worker, ctx)
			result, err := scope.Execute(statement(t, "WITH moved AS (DELETE FROM a RETURNING id), copied AS (INSERT INTO b SELECT id FROM moved RETURNING id) SELECT * FROM copied"), 0, nil)
			if err != nil || result.Count != 2 || result.Rows == nil {
				t.Fatal(result, err)
			}
			defer result.Rows.Close()
			if scope.completed || len(scope.stage.rows[b.Address]) != 2 || scope.stage.deletes[a.Address] == nil || len(scope.stage.deletes[a.Address].Rows) != 2 || scope.cteInvocation != nil {
				t.Fatal("not one unpublished complete stage")
			}
			view := scope.view.Load()
			if view == nil || len(view.Committed) < 2 || &view.Committed[a.Address][0].Values[0] != &e.storage.Relations[a.Address].Versions[0].Values[0] || &view.Committed[b.Address][0].Values[0] != &e.storage.Relations[b.Address].Versions[0].Values[0] {
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
			if scope.view != nil || scope.ticket != nil || scope.stage.rows != nil || scope.stage.deletes != nil || scope.mutations != nil || scope.cteInvocation != nil {
				t.Fatal("cancel retained statement roots")
			}
			transactionRows(t, observer, "SELECT id FROM a ORDER BY id", 1, 2)
			transactionRows(t, observer, "SELECT id FROM b ORDER BY id", 9)
			want := TxIdle
			if explicit {
				want = TxAborted
			}
			if worker.TransactionState() != want {
				t.Fatal(worker.TransactionState(), want)
			}
			if explicit {
				transactionEnd(t, worker, tx, false)
			}
			lockEmpty(t, e)
		})
	}
}

func TestCTEDMLAtomicPublication(t *testing.T) {
	e := New()
	worker, observer := sessionStart(t, e, nil), sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE a(id INT)", "CREATE TABLE b(id INT)", "INSERT INTO a VALUES(1),(2)"} {
		sessionSQL(t, worker, q)
	}
	scope, op := cteDMLUnitScope(t, worker, context.Background())
	result, err := scope.Execute(statement(t, "WITH d AS (DELETE FROM a RETURNING id), i AS (INSERT INTO b SELECT id FROM d RETURNING id) SELECT * FROM i"), 0, nil)
	if err != nil || result.Rows == nil {
		t.Fatal(result, err)
	}
	defer result.Rows.Close()
	before := e.visibility.CommitSeq()
	transactionRows(t, observer, "SELECT id FROM a ORDER BY id", 1, 2)
	transactionRows(t, observer, "SELECT id FROM b")
	if scope.completed {
		t.Fatal("auxiliary published before Finish")
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	op.Done()
	if e.visibility.CommitSeq() != before+1 {
		t.Fatal("more than one publication frontier", before, e.visibility.CommitSeq())
	}
	transactionRows(t, observer, "SELECT id FROM a")
	transactionRows(t, observer, "SELECT id FROM b ORDER BY id", 1, 2)
	if scope.view != nil || scope.ticket != nil || scope.mutations != nil {
		t.Fatal("successful Finish retained owner")
	}
	lockEmpty(t, e)
}

func TestCTEDMLLateFailureIdentity(t *testing.T) {
	for _, phase := range []string{"output_error", "output_cancel", "close_veto", "close_veto_exec", "primary_error_before_close"} {
		t.Run(phase, func(t *testing.T) {
			e := New()
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
			ast := statement(t, sql)
			if _, err := scope.Describe(ast); err != nil {
				t.Fatal(err)
			}
			e.mu.Lock()
			auth, err := e.statementAuthorizationLocked(op, ast)
			if err == nil {
				err = scope.captureIdentityLocked(auth)
			}
			var bound binding
			if err == nil {
				bound, err = describe(ctx, e.analysisHost(op), ast, auth, scope.lockedRef, scope.input, nil, &scope.queryLockAdmission)
			}
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			a := catalogRef(t, e, e.CatalogRead(), "a")
			integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
			primary := errors.New("later actual RETURNING failure")
			closeFailure := errors.New("actual producer Close veto")
			calls, closes := 0, 0
			probe := queryProbe(integer.Usage(), func(c *exec.Context) (sqltype.Value, error) {
				calls++
				if len(scope.stage.rows[a.Address]) != 1 {
					t.Fatal("later output did not follow accepted earlier private write")
				}
				if phase == "close_veto" || phase == "close_veto_exec" || phase == "primary_error_before_close" {
					producer := &exec.CTEProducer{Opened: true, Inner: exec.Stage{Finish: func() error { closes++; return closeFailure }}}
					scope.cteInvocation.Order = append(scope.cteInvocation.Order, producer)
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
			cteMutationOf(bound.ctes[1]).returning.expressions = []expr.Expr{probe}
			quotes := exec.QuoteCaptureCandidate{}
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: hostOf(scope), Catalog: auth.Catalog, Input: scope.input, Output: scope.output, QuoteCaptures: &quotes}}
			e.mu.Lock()
			result, err := e.executeCTECommandsLocked(scope, ast, bound, auth, nil, evaluation, nil, nil)
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
			lockEmpty(t, e)
		})
	}
}

func TestCTEDMLCTASPublication(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "cancel_before_finish"}[canceled], func(t *testing.T) {
			e := New()
			worker, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, worker, "CREATE TABLE a(id INT)")
			sessionSQL(t, worker, "INSERT INTO a VALUES(1),(2)")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			scope, op := cteDMLUnitScope(t, worker, ctx)
			result, err := scope.Execute(statement(t, "CREATE TABLE copied AS WITH d AS (DELETE FROM a RETURNING id) SELECT * FROM d"), 0, nil)
			if err != nil || result.Count != 2 || result.Rows != nil || scope.completed || scope.catalog == nil {
				t.Fatal("creation did not join the private statement stage", result, err)
			}
			var created ids.ObjectAddress
			for address, descriptor := range scope.catalog.descriptors {
				if address.ClassID == ids.RelationClassID && descriptor.Name() == "copied" {
					created = address
				}
			}
			if created == (ids.ObjectAddress{}) || e.catalog.state.Get(created) != nil || e.storage.Relations[created] != nil || len(scope.stage.rows[created]) != 2 {
				t.Fatal("destination published before Finish")
			}
			transactionRows(t, peer, "SELECT id FROM a ORDER BY id", 1, 2)
			if canceled {
				cancel()
			}
			err = scope.Finish(nil)
			op.Done()
			if canceled {
				if err != context.Canceled || e.catalog.state.Get(created) != nil || e.storage.Relations[created] != nil {
					t.Fatal("cancellation published destination", err)
				}
				transactionRows(t, peer, "SELECT id FROM a ORDER BY id", 1, 2)
			} else {
				if err != nil || e.catalog.state.Get(created) == nil {
					t.Fatal(err)
				}
				transactionRows(t, peer, "SELECT id FROM a")
				transactionRows(t, peer, "SELECT id FROM copied ORDER BY id", 1, 2)
			}
			lockEmpty(t, e)
		})
	}
}
