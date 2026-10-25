package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAggregateLifecycleCancellation(t *testing.T) {
	for _, form := range []string{"permanent", "temporary", "inferred"} {
		for _, prepared := range []bool{false, true} {
			t.Run(form+"/"+map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
				createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					lifecycleSetup(t, p, b, c)
					lifecycleExec(t, p, b, c, "CREATE VIEW al_view AS SELECT al_sum(x) AS v FROM al_input")
					id := lifecycleOID(t, p, c, "al_sum(integer)")
					if !b.PG {
						for _, q := range []string{"ALTER AGGREGATE al_sum(integer) RENAME TO al_cancelled", "DROP AGGREGATE al_sum(integer) CASCADE"} {
							stmt, err := c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							ctx, cancel := context.WithCancel(p.Ctx)
							cancel()
							var result sql.Result
							if prepared {
								result, err = stmt.ExecContext(ctx)
							} else {
								result, err = c.ExecContext(ctx, q)
							}
							if result != nil || !errors.Is(err, context.Canceled) {
								t.Fatalf("pre-cancel result=%v error=%v", result, err)
							}
							lifecycleFacts(t, p, b, c, "al_sum", id)
							lifecycleExec(t, p, b, c, "BEGIN")
							fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 0))
							lifecycleExec(t, p, b, c, "ROLLBACK")
							stmt.Close()
							lifecycleFacts(t, p, b, c, "al_sum", id)
						}
					}
					other, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer other.Close()
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					defer other.ExecContext(p.Ctx, "ROLLBACK")
					const drop = "DROP AGGREGATE al_sum(integer) CASCADE"
					stmt, err := other.PrepareContext(p.Ctx, drop)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					holder, waiter := 0, 0
					if b.PG {
						holder = fixture.LockPID(t, p.Ctx, c)
						waiter = fixture.LockPID(t, p.Ctx, other)
					}
					lifecycleViewSource(t, p, b, c, form)
					lifecycleExec(t, p, b, c, "BEGIN", lifecycleViewDefinition(form))
					lifecycleViewPersistence(t, p, b, c, form)
					ctx, cancel := context.WithCancel(p.Ctx)
					defer cancel()
					finished := make(chan struct{})
					await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
						defer close(finished)
						var result sql.Result
						var err error
						if prepared {
							result, err = stmt.ExecContext(ctx)
						} else {
							result, err = other.ExecContext(ctx, drop)
						}
						return fixture.RelationOutcome{Result: result, Err: err}
					})
					joined := false
					defer func() {
						cancel()
						if !joined {
							await()
						}
					}()
					fixture.ObjectLockPending(t, p, b, waiter, holder, 1255, id, "AccessExclusiveLock", finished)
					cancel()
					outcome := await()
					joined = true
					if outcome.Result != nil {
						t.Fatal("canceled drop exposed result")
					}
					if b.PG {
						want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
						fixture.AssertDiagnostic(t, outcome.Err, &want)
					} else if !errors.Is(outcome.Err, context.Canceled) {
						t.Fatal(outcome.Err)
					}
					lifecycleExec(t, p, b, c, "ROLLBACK")
					lifecycleFacts(t, p, b, c, "al_sum", id)
					lifecycleValues(t, p, b, c, "al_sum")
					createAggregateBoth(t, p, b, c, createAggregateValue("SELECT v FROM al_view", int64(6)))
					createAggregateBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='al_uncommitted'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				})
			})
		}
	}
}
