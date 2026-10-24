package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalRoutinesLateDependencies(t *testing.T) {
	for _, family := range []string{"hard", "default", "aggregate", "operator"} {
		for _, commit := range []bool{false, true} {
			name := "rollback"
			if commit {
				name = "commit"
			}
			t.Run(family+"/retirement-first/"+name, func(t *testing.T) {
				routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, owner *sql.Conn) {
					peer, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					q, call := transactionalRoutineDependent(family)
					pidOwner, pidPeer := 0, 0
					if b.PG {
						pidOwner, pidPeer = fixture.LockPID(t, p.Ctx, owner), fixture.LockPID(t, p.Ctx, peer)
					}
					routineExec(t, p, b, owner, "CREATE FUNCTION txr_provider(integer,integer) RETURNS integer RETURN $1+$2")
					provider := transactionalRoutineOID(t, p, owner, "SELECT 'txr_provider(integer,integer)'::regprocedure::oid")
					routineExec(t, p, b, owner, "BEGIN", "DROP FUNCTION txr_provider(integer,integer)")
					defer owner.ExecContext(p.Ctx, "ROLLBACK")
					routineExec(t, p, b, peer, "BEGIN")
					defer peer.ExecContext(p.Ctx, "ROLLBACK")
					ctx, cancel := context.WithCancel(p.Ctx)
					defer cancel()
					finished := make(chan struct{})
					join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
						defer close(finished)
						r, err := peer.ExecContext(ctx, q)
						return fixture.RelationOutcome{Result: r, Err: err}
					})
					joined := false
					defer func() {
						cancel()
						if !joined {
							join()
						}
					}()
					transactionalRoutineObjectWait(t, p, b, pidPeer, pidOwner, 1255, provider, finished)
					end := "ROLLBACK"
					if commit {
						end = "COMMIT"
					}
					routineExec(t, p, b, owner, end)
					o := join()
					joined = true
					if commit {
						fixture.AssertDiagnostic(t, o.Err, new(fixture.ControlsReferenceDiagnostic("42704", "referenced function was concurrently dropped")))
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
					} else {
						if o.Err != nil {
							t.Fatal(o.Err)
						}
						if n, err := o.Result.RowsAffected(); err != nil || n != 0 {
							t.Fatal(n, err)
						}
					}
					routineExec(t, p, b, peer, "COMMIT")
					want := int64(1)
					if commit {
						want = 0
					}
					transactionalRoutineCount(t, p, b, owner, "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='txr_provider'", want)
					probe := "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='txr_dependent'"
					if family == "operator" {
						probe = "SELECT count(*) AS v FROM pg_operator WHERE oprnamespace='public'::regnamespace AND oprname='#~#'"
					}
					transactionalRoutineCount(t, p, b, owner, probe, want)
					if !commit {
						routineValueWithNativeNullability(t, p, b, owner, call, "v", "INT4", int64(5), family == "aggregate", family == "aggregate")
					} else {
						message, hint, pos := "function txr_dependent() does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", "8"
						if family == "aggregate" {
							message = "function txr_dependent(integer) does not exist"
						}
						if family == "operator" {
							message, hint, pos = "operator does not exist: integer public.#~# integer", "No operator matches the given name and argument types. You might need to add explicit type casts.", "10"
						}
						d := fixture.ControlsReferenceDiagnostic("42883", message)
						d.Hint, d.Position = hint, pos
						s := fixture.ErrorDiagnostic(call, d)
						s.SelectRows = true
						fixture.RunStep(t, p.Ctx, owner, b.Recorder, s)
					}
				})
			})
		}
		t.Run(family+"/dependent-first", func(t *testing.T) {
			routineDropScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
				q, call := transactionalRoutineDependent(family)
				routineExec(t, p, b, c, "CREATE FUNCTION txr_provider(integer,integer) RETURNS integer RETURN $1+$2", "BEGIN")
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				peer, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				routineExec(t, p, b, peer, q)
				routineExec(t, p, b, c, "SAVEPOINT retirement", "DROP FUNCTION txr_provider(integer,integer) CASCADE")
				dependent := "function txr_dependent()"
				if family == "default" {
					dependent = "function txr_dependent(integer)"
				}
				if family == "aggregate" {
					dependent = "function txr_dependent(integer)"
				}
				if family == "operator" {
					dependent = "operator #~#(integer,integer)"
				}
				routineDropNotices(t, notices, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to " + dependent})
				routineExec(t, p, b, c, "ROLLBACK TO retirement", "RELEASE retirement", "COMMIT")
				routineValueWithNativeNullability(t, p, b, peer, call, "v", "INT4", int64(5), family == "aggregate", family == "aggregate")
			})
		})
	}
}

func transactionalRoutineDependent(family string) (string, string) {
	switch family {
	case "hard":
		return "CREATE FUNCTION txr_dependent() RETURNS integer RETURN txr_provider(2,3)", "SELECT txr_dependent() AS v"
	case "default":
		return "CREATE FUNCTION txr_dependent(x integer DEFAULT txr_provider(2,3)) RETURNS integer RETURN x", "SELECT txr_dependent() AS v"
	case "aggregate":
		return "CREATE AGGREGATE txr_dependent(integer) (SFUNC=txr_provider,STYPE=integer,INITCOND='0')", "SELECT txr_dependent(v) AS v FROM(VALUES(2),(3)) s(v)"
	default:
		return "CREATE OPERATOR public.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=txr_provider)", "SELECT 2 OPERATOR(public.#~#) 3 AS v"
	}
}
