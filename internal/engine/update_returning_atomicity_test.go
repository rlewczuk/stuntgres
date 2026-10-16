package engine

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestUpdateReturningProjectionAtomicity(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, cancelOutput := range []bool{false, true} {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(v INT)")
			sessionSQL(t, s, "INSERT INTO t VALUES(1),(2),(3)")
			var tx TxRef
			if explicit {
				tx = transactionBegin(t, s)
				sessionSQL(t, s, "UPDATE t SET v=7 WHERE v=1")
				sessionSQL(t, s, "DELETE FROM t WHERE v=3")
			}
			ctx, cancel := context.WithCancel(context.Background())
			op, scope, b, source := updateBoundSource(t, e, s, ctx, "UPDATE t SET v=v+1 RETURNING old.v,new.v")
			sentinel := errors.New("late output failure")
			want := error(sentinel)
			calls := 0
			if cancelOutput {
				want = context.Canceled
			}
			usage := b.relation.columns[0].Usage
			b.returning.expressions = []BoundExpr{queryProbe(usage, func(c *EvalContext) (sqltype.Value, error) {
				calls++
				if testExport(c.Row[0]).(int64) != testExport(c.OldRow[0]).(int64)+1 {
					return sqltype.Value{}, fmt.Errorf("incorrect version frames: %v %v", c.Row, c.OldRow)
				}
				if calls == 2 {
					if cancelOutput {
						cancel()
						return sqltype.Value{}, ctx.Err()
					}
					return sqltype.Value{}, sentinel
				}
				return c.Row[0], nil
			})}
			b.returning.schema = b.returning.schema[:1]
			r, err := e.executeUpdate(ctx, scope, b, source, nil, nil, "public")
			finishErr := scope.Finish(err)
			op.Done()
			cancel()
			if !errors.Is(err, want) || r.Rows != nil || r.Count != 0 || calls != 2 {
				if r.Rows != nil {
					r.Rows.Close()
				}
				t.Fatal(r, err, calls)
			}
			if !errors.Is(finishErr, want) {
				t.Fatal(finishErr)
			}
			if explicit {
				if s.TransactionState() != TxAborted {
					t.Fatal(s.TransactionState())
				}
				transactionEnd(t, s, tx, false)
			}
			transactionRows(t, s, "SELECT v FROM t ORDER BY v", 1, 2, 3)
			lockEmpty(t, e)
		}
	}
}

func TestUpdateReturningConstraintBeforeProjection(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(v INT NOT NULL CHECK(v>0))")
	sessionSQL(t, s, "INSERT INTO t VALUES(1),(2)")
	for _, q := range []string{"UPDATE t SET v=NULL RETURNING old.v", "UPDATE t SET v=0 RETURNING old.v"} {
		op, scope, b, source := updateBoundSource(t, e, s, context.Background(), q)
		calls := 0
		b.returning.expressions[0] = queryProbe(b.relation.columns[0].Usage, func(c *EvalContext) (sqltype.Value, error) { calls++; return c.OldRow[0], nil })
		r, err := e.executeUpdate(op.Context(), scope, b, source, nil, nil, "public")
		if err == nil || calls != 0 || r.Rows != nil || r.Count != 0 {
			t.Fatal(r, err, calls)
		}
		scope.Finish(err)
		op.Done()
		transactionRows(t, s, "SELECT v FROM t ORDER BY v", 1, 2)
		lockEmpty(t, e)
	}
}
