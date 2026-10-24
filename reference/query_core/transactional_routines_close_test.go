package reference_test

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalRoutinesPhysicalClose(t *testing.T) {
	for _, resource := range []string{"procedure-name", "operator-name", "provider-retirement", "provider-definition"} {
		t.Run(resource, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					// Use the incumbent physical-close convention: with no idle
					// slots, Conn.Close terminates the underlying driver session.
					b.DB.SetMaxIdleConns(0)
					owner, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					observer, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer observer.Close()
					ownerPID, peerPID := 0, 0
					if b.PG {
						ownerPID, peerPID = fixture.RowPIDs(t, p.Ctx, owner, peer)
					}
					routineExec(t, p, b, observer,
						"CREATE TABLE txc_keep_rows(v integer)",
						"CREATE FUNCTION txc_keep(integer) RETURNS integer RETURN $1+1",
						"CREATE FUNCTION txc_keep_step(integer,integer) RETURNS integer RETURN $1+$2",
						"CREATE AGGREGATE txc_keep_sum(integer) (SFUNC=txc_keep_step,STYPE=integer,INITCOND='0')",
						"CREATE OPERATOR public.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=txc_keep_step)",
						"CREATE FUNCTION txc_provider(integer,integer) RETURNS integer RETURN $1+$2")
					fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Exec("INSERT INTO txc_keep_rows VALUES(7),(9)", 2))
					provider := transactionalRoutineOID(t, p, observer, "SELECT 'txc_provider(integer,integer)'::regprocedure::oid")
					keep := transactionalRoutineOID(t, p, observer, "SELECT 'txc_keep(integer)'::regprocedure::oid")
					keepAggregate := transactionalRoutineOID(t, p, observer, "SELECT 'txc_keep_sum(integer)'::regprocedure::oid")
					keepOperator := transactionalRoutineOID(t, p, observer, "SELECT 'public.#~#(integer,integer)'::regoperator::oid")

					routineExec(t, p, b, owner, "BEGIN",
						"CREATE TABLE txc_private_rows(v integer)",
						"CREATE FUNCTION txc_private_step(integer,integer) RETURNS integer RETURN $1+$2",
						"CREATE FUNCTION txc_private(integer) RETURNS integer RETURN txc_private_step($1,1)",
						"CREATE AGGREGATE txc_private_sum(integer) (SFUNC=txc_private_step,STYPE=integer,INITCOND='0')",
						"CREATE OPERATOR public.#?# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=txc_private_step)")
					fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("INSERT INTO txc_private_rows VALUES(99)", 1))
					routineValue(t, p, b, owner, "SELECT txc_private(4) AS v", "v", "INT4", int64(5))
					routineValue(t, p, b, owner, "SELECT 2 OPERATOR(public.#?#) 3 AS v", "v", "INT4", int64(5))
					routineValueWithNativeNullability(t, p, b, owner, "SELECT txc_private_sum(v) AS v FROM(VALUES(1),(2),(3)) s(v)", "v", "INT4", int64(6), true, true)
					privateStep := transactionalRoutineOID(t, p, owner, "SELECT 'txc_private_step(integer,integer)'::regprocedure::oid")
					privateCall := transactionalRoutineOID(t, p, owner, "SELECT 'txc_private(integer)'::regprocedure::oid")
					privateAggregate := transactionalRoutineOID(t, p, owner, "SELECT 'txc_private_sum(integer)'::regprocedure::oid")
					privateOperator := transactionalRoutineOID(t, p, owner, "SELECT 'public.#?#(integer,integer)'::regoperator::oid")
					privateEdges := fmt.Sprintf("SELECT count(*) AS v FROM pg_depend WHERE (classid=1255 AND objid IN (%d::oid,%d::oid,%d::oid)) OR (classid=2617 AND objid=%d::oid)", privateStep, privateCall, privateAggregate, privateOperator)
					privateRows := "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('txc_private_step','txc_private','txc_private_sum','txc_using_provider')"
					transactionalRoutineCount(t, p, b, observer, privateRows, 0)
					transactionalRoutineCount(t, p, b, observer, privateEdges, 0)
					transactionalRoutineCount(t, p, b, owner, privateRows, 3)
					transactionalRoutineCount(t, p, b, owner, fmt.Sprintf("SELECT count(*) AS v FROM pg_depend WHERE refclassid=1255 AND refobjid=%d::oid AND ((classid=1255 AND objid IN (%d::oid,%d::oid)) OR (classid=2617 AND objid=%d::oid))", privateStep, privateCall, privateAggregate, privateOperator), 3)

					query := "CREATE FUNCTION txc_claim(integer) RETURNS integer RETURN $1+1"
					independent := "CREATE FUNCTION txc_claim(bigint) RETURNS bigint RETURN 108::bigint"
					independentCall := "SELECT txc_claim(1::bigint) AS v"
					independentType, independentValue := "INT8", int64(108)
					var privateClaim int64
					switch resource {
					case "procedure-name":
						routineExec(t, p, b, owner, "CREATE FUNCTION txc_claim(integer) RETURNS integer RETURN $1+100")
						privateClaim = transactionalRoutineOID(t, p, owner, "SELECT 'txc_claim(integer)'::regprocedure::oid")
						routineValue(t, p, b, owner, "SELECT txc_claim(4) AS v", "v", "INT4", int64(104))
					case "operator-name":
						routineExec(t, p, b, owner, "CREATE OPERATOR public.#@# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4mi)")
						privateClaim = transactionalRoutineOID(t, p, owner, "SELECT 'public.#@#(integer,integer)'::regoperator::oid")
						routineValue(t, p, b, owner, "SELECT 2 OPERATOR(public.#@#) 3 AS v", "v", "INT4", int64(-1))
						query = "CREATE OPERATOR public.#@# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4pl)"
						independent = "CREATE OPERATOR public.#@# (RIGHTARG=int4,FUNCTION=pg_catalog.int4um)"
						independentCall, independentType, independentValue = "SELECT OPERATOR(public.#@#) 3 AS v", "INT4", -3
					case "provider-retirement":
						routineExec(t, p, b, owner, "DROP FUNCTION txc_provider(integer,integer)")
						query = "CREATE FUNCTION txc_peer() RETURNS integer RETURN txc_provider(2,3)"
						independent = "CREATE FUNCTION txc_provider(bigint,bigint) RETURNS bigint RETURN 108::bigint"
						independentCall = "SELECT txc_provider(1::bigint,2::bigint) AS v"
					case "provider-definition":
						routineExec(t, p, b, owner, "CREATE FUNCTION txc_using_provider() RETURNS integer RETURN txc_provider(2,3)")
						routineValue(t, p, b, owner, "SELECT txc_using_provider() AS v", "v", "INT4", int64(5))
						query = "DROP FUNCTION txc_provider(integer,integer)"
						independent = "CREATE FUNCTION txc_provider(bigint,bigint) RETURNS bigint RETURN 108::bigint"
						independentCall = "SELECT txc_provider(1::bigint,2::bigint) AS v"
					}
					statement, err := peer.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer statement.Close()
					routineExec(t, p, b, peer, "BEGIN")
					ctx, cancel := context.WithCancel(p.Ctx)
					finished := make(chan struct{})
					join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
						defer close(finished)
						r, err := statement.ExecContext(ctx)
						return fixture.RelationOutcome{Result: r, Err: err}
					})
					joined := false
					defer func() {
						cancel()
						if !joined {
							join()
						}
					}()
					if resource == "procedure-name" || resource == "operator-name" {
						fixture.RelationLockPending(t, p, b, peerPID, ownerPID, 0, "", finished)
					} else {
						transactionalRoutineObjectWait(t, p, b, peerPID, ownerPID, 1255, provider, finished)
					}
					// A full-key waiter must not become a namespace-wide blockade.
					routineExec(t, p, b, observer, independent)
					routineValue(t, p, b, observer, independentCall, "v", independentType, independentValue)
					// No COMMIT/ROLLBACK is sent to owner. This physically ends the
					// session with its private callable graph and ticket still live.
					if err := owner.Close(); err != nil {
						t.Fatal(err)
					}
					outcome := join()
					joined = true
					if outcome.Err != nil {
						t.Fatal(outcome.Err)
					}
					fixture.LockAffected(t, outcome.Result, 0)
					if resource == "provider-definition" {
						transactionalRoutineCount(t, p, b, peer, fmt.Sprintf("SELECT count(*) AS v FROM pg_proc WHERE oid=%d::oid", provider), 0)
						routineValue(t, p, b, observer, "SELECT txc_provider(2,3) AS v", "v", "INT4", int64(5))
						routineExec(t, p, b, peer, "ROLLBACK")
					} else {
						routineExec(t, p, b, peer, "COMMIT")
					}

					transactionalRoutineCount(t, p, b, peer, privateRows, 0)
					transactionalRoutineCount(t, p, b, peer, privateEdges, 0)
					transactionalRoutineCount(t, p, b, peer, "SELECT count(*) AS v FROM pg_operator WHERE oprnamespace='public'::regnamespace AND oprname='#?#'", 0)
					transactionalRoutineCount(t, p, b, peer, "SELECT count(*) AS v FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='txc_private_rows'", 0)
					for _, absent := range []struct{ query, message, hint, position string }{
						{"SELECT txc_private(4) AS v", "function txc_private(integer) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", "8"},
						{"SELECT txc_private_sum(v) AS v FROM(VALUES(1),(2),(3)) s(v)", "function txc_private_sum(integer) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", "8"},
						{"SELECT 2 OPERATOR(public.#?#) 3 AS v", "operator does not exist: integer public.#?# integer", "No operator matches the given name and argument types. You might need to add explicit type casts.", "10"},
					} {
						d := fixture.ControlsReferenceDiagnostic("42883", absent.message)
						d.Hint, d.Position = absent.hint, absent.position
						s := fixture.ErrorDiagnostic(absent.query, d)
						s.SelectRows = true
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, s)
					}
					for _, identity := range []struct {
						query string
						oid   int64
					}{
						{"SELECT 'txc_provider(integer,integer)'::regprocedure::oid", provider},
						{"SELECT 'txc_keep(integer)'::regprocedure::oid", keep},
						{"SELECT 'txc_keep_sum(integer)'::regprocedure::oid", keepAggregate},
						{"SELECT 'public.#~#(integer,integer)'::regoperator::oid", keepOperator},
					} {
						if after := transactionalRoutineOID(t, p, peer, identity.query); after != identity.oid {
							t.Fatal("physical close changed committed identity", identity.query, identity.oid, after)
						}
					}
					routineValue(t, p, b, peer, "SELECT txc_keep(4) AS v", "v", "INT4", int64(5))
					routineValue(t, p, b, peer, "SELECT 2 OPERATOR(public.#~#) 3 AS v", "v", "INT4", int64(5))
					routineValueWithNativeNullability(t, p, b, peer, "SELECT txc_keep_sum(v) AS v FROM(VALUES(1),(2),(3)) s(v)", "v", "INT4", int64(6), true, true)
					routineValue(t, p, b, peer, "SELECT txc_provider(2,3) AS v", "v", "INT4", int64(5))
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT v FROM txc_keep_rows ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}}))
					routineValue(t, p, b, peer, independentCall, "v", independentType, independentValue)
					switch resource {
					case "procedure-name":
						if after := transactionalRoutineOID(t, p, peer, "SELECT 'txc_claim(integer)'::regprocedure::oid"); after == privateClaim {
							t.Fatal("terminated private declaration reused", after)
						}
						routineValue(t, p, b, peer, "SELECT txc_claim(4) AS v", "v", "INT4", int64(5))
						transactionalRoutineCount(t, p, b, peer, fmt.Sprintf("SELECT count(*) AS v FROM pg_proc WHERE oid=%d::oid", privateClaim), 0)
						transactionalRoutineCount(t, p, b, peer, "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='txc_claim'", 2)
					case "operator-name":
						if after := transactionalRoutineOID(t, p, peer, "SELECT 'public.#@#(integer,integer)'::regoperator::oid"); after == privateClaim {
							t.Fatal("terminated private operator reused", after)
						}
						routineValue(t, p, b, peer, "SELECT 2 OPERATOR(public.#@#) 3 AS v", "v", "INT4", int64(5))
						transactionalRoutineCount(t, p, b, peer, fmt.Sprintf("SELECT count(*) AS v FROM pg_operator WHERE oid=%d::oid", privateClaim), 0)
						transactionalRoutineCount(t, p, b, peer, "SELECT count(*) AS v FROM pg_operator WHERE oprnamespace='public'::regnamespace AND oprname='#@#'", 2)
					case "provider-retirement":
						routineValue(t, p, b, peer, "SELECT txc_peer() AS v", "v", "INT4", int64(5))
						child := transactionalRoutineOID(t, p, peer, "SELECT 'txc_peer()'::regprocedure::oid")
						transactionalRoutineCount(t, p, b, peer, fmt.Sprintf("SELECT count(*) AS v FROM pg_depend WHERE classid=1255 AND objid=%d::oid AND refclassid=1255 AND refobjid=%d::oid AND deptype='n'", child, provider), 1)
					}
				})
			}
		})
	}
}
