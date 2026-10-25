package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestDeleteReturningOutputAtomicity(t *testing.T) {
	for _, phase := range []string{"late_error", "output_cancel", "explicit_error", "explicit_cancel", "sequence_error"} {
		t.Run(phase, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1),(2),(3)")
			enginetest.SessionSQL(t, s, "CREATE SEQUENCE seq START 101 CACHE 1")
			var tx ids.TxRef
			if phase == "explicit_error" || phase == "explicit_cancel" {
				tx = enginetest.TransactionBegin(t, s)
				enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(7)")
				prior := enginetest.SessionSQL(t, s, "DELETE FROM t WHERE v=3 RETURNING v")
				if prior.Count != 1 {
					t.Fatal(prior)
				}
				prior.Rows.Close()
			}
			scope, b, source := deleteProbeScope(t, s)
			integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
			sentinel := errors.New("late RETURNING failure")
			ctx, cancel := context.WithCancel(scope.Op.Context())
			defer cancel()
			scope.Op.Ctx = ctx
			calls := 0
			probe := queryProbe(integer.Usage(), func(c *exec.Context) (sqltype.Value, error) {
				calls++
				root := scope.Stage.Deletes[b.Ref.Address]
				if root == nil || len(root.Rows) != calls {
					t.Fatal("output ran before actual stage", calls, root)
				}
				if calls == 2 {
					if phase == "output_cancel" || phase == "explicit_cancel" {
						cancel()
						return sqltype.Value{}, context.Canceled
					}
					return sqltype.Value{}, sentinel
				}
				return c.Row[0], nil
			})
			b.Returning = &analyze.Returning{Expressions: []expr.Expr{probe}, Schema: []catalog.Column{{Name: "v", Usage: integer.Usage()}}}
			if phase == "sequence_error" {
				e.Mu.Lock()
				binder := core.NewExpressionBinder(ctx, e, scope.Input, scope.Op, nil)
				output, err := binder.BindReturning(enginetest.Statement(t, "DELETE FROM t RETURNING nextval('seq')").(*parser.Delete).Returning, analyze.ReturningDelete)
				e.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				b.Returning.Expressions = append(output.Expressions, probe)
				b.Returning.Schema = append(output.Schema, b.Returning.Schema...)
			}
			result, err := dml.ExecuteDelete(scope.WriteHost(), dml.DeleteInput{Binding: b, Source: source})
			want := sentinel
			if phase == "output_cancel" || phase == "explicit_cancel" {
				want = context.Canceled
			}
			if !errors.Is(err, want) || result.Rows != nil || result.Count != 0 || calls != 2 {
				t.Fatal(result, err, calls)
			}
			if err = scope.Finish(err); !errors.Is(err, want) {
				t.Fatal(err)
			}
			if phase == "explicit_error" || phase == "explicit_cancel" {
				if s.TransactionState() != session.TxAborted {
					t.Fatal("not aborted")
				}
				enginetest.TransactionEnd(t, s, tx, false)
			}
			enginetest.TransactionRows(t, s, "SELECT v FROM t ORDER BY v", 1, 2, 3)
			if phase == "sequence_error" {
				enginetest.TransactionRows(t, s, "SELECT nextval('seq')", 103)
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestDeleteReturningCancellation(t *testing.T) {
	for _, wins := range []bool{false, true} {
		t.Run(map[bool]string{false: "publication_veto", true: "postpublication"}[wins], func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1),(2)")
			op := enginetest.TransactionOperation(t, s)
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			result, err := scope.Execute(enginetest.Statement(t, "DELETE FROM t RETURNING old.v,new.v"), 0, nil)
			if err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			defer result.Rows.Close()
			base, cancel := context.WithCancel(op.Context())
			defer cancel()
			if wins {
				op.Ctx = &deleteCancelAfterCheck{Context: base, cancel: cancel, at: 2}
			} else {
				op.Ctx = &cancelAtErrCheck{Context: base, cancel: cancel, at: 2}
			}
			err = scope.Finish(nil)
			if wins {
				if err != nil {
					t.Fatal(err)
				}
				enginetest.TransactionRows(t, s, "SELECT v FROM t ORDER BY v")
			} else {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				result.Rows.Close()
				enginetest.TransactionRows(t, s, "SELECT v FROM t ORDER BY v", 1, 2)
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func deleteProbeScope(t *testing.T, s *session.Session) (*core.StatementScope, analyze.Binding, *exec.Source) {
	t.Helper()
	op := enginetest.TransactionOperation(t, s)
	scope, err := core.EngineOf(s).StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !scope.Finished {
			_ = scope.Finish(errors.New("test cleanup"))
		}
	})
	_, err = scope.Describe(enginetest.Statement(t, "DELETE FROM t"))
	if err != nil {
		_ = scope.Finish(err)
		t.Fatal(err)
	}
	ref := scope.LockedRef
	view, err := scope.AcquireDataView(ref)
	if err != nil {
		_ = scope.Finish(err)
		t.Fatal(err)
	}
	source, err := exec.CaptureSource(op.Context(), view, ref)
	if err != nil {
		_ = scope.Finish(err)
		t.Fatal(err)
	}
	t.Cleanup(source.Close)
	relation := scope.LockedDescriptor.(*catalog.Relation)
	return scope, analyze.Binding{Ref: ref, Relation: relation, Storage: scope.LockedStorage}, source
}

type deleteCancelAfterCheck struct {
	context.Context
	cancel context.CancelFunc
	checks int
	at     int
}

func (c *deleteCancelAfterCheck) Err() error {
	c.checks++
	err := c.Context.Err()
	if c.checks == c.at {
		c.cancel()
	}
	return err
}
