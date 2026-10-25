package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func lifecycleViewSource(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, form string) {
	t.Helper()
	if form == "inferred" {
		lifecycleExec(t, p, b, c, "CREATE TEMP TABLE al_temp_input(x integer)")
		createAggregateRun(t, p, b, c, fixture.Exec("INSERT INTO al_temp_input VALUES(1),(2),(3)", 3))
	}
}

func lifecycleViewDefinition(form string) string {
	if form == "temporary" {
		return "CREATE TEMP VIEW al_uncommitted AS SELECT al_sum(x) AS v FROM al_input"
	}
	if form == "inferred" {
		return "CREATE VIEW al_uncommitted AS SELECT al_sum(x) AS v FROM al_temp_input"
	}
	return "CREATE VIEW al_uncommitted AS SELECT al_sum(x) AS v FROM al_input"
}

func lifecycleViewPersistence(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, form string) {
	t.Helper()
	createAggregateBoth(t, p, b, c, fixture.Select("SELECT relpersistence='t' AS temporary FROM pg_class WHERE oid='al_uncommitted'::regclass", []string{"temporary"}, []string{"BOOL"}, [][]any{{form != "permanent"}}))
}

func TestReferenceAggregateLifecycleViewRetirement(t *testing.T) {
	for _, form := range []string{"permanent", "temporary", "inferred"} {
		for _, prepared := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared_%t/commit_%t", form, prepared, commit), func(t *testing.T) {
					createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						lifecycleSetup(t, p, b, c)
						id := lifecycleOID(t, p, c, "al_sum(integer)")
						peer, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer peer.Close()
						defer c.ExecContext(p.Ctx, "ROLLBACK")
						defer peer.ExecContext(p.Ctx, "ROLLBACK")
						lifecycleViewSource(t, p, b, peer, form)
						query := lifecycleViewDefinition(form)
						stmt, err := peer.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						holder, waiter := 0, 0
						if b.PG {
							holder, waiter = fixture.LockPID(t, p.Ctx, c), fixture.LockPID(t, p.Ctx, peer)
						}
						lifecycleExec(t, p, b, c, "BEGIN", "DROP AGGREGATE al_sum(integer)")
						lifecycleExec(t, p, b, peer, "BEGIN")
						ctx, cancel := context.WithCancel(p.Ctx)
						defer cancel()
						finished := make(chan struct{})
						join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
							defer close(finished)
							var result sql.Result
							var err error
							if prepared {
								result, err = stmt.ExecContext(ctx)
							} else {
								result, err = peer.ExecContext(ctx, query)
							}
							return fixture.RelationOutcome{Result: result, Err: err}
						})
						joined := false
						defer func() {
							cancel()
							if !joined {
								join()
							}
						}()
						fixture.ObjectLockPending(t, p, b, waiter, holder, 1255, id, "AccessShareLock", finished)
						end := "ROLLBACK"
						if commit {
							end = "COMMIT"
						}
						lifecycleExec(t, p, b, c, end)
						outcome := join()
						joined = true
						if commit {
							if outcome.Result != nil {
								t.Fatal("retired provider exposed result")
							}
							want := fixture.ControlsReferenceDiagnostic("42704", "referenced function was concurrently dropped")
							fixture.AssertDiagnostic(t, outcome.Err, &want)
							createAggregateRun(t, p, b, peer, fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
						} else {
							fixture.DeleteReferenceCount(t, outcome.Result, outcome.Err, 0)
							lifecycleViewPersistence(t, p, b, peer, form)
							createAggregateBoth(t, p, b, peer, createAggregateValue("SELECT v FROM al_uncommitted", int64(6)))
							lifecycleFacts(t, p, b, c, "al_sum", id)
						}
						lifecycleExec(t, p, b, peer, "ROLLBACK")
						createAggregateBoth(t, p, b, peer, fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='al_uncommitted'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
						createAggregateBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE oid=$1::oid", []string{"n"}, []string{"INT8"}, [][]any{{map[bool]int64{false: 1, true: 0}[commit]}}, id))
						createAggregateBoth(t, p, b, c, fixture.Select("SELECT al_sum(x::bigint) AS v FROM al_input", []string{"v"}, []string{"INT8"}, [][]any{{int64(106)}}))
					})
				})
			}
		}
	}
}
