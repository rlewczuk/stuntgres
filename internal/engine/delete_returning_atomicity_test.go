package engine

import (
	"context"
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestDeleteReturningOutputAtomicity(t *testing.T) {
	for _, phase := range []string{"late_error", "output_cancel", "explicit_error", "explicit_cancel", "sequence_error"} {
		t.Run(phase, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(v INT)")
			sessionSQL(t, s, "INSERT INTO t VALUES(1),(2),(3)")
			sessionSQL(t, s, "CREATE SEQUENCE seq START 101 CACHE 1")
			var tx TxRef
			if phase == "explicit_error" || phase == "explicit_cancel" {
				tx = transactionBegin(t, s)
				sessionSQL(t, s, "INSERT INTO t VALUES(7)")
				prior := sessionSQL(t, s, "DELETE FROM t WHERE v=3 RETURNING v")
				if prior.Count != 1 {
					t.Fatal(prior)
				}
				prior.Rows.Close()
			}
			scope, b, source := deleteProbeScope(t, s)
			integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
			sentinel := errors.New("late RETURNING failure")
			ctx, cancel := context.WithCancel(scope.op.Context())
			defer cancel()
			scope.op.ctx = ctx
			calls := 0
			probe := queryProbe(integer.Usage(), func(c *EvalContext) (sqltype.Value, error) {
				calls++
				root := scope.stage.deletes[b.ref.Address]
				if root == nil || len(root.rows) != calls {
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
			b.returning = &boundReturning{expressions: []BoundExpr{probe}, schema: []ColumnDescriptor{{Name: "v", Usage: integer.Usage()}}}
			if phase == "sequence_error" {
				e.mu.Lock()
				binder := newExpressionBinder(ctx, e, scope.input, scope.op, nil)
				output, err := binder.bindReturning(statement(t, "DELETE FROM t RETURNING nextval('seq')").(*parser.Delete).Returning, returningDelete)
				e.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				b.returning.expressions = append(output.expressions, probe)
				b.returning.schema = append(output.schema, b.returning.schema...)
			}
			result, err := scope.deleteSource(b, source, nil, nil)
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
				if s.TransactionState() != TxAborted {
					t.Fatal("not aborted")
				}
				transactionEnd(t, s, tx, false)
			}
			transactionRows(t, s, "SELECT v FROM t ORDER BY v", 1, 2, 3)
			if phase == "sequence_error" {
				transactionRows(t, s, "SELECT nextval('seq')", 103)
			}
			lockEmpty(t, e)
		})
	}
}

func TestDeleteReturningCancellation(t *testing.T) {
	for _, wins := range []bool{false, true} {
		t.Run(map[bool]string{false: "publication_veto", true: "postpublication"}[wins], func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(v INT)")
			sessionSQL(t, s, "INSERT INTO t VALUES(1),(2)")
			op := transactionOperation(t, s)
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			result, err := scope.Execute(statement(t, "DELETE FROM t RETURNING old.v,new.v"), 0, nil)
			if err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			defer result.Rows.Close()
			base, cancel := context.WithCancel(op.Context())
			defer cancel()
			if wins {
				op.ctx = &deleteCancelAfterCheck{Context: base, cancel: cancel, at: 2}
			} else {
				op.ctx = &cancelAtErrCheck{Context: base, cancel: cancel, at: 2}
			}
			err = scope.Finish(nil)
			if wins {
				if err != nil {
					t.Fatal(err)
				}
				transactionRows(t, s, "SELECT v FROM t ORDER BY v")
			} else {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				result.Rows.Close()
				transactionRows(t, s, "SELECT v FROM t ORDER BY v", 1, 2)
			}
			lockEmpty(t, e)
		})
	}
}
