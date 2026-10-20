package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestUpdateFromCancelledTraversalAndPublicationCleanup(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, phase := range []string{"source_rejected", "candidate_rejected", "output", "after_output"} {
			t.Run(map[bool]string{false: "implicit/", true: "explicit/"}[explicit]+phase, func(t *testing.T) {
				e := New()
				writer := sessionStart(t, e, nil)
				peer := sessionStart(t, e, nil)
				for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "CREATE TABLE s(k INT)", "CREATE TABLE r(k INT)", "INSERT INTO t VALUES(1,10),(2,20)", "INSERT INTO s VALUES(1),(2)", "INSERT INTO r VALUES(1),(2)"} {
					sessionSQL(t, peer, q)
				}
				var tx ids.TxRef
				if explicit {
					tx = transactionBegin(t, writer)
					sessionSQL(t, writer, "UPDATE t SET v=11 WHERE id=1")
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				op, scope, b, target, from := updateFromBoundSources(t, e, writer, ctx, "UPDATE t SET v=s.k FROM s JOIN r ON s.k=r.k WHERE t.id=s.k RETURNING s.k")
				children := append([]*exec.Source(nil), from.Sources...)
				calls := 0
				switch phase {
				case "source_rejected":
					join := b.Update.From.(*plan.JoinNode)
					join.Predicate = queryProbe(join.Predicate.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
						calls++
						if calls == 3 {
							cancel()
						}
						return sqltype.Boolean(join.Predicate.ResultUsage().Type, false), nil
					})
				case "candidate_rejected":
					usage := b.Update.Predicate.ResultUsage()
					b.Update.Predicate = queryProbe(usage, func(c *exec.Context) (sqltype.Value, error) {
						calls++
						if calls == 3 {
							cancel()
						}
						return sqltype.Boolean(usage.Type, false), nil
					})
				default:
					original := b.Returning.Expressions[0]
					b.Returning.Expressions[0] = queryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
						calls++
						if phase == "output" && calls == 2 {
							cancel()
							return sqltype.Value{}, ctx.Err()
						}
						value, err := original.Eval(c)
						if phase == "after_output" && calls == 2 {
							cancel()
						}
						return value, err
					})
				}
				r, err := e.executeUpdate(ctx, scope, b, target, from, nil, "public")
				wantCalls := 2
				if phase == "source_rejected" || phase == "candidate_rejected" {
					wantCalls = 3
				}
				if !errors.Is(err, context.Canceled) || r.Count != 0 || r.Rows != nil || calls != wantCalls {
					scope.Finish(err)
					op.Done()
					t.Fatal("cancelled statement leaked output or lost cancellation", r, err, calls)
				}
				if finishErr := scope.Finish(err); !errors.Is(finishErr, context.Canceled) {
					t.Fatal(finishErr)
				}
				op.Done()
				for _, child := range children {
					if child.View != nil || child.Committed != nil || child.Own != nil {
						t.Fatal("orphan source view", child)
					}
				}
				if target.View != nil || from.Sources != nil || len(scope.querySources) != 0 {
					t.Fatal("orphan captured statement")
				}
				if explicit {
					if writer.TransactionState() != TxAborted || writer.Transactions.writes != nil {
						t.Fatal("prior private write survived abort")
					}
					transactionEnd(t, writer, tx, false)
				}
				transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 20)
				lockEmpty(t, e)
				sessionSQL(t, peer, "UPDATE t SET v=s.k FROM s WHERE t.id=s.k")
				transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 1, 2)
				lockEmpty(t, e)
			})
		}
	}
}
