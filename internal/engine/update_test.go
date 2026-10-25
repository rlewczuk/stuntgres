package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestUpdateCoreVersionsAndPrivateHeads(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE TABLE t(v INT)")
	sessionSQL(t, b, "INSERT INTO t VALUES(1),(1)")
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	original := append([]storage.RowVersion(nil), e.Storage.Relations[ref.Address].Versions...)
	saved := transactionRows(t, b, "SELECT v FROM t", 1, 1)
	tx := transactionBegin(t, a)
	sessionSQL(t, a, "UPDATE t SET v=2")
	first := append([]storage.RowVersion(nil), a.Transactions.Writes[ref.Address]...)
	transactionRows(t, a, "SELECT v FROM t", 2, 2)
	transactionRows(t, b, "SELECT v FROM t", 1, 1)
	sessionSQL(t, a, "UPDATE t SET v=3")
	for i, row := range a.Transactions.Writes[ref.Address] {
		if row.Row != original[i].Row || row.Base != original[i].Version || row.Version <= first[i].Version {
			t.Fatal("replacement provenance", row, original[i], first[i])
		}
	}
	sessionSQL(t, a, "INSERT INTO t VALUES(4)")
	sessionSQL(t, a, "UPDATE t SET v=5 WHERE v=4")
	sessionSQL(t, a, "UPDATE t SET v=6 WHERE v=5")
	transactionRows(t, a, "SELECT v FROM t ORDER BY v", 3, 3, 6)
	transactionEnd(t, a, tx, true)
	transactionRows(t, b, "SELECT v FROM t ORDER BY v", 3, 3, 6)
	current := e.Storage.Relations[ref.Address].Versions
	if len(current) != 3 || current[0].Row != original[0].Row || current[1].Row != original[1].Row || current[0].Version == original[0].Version || current[0].Born <= original[0].Born || current[0].Base != 0 {
		t.Fatal("current committed heads", current)
	}
	if saved.Count != 2 || testExport(original[0].Values[0]) != int64(1) {
		t.Fatal("retained predecessor changed")
	}
	tx = transactionBegin(t, a)
	sessionSQL(t, a, "UPDATE t SET v=9")
	transactionEnd(t, a, tx, false)
	transactionRows(t, b, "SELECT v FROM t ORDER BY v", 3, 3, 6)
}

func TestUpdateCoreFinishCancellationAndExhaustion(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "implicit", true: "explicit"}[explicit], func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, b, "CREATE TABLE t(v INT)")
			sessionSQL(t, b, "INSERT INTO t VALUES(1),(2)")
			var tx ids.TxRef
			if explicit {
				tx = transactionBegin(t, a)
				sessionSQL(t, a, "INSERT INTO t VALUES(3)")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, err := a.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			r, err := scope.Execute(statement(t, "UPDATE t SET v=9"), 0, nil)
			if err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			if r.Count != map[bool]int64{false: 2, true: 3}[explicit] || len(scope.Stage.Rows) == 0 {
				scope.Finish(errors.New("bad stage"))
				t.Fatal("missing completed private stage")
			}
			cancel()
			if err = scope.Finish(nil); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			transactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 2)
			if explicit {
				if a.TransactionState() != session.TxAborted {
					t.Fatal(a.TransactionState())
				}
				transactionEnd(t, a, tx, false)
			}
			sessionSQL(t, b, "UPDATE t SET v=7")
			lockEmpty(t, e)
		})
	}
	e := New()
	a := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(v INT)")
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	before := e.Storage.Relations[catalogRef(t, e, e.CatalogRead(), "t").Address].Versions[0]
	e.Storage.SetFrontierLocked(e.Storage.RowFrontier(), ^uint64(0))
	op := transactionOperation(t, a)
	_, err := e.Execute(op, statement(t, "UPDATE t SET v=2"), 0, nil)
	if err == nil || err.Error() != "stuntgres: version identity exhausted" {
		t.Fatal(err)
	}
	after := e.Storage.Relations[catalogRef(t, e, e.CatalogRead(), "t").Address].Versions[0]
	if !reflect.DeepEqual(before, after) {
		t.Fatal("exhaustion mutated row")
	}
	lockEmpty(t, e)
}

func TestUpdateCoreLateEvaluatorFailure(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(v INT)")
	sessionSQL(t, a, "INSERT INTO t VALUES(1),(2)")
	op := transactionOperation(t, a)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	ast := statement(t, "UPDATE t SET v=v")
	if err = scope.Gate(ast); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	if err = scope.AcquireRelation(ast, true); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		scope.Finish(err)
		t.Fatal(err)
	}
	bound, err := Describe(op.Ctx, e.AnalysisHost(op), ast, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
	if err != nil {
		e.Mu.Unlock()
		scope.Finish(err)
		t.Fatal(err)
	}
	view, err := scope.AcquireDataViewLocked(e, bound.Ref)
	e.Mu.Unlock()
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	source, err := exec.CaptureSource(op.Context(), view, bound.Ref)
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	sentinel := errors.New("late evaluator failure")
	calls := 0
	bound.Update.Assignments[0] = queryProbe(bound.Relation.Columns[0].Usage, func(c *exec.Context) (sqltype.Value, error) {
		calls++
		if calls == 2 {
			return sqltype.Value{}, sentinel
		}
		return sqltype.Integer(c.Row[0].Type(), 9)
	})
	r, err := ExecuteUpdate(op.Ctx, scope, bound, source, nil, nil, "public")
	if err != sentinel || r.Count != 0 || calls != 2 {
		scope.Finish(err)
		t.Fatal(r, err, calls)
	}
	if err = scope.Finish(err); err != sentinel {
		t.Fatal(err)
	}
	transactionRows(t, a, "SELECT v FROM t ORDER BY v", 1, 2)
	lockEmpty(t, e)
}
