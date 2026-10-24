package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalRoutinesNameConflicts(t *testing.T) {
	t.Run("destinations", transactionalRoutineDestinationConflicts)
	t.Run("namespace-retirement", transactionalRoutineNamespaceRetirement)
	for _, family := range []string{"function", "aggregate", "operator"} {
		for _, outcome := range []string{"commit", "rollback", "cancel", "timeout"} {
			t.Run(family+"/"+outcome, func(t *testing.T) {
				routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, owner *sql.Conn) {
					waiter, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer waiter.Close()
					other, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer other.Close()
					q, independent, call := "CREATE FUNCTION txr_race(integer) RETURNS integer RETURN $1+1", "CREATE FUNCTION txr_race(bigint) RETURNS bigint RETURN $1+2", "SELECT txr_race(4) AS v"
					if family == "aggregate" {
						q = "CREATE AGGREGATE txr_race(integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')"
						call = "SELECT txr_race(v) AS v FROM(VALUES(2),(3)) s(v)"
					}
					if family == "operator" {
						q = "CREATE OPERATOR public.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4pl)"
						independent = "CREATE OPERATOR public.#~# (RIGHTARG=int4,FUNCTION=pg_catalog.int4um)"
						call = "SELECT 2 OPERATOR(public.#~#) 3 AS v"
					}
					pidOwner, pidWaiter := 0, 0
					if b.PG {
						pidOwner, pidWaiter = fixture.LockPID(t, p.Ctx, owner), fixture.LockPID(t, p.Ctx, waiter)
					}
					ns := transactionalRoutineOID(t, p, owner, "SELECT 'public'::regnamespace::oid")
					routineExec(t, p, b, owner, "BEGIN", q)
					defer owner.ExecContext(p.Ctx, "ROLLBACK")
					routineExec(t, p, b, waiter, "BEGIN")
					defer waiter.ExecContext(p.Ctx, "ROLLBACK")
					stmt, err := waiter.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal("quiet conflict prepare", err)
					}
					defer stmt.Close()
					if outcome == "timeout" {
						routineExec(t, p, b, waiter, "SET LOCAL lock_timeout='100ms'")
					}
					ctx, cancel := context.WithCancel(p.Ctx)
					defer cancel()
					finished := make(chan struct{})
					join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
						defer close(finished)
						result, err := stmt.ExecContext(ctx)
						return fixture.RelationOutcome{Result: result, Err: err}
					})
					joined := false
					defer func() {
						cancel()
						if !joined {
							join()
						}
					}()
					if outcome != "timeout" {
						fixture.RelationLockPending(t, p, b, pidWaiter, pidOwner, 0, "", finished)
					}
					// An unrelated overload is not serialized behind the pending full key.
					routineExec(t, p, b, other, independent, "CREATE TABLE txr_unrelated(v integer)")
					switch outcome {
					case "commit":
						routineExec(t, p, b, owner, "COMMIT")
					case "rollback":
						routineExec(t, p, b, owner, "ROLLBACK")
					case "cancel":
						var canceled bool
						if b.PG {
							if err := other.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pidWaiter).Scan(&canceled); err != nil || !canceled {
								t.Fatal("cancel", canceled, err)
							}
						} else {
							cancel()
						}
					}
					o := join()
					joined = true
					if outcome == "rollback" {
						if o.Err != nil {
							t.Fatal(o.Err)
						}
						if n, err := o.Result.RowsAffected(); err != nil || n != 0 {
							t.Fatal(n, err)
						}
						routineExec(t, p, b, waiter, "COMMIT")
					} else {
						d := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
						if outcome == "timeout" {
							d = fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
						}
						if outcome == "commit" {
							constraint, table, keys, values := "pg_proc_proname_args_nsp_index", "pg_proc", "proname, proargtypes, pronamespace", fmt.Sprintf("txr_race, 23, %d", ns)
							if family == "operator" {
								constraint, table, keys, values = "pg_operator_oprname_l_r_n_index", "pg_operator", "oprname, oprleft, oprright, oprnamespace", fmt.Sprintf("#~#, 23, 23, %d", ns)
							}
							d = fixture.ControlsReferenceDiagnostic("23505", "duplicate key value violates unique constraint \""+constraint+"\"")
							d.Schema, d.Table, d.Constraint, d.Detail = "pg_catalog", table, constraint, "Key ("+keys+")=("+values+") already exists."
						}
						if b.PG && outcome != "commit" {
							index := "pg_proc_proname_args_nsp_index"
							if family == "operator" {
								index = "pg_operator_oprname_l_r_n_index"
							}
							got := fixture.ExtractDiagnostic(t, o.Err)
							if !regexp.MustCompile(`^while inserting index tuple \([0-9]+,[1-9][0-9]*\) in relation "` + index + `"$`).MatchString(got.Context) {
								t.Fatal("physical index tuple context", got.Context)
							}
							d.Context = got.Context
						}
						if !b.PG && outcome == "cancel" {
							if o.Err != context.Canceled {
								t.Fatal("native source cancellation", o.Err)
							}
						} else {
							fixture.AssertDiagnostic(t, o.Err, &d)
						}
						routineExec(t, p, b, waiter, "ROLLBACK")
					}
					if outcome == "cancel" || outcome == "timeout" {
						routineExec(t, p, b, owner, "ROLLBACK")
						routineExec(t, p, b, waiter, q)
					}
					routineValueWithNativeNullability(t, p, b, waiter, call, "v", "INT4", int64(5), family == "aggregate", family == "aggregate")
					transactionalRoutineCount(t, p, b, other, "SELECT count(*) AS v FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='txr_unrelated'", 1)
				})
			})
		}
	}
}
