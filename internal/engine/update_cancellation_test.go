package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Reuse the real binder, admission and view; only the existing evaluator seam
// is replaced when a test must observe construction between two row payloads.
func updateBoundSource(t *testing.T, e *Engine, s *session.Session, ctx context.Context, q string) (*session.Operation, *StatementScope, analyze.Binding, *exec.Source) {
	t.Helper()
	op, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	fail := func(err error) { scope.Finish(err); op.Done(); t.Fatal(err) }
	ast := statement(t, q)
	if err = scope.gate(ast); err != nil {
		fail(err)
	}
	if err = scope.acquireRelation(ast, true); err != nil {
		fail(err)
	}
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	var b analyze.Binding
	var view *visibility.View
	if err == nil {
		b, err = describe(op.Ctx, e.analysisHost(op), ast, auth, scope.lockedRef, scope.input, nil, &scope.queryLockAdmission)
		scope.routineAuth, scope.routineDependencies = auth, b.Dependencies
	}
	if err == nil {
		view, err = scope.acquireDataViewLocked(e, b.Ref)
	}
	e.mu.Unlock()
	if err != nil {
		fail(err)
	}
	source, err := exec.CaptureSource(op.Context(), view, b.Ref)
	if err != nil {
		fail(err)
	}
	return op, scope, b, source
}

func TestUpdateCoreMidExecutionCancellation(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, deadline := range []bool{false, true} {
			t.Run(map[bool]string{false: "implicit", true: "explicit"}[explicit]+"/"+map[bool]string{false: "cancel", true: "deadline"}[deadline], func(t *testing.T) {
				e := New()
				a := sessionStart(t, e, nil)
				peer := sessionStart(t, e, nil)
				sessionSQL(t, peer, "CREATE TABLE t(v INT)")
				sessionSQL(t, peer, "INSERT INTO t VALUES(1),(2)")
				ref := catalogRef(t, e, e.CatalogRead(), "t")
				saved := e.storage.Relations[ref.Address].Versions
				var tx ids.TxRef
				if explicit {
					tx = transactionBegin(t, a)
					sessionSQL(t, a, "UPDATE t SET v=3 WHERE v=1")
				}
				ctx, cancel := context.WithCancel(context.Background())
				want := error(context.Canceled)
				if deadline {
					cancel()
					ctx, cancel = context.WithTimeout(context.Background(), time.Second)
					want = context.DeadlineExceeded
				}
				defer cancel()
				op, scope, b, source := updateBoundSource(t, e, a, ctx, "UPDATE t SET v=v")
				calls := 0
				b.Update.Assignments[0] = queryProbe(b.Relation.Columns[0].Usage, func(c *exec.Context) (sqltype.Value, error) {
					calls++
					if calls == 2 {
						if deadline {
							<-ctx.Done()
						} else {
							cancel()
						}
						return sqltype.Value{}, ctx.Err()
					}
					if err := ctx.Err(); err != nil {
						return sqltype.Value{}, err
					}
					return sqltype.Integer(c.Row[0].Type(), 9)
				})
				r, err := executeUpdate(ctx, scope, b, source, nil, nil, "public")
				if !errors.Is(err, want) || r.Count != 0 || r.Rows != nil || calls != 2 {
					scope.Finish(err)
					op.Done()
					t.Fatal("mid-payload cancellation", r, err, calls)
				}
				if got := scope.Finish(err); !errors.Is(got, want) {
					op.Done()
					t.Fatal("lost original error", got)
				}
				op.Done()
				if testExport(saved[0].Values[0]) != int64(1) || testExport(saved[1].Values[0]) != int64(2) {
					t.Fatal("retained root changed")
				}
				if explicit {
					if a.TransactionState() != session.TxAborted || a.Transactions.Writes != nil {
						t.Fatal("prior private effects not discarded")
					}
					op := transactionOperation(t, a)
					_, err = e.Execute(op, statement(t, "UPDATE t SET v=4"), 0, nil)
					op.Done()
					var d *sqlstate.Error
					if !errors.As(err, &d) || d.Code != "25P02" {
						t.Fatal(err)
					}
					transactionEnd(t, a, tx, false)
				}
				transactionRows(t, peer, "SELECT v FROM t ORDER BY v", 1, 2)
				lockEmpty(t, e)
				sessionSQL(t, peer, "UPDATE t SET v=7")
				transactionRows(t, peer, "SELECT v FROM t ORDER BY v", 7, 7)
				lockEmpty(t, e)
			})
		}
	}
}
