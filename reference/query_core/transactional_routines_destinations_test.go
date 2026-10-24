package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func transactionalRoutineDestinationConflicts(t *testing.T) {
	for _, family := range []string{"function-rename", "function-move", "aggregate-rename", "aggregate-move", "operator-move"} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/commit=%v", family, commit), func(t *testing.T) {
				fixture.SchemaReferenceCleanup(t, "txr_src", "txr_dest")
				routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					peer, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					other, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer other.Close()
					routineExec(t, p, b, c, "CREATE SCHEMA txr_src", "CREATE SCHEMA txr_dest")
					seed, create, alter, call, schema, object := "CREATE FUNCTION public.txr_source(integer) RETURNS integer RETURN $1+1", "CREATE FUNCTION public.txr_taken(integer) RETURNS integer RETURN $1+100", "ALTER FUNCTION public.txr_source(integer) RENAME TO txr_taken", "SELECT public.txr_taken(4) AS v", "public", "txr_taken"
					switch family {
					case "function-move":
						seed, create, alter, call, schema = "CREATE FUNCTION txr_src.txr_taken(integer) RETURNS integer RETURN $1+1", "CREATE FUNCTION txr_dest.txr_taken(integer) RETURNS integer RETURN $1+100", "ALTER FUNCTION txr_src.txr_taken(integer) SET SCHEMA txr_dest", "SELECT txr_dest.txr_taken(4) AS v", "txr_dest"
					case "aggregate-rename":
						seed, create, alter, call = "CREATE AGGREGATE public.txr_source(integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')", "CREATE FUNCTION public.txr_taken(integer) RETURNS integer RETURN $1+100", "ALTER FUNCTION public.txr_source(integer) RENAME TO txr_taken", "SELECT public.txr_taken(v) AS v FROM(VALUES(2),(3)) s(v)"
					case "aggregate-move":
						seed, create, alter, call, schema = "CREATE AGGREGATE txr_src.txr_taken(integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')", "CREATE FUNCTION txr_dest.txr_taken(integer) RETURNS integer RETURN $1+100", "ALTER FUNCTION txr_src.txr_taken(integer) SET SCHEMA txr_dest", "SELECT txr_dest.txr_taken(v) AS v FROM(VALUES(2),(3)) s(v)", "txr_dest"
					case "operator-move":
						seed, create, alter, call, schema, object = "CREATE OPERATOR txr_src.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4pl)", "CREATE OPERATOR txr_dest.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4mi)", "ALTER OPERATOR txr_src.#~# (integer,integer) SET SCHEMA txr_dest", "SELECT 2 OPERATOR(txr_dest.#~#) 3 AS v", "txr_dest", "#~#"
					}
					routineExec(t, p, b, c, seed)
					ns := transactionalRoutineOID(t, p, c, "SELECT '"+schema+"'::regnamespace::oid")
					pidC, pidP := 0, 0
					if b.PG {
						pidC, pidP = fixture.LockPID(t, p.Ctx, c), fixture.LockPID(t, p.Ctx, peer)
					}
					routineExec(t, p, b, c, "BEGIN", create)
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					routineExec(t, p, b, peer, "BEGIN")
					defer peer.ExecContext(p.Ctx, "ROLLBACK")
					stmt, err := peer.PrepareContext(p.Ctx, alter)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					ctx, cancel := context.WithCancel(p.Ctx)
					defer cancel()
					finished := make(chan struct{})
					join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
						defer close(finished)
						r, err := stmt.ExecContext(ctx)
						return fixture.RelationOutcome{Result: r, Err: err}
					})
					joined := false
					defer func() {
						cancel()
						if !joined {
							join()
						}
					}()
					fixture.RelationLockPending(t, p, b, pidP, pidC, 0, "", finished)
					routineExec(t, p, b, other, "CREATE FUNCTION "+schema+".txr_taken(bigint) RETURNS bigint RETURN 108::bigint")
					end := "ROLLBACK"
					if commit {
						end = "COMMIT"
					}
					routineExec(t, p, b, c, end)
					o := join()
					joined = true
					if commit {
						index, table, keys, values := "pg_proc_proname_args_nsp_index", "pg_proc", "proname, proargtypes, pronamespace", fmt.Sprintf("%s, 23, %d", object, ns)
						if family == "operator-move" {
							index, table, keys, values = "pg_operator_oprname_l_r_n_index", "pg_operator", "oprname, oprleft, oprright, oprnamespace", fmt.Sprintf("#~#, 23, 23, %d", ns)
						}
						d := fixture.ControlsReferenceDiagnostic("23505", "duplicate key value violates unique constraint \""+index+"\"")
						d.Schema, d.Table, d.Constraint, d.Detail = "pg_catalog", table, index, "Key ("+keys+")=("+values+") already exists."
						fixture.AssertDiagnostic(t, o.Err, &d)
						routineExec(t, p, b, peer, "ROLLBACK")
					} else {
						if o.Err != nil {
							t.Fatal(o.Err)
						}
						if n, err := o.Result.RowsAffected(); err != nil || n != 0 {
							t.Fatal(n, err)
						}
						routineExec(t, p, b, peer, "COMMIT")
						routineValueWithNativeNullability(t, p, b, other, call, "v", "INT4", int64(5), family == "aggregate-rename" || family == "aggregate-move", family == "aggregate-rename" || family == "aggregate-move")
					}
					routineValue(t, p, b, peer, "SELECT "+schema+".txr_taken(1::bigint) AS v", "v", "INT8", int64(108))
				})
			})
		}
	}
}
