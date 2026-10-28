package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWSQLValidatorCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		mode := "direct"
		if prepared {
			mode = "prepared"
		}
		t.Run(mode, func(t *testing.T) {
			for _, alter := range []bool{false, true} {
				action := "create"
				if alter {
					action = "alter"
				}
				t.Run(action, func(t *testing.T) {
					fdwValidatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
						run(fixture.Exec("CREATE TABLE fdw_validator_gate(v integer)", 0))
						run(fixture.Exec("INSERT INTO fdw_validator_gate VALUES(1)", 1))
						run(fixture.Exec("CREATE TABLE fdw_validator_audit(kind oid)", 0))
						// This is an ordinary SQL body, not a registered callback or
						// a sleep surrogate. Only its relation read can reach the gate.
						run(fixture.Exec("CREATE FUNCTION public.fdw_accept(text[],oid) RETURNS integer LANGUAGE SQL AS 'INSERT INTO public.fdw_validator_audit SELECT $2; SELECT v FROM public.fdw_validator_gate'", 0))
						run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_v VALIDATOR public.fdw_accept", 0))
						query := "CREATE SERVER fdw_vs FOREIGN DATA WRAPPER fdw_v OPTIONS(seed 'candidate')"
						if alter {
							run(fixture.Exec("CREATE SERVER fdw_vs FOREIGN DATA WRAPPER fdw_v OPTIONS(seed 'incumbent')", 0))
							query = "ALTER SERVER fdw_vs OPTIONS(SET seed 'candidate')"
						}
						run(fixture.Exec("TRUNCATE fdw_validator_audit", 0))
						var stmt *sql.Stmt
						if prepared {
							var err error
							stmt, err = c.PrepareContext(p.Ctx, query)
							if err != nil {
								t.Fatal(err)
							}
							t.Cleanup(func() { stmt.Close() })
						}
						holder, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() {
							ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
							defer cancel()
							if _, err := holder.ExecContext(ctx, "ROLLBACK"); err != nil {
								t.Error(err)
							}
							holder.Close()
						})
						waiterPID, holderPID := 0, 0
						var oid int64
						if b.PG {
							waiterPID = fixture.LockPID(t, p.Ctx, c)
							holderPID = fixture.LockPID(t, p.Ctx, holder)
							oid = fixture.LockOID(t, p.Ctx, "public.fdw_validator_gate")
						}
						fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("BEGIN", 0))
						fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("LOCK TABLE fdw_validator_gate IN ACCESS EXCLUSIVE MODE", 0))
						ctx, cancel := context.WithCancel(p.Ctx)
						t.Cleanup(cancel)
						finished := make(chan struct{})
						join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
							defer close(finished)
							var result sql.Result
							var err error
							if prepared {
								result, err = stmt.ExecContext(ctx)
							} else {
								result, err = c.ExecContext(ctx, query)
							}
							return fixture.RelationOutcome{Result: result, Err: err}
						}, 15*time.Second)
						fixture.RelationLockPending(t, p, b, waiterPID, holderPID, oid, "AccessShareLock", finished)
						if b.PG {
							var signaled bool
							if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", waiterPID).Scan(&signaled); err != nil || !signaled {
								t.Fatalf("cancel validator: signaled=%t error=%v", signaled, err)
							}
						} else {
							cancel()
						}
						outcome := join()
						if outcome.Result != nil || outcome.Rows != nil || outcome.Stmt != nil {
							t.Fatal("canceled validator published a result, rows or statement")
						}
						if b.PG {
							d := fdwDiagnostic("57014", "canceling statement due to user request")
							d.Context = `SQL function "fdw_accept" statement 2`
							fixture.AssertDiagnostic(t, outcome.Err, &d)
						} else {
							fixture.LexicalOrdinary(t, outcome.Err, context.Canceled)
						}
						// Check publication before releasing the gate. Cancellation
						// must join on its own, not complete because the holder exits.
						if alter {
							run(fdwValidatorServerOptions("{seed=incumbent}"))
						} else {
							run(fixture.Select("SELECT count(*) FROM pg_foreign_server WHERE srvname='fdw_vs'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
						}
						run(fixture.Select("SELECT kind::bigint FROM fdw_validator_audit", []string{"kind"}, []string{"INT8"}, nil))
						run(fixture.Select("SELECT 42 AS usable", []string{"usable"}, []string{"INT4"}, [][]any{{int64(42)}}))
						fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("ROLLBACK", 0))
						step := fixture.Exec(query, 0)
						step.Prepared = prepared
						run(step)
						run(fdwValidatorServerOptions("{seed=candidate}"))
						run(fixture.Select("SELECT kind::bigint FROM fdw_validator_audit", []string{"kind"}, []string{"INT8"}, [][]any{{int64(1417)}}))
					})
				})
			}
		})
	}
}
