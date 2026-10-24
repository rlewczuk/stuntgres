package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalRoutineOperators(t *testing.T) {
	transactionalRoutineRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, e fixture.Executor, exec func(string), value func(string, string, int64), finish func(), explicit, commit bool) {
		create := "CREATE OPERATOR public.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4pl)"
		exec(create)
		value("SELECT 2 OPERATOR(public.#~#) 3 AS v", "INT4", 5)
		exec("ALTER OPERATOR public.#~# (int4,int4) SET (RESTRICT=NONE)")
		value("SELECT 2 OPERATOR(public.#~#) 3 AS v", "INT4", 5)
		if explicit {
			exec("SAVEPOINT op_mark")
			exec("DROP OPERATOR public.#~# (int4,int4)")
			transactionalRoutineCount(t, p, b, e, "SELECT count(*) AS v FROM pg_operator WHERE oprnamespace='public'::regnamespace AND oprname='#~#'", 0)
			exec("ROLLBACK TO op_mark")
			value("SELECT 2 OPERATOR(public.#~#) 3 AS v", "INT4", 5)
			exec("RELEASE op_mark")
		} else {
			exec("DROP OPERATOR public.#~# (int4,int4)")
			exec(create)
		}
		// The support program and unary NONE signature are genuinely private too.
		exec("CREATE FUNCTION txr_op_step(integer,integer) RETURNS integer RETURN $1-$2")
		exec("CREATE OPERATOR public.#?# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=txr_op_step)")
		exec("CREATE OPERATOR public.#?# (RIGHTARG=int4,FUNCTION=pg_catalog.int4um)")
		value("SELECT 2 OPERATOR(public.#?#) 3 AS v", "INT4", -1)
		value("SELECT OPERATOR(public.#?#) 3 AS v", "INT4", -3)
		exec("CREATE SCHEMA txr_dest")
		exec("ALTER OPERATOR public.#~# (integer,integer) SET SCHEMA txr_dest")
		exec("ALTER OPERATOR txr_dest.#~# (integer,integer) OWNER TO CURRENT_USER")
		value("SELECT 2 OPERATOR(txr_dest.#~#) 3 AS v", "INT4", 5)
		exec("ALTER OPERATOR txr_dest.#~# (integer,integer) SET SCHEMA public")
		exec("CREATE OPERATOR public.#=# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4eq,COMMUTATOR=OPERATOR(public.#=#),NEGATOR=OPERATOR(public.#!#),HASHES,MERGES)")
		shell := transactionalRoutineOID(t, p, e, "SELECT 'public.#!#(integer,integer)'::regoperator::oid")
		if explicit {
			exec("SAVEPOINT shell_completion")
		}
		exec("CREATE OPERATOR public.#!# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4ne,NEGATOR=OPERATOR(public.#=#))")
		if completed := transactionalRoutineOID(t, p, e, "SELECT 'public.#!#(integer,integer)'::regoperator::oid"); completed != shell {
			t.Fatal("shell completion changed identity", shell, completed)
		}
		value("SELECT CASE WHEN 2 OPERATOR(public.#!#) 3 THEN 1 ELSE 0 END AS v", "INT4", 1)
		fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT oprnegate='public.#=#(integer,integer)'::regoperator::oid AS linked FROM pg_operator WHERE oid='public.#!#(integer,integer)'::regoperator", []string{"linked"}, []string{"BOOL"}, [][]any{{true}}))
		if explicit {
			exec("ROLLBACK TO shell_completion")
			transactionalRoutineCount(t, p, b, e, "SELECT count(*) AS v FROM pg_operator WHERE oid='public.#!#(integer,integer)'::regoperator AND oprcode::oid=0 AND oprresult=0", 1)
			exec("RELEASE shell_completion")
			exec("CREATE OPERATOR public.#!# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4ne,NEGATOR=OPERATOR(public.#=#))")
		}
		exec("ALTER OPERATOR public.#=# (integer,integer) SET (RESTRICT=eqsel,JOIN=eqjoinsel,HASHES,MERGES)")
		transactionalRoutineCount(t, p, b, e, "SELECT count(*) AS v FROM pg_operator WHERE oid='public.#=#(integer,integer)'::regoperator AND oprcanhash AND oprcanmerge AND oprcom=oid AND oprrest::oid=101 AND oprjoin::oid=105", 1)
		want := int64(5)
		if explicit {
			want = 0
		}
		q := "SELECT count(*) AS v FROM pg_operator WHERE oprnamespace='public'::regnamespace AND oprname IN ('#~#','#?#','#=#','#!#')"
		transactionalRoutineCount(t, p, b, peer, q, want)
		finish()
		want = 0
		if commit {
			want = 5
		}
		transactionalRoutineCount(t, p, b, peer, q, want)
		if commit {
			routineValue(t, p, b, peer, "SELECT 2 OPERATOR(public.#~#) 3 AS v", "v", "INT4", int64(5))
		}
	})
}

func TestReferenceTransactionalRoutineAggregates(t *testing.T) {
	transactionalRoutineRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, e fixture.Executor, exec func(string), value func(string, string, int64), finish func(), explicit, commit bool) {
		exec("CREATE AGGREGATE public.txr_sum(integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0',SSPACE=4)")
		value("SELECT public.txr_sum(v) AS v FROM (VALUES(1),(2),(3)) s(v)", "INT4", 6)
		value("SELECT public.txr_sum(v) AS v FROM (VALUES(1)) s(v) WHERE false", "INT4", 0)
		value("SELECT public.txr_sum(v) AS v FROM (VALUES(NULL::integer),(NULL::integer)) s(v)", "INT4", 0)
		transactionalRoutineCount(t, p, b, e, "SELECT count(*) AS v FROM pg_proc p JOIN pg_aggregate a ON a.aggfnoid=p.oid WHERE p.pronamespace='public'::regnamespace AND p.proname='txr_sum' AND p.prokind='a' AND a.aggtranstype='integer'::regtype AND a.aggtransfn='pg_catalog.int4pl(integer,integer)'::regprocedure", 1)
		if explicit {
			exec("SAVEPOINT agg_mark")
		}
		exec("ALTER FUNCTION public.txr_sum(integer) RENAME TO txr_sum_new")
		value("SELECT public.txr_sum_new(v) AS v FROM (VALUES(1),(2),(3)) s(v)", "INT4", 6)
		exec("CREATE SCHEMA txr_dest")
		exec("ALTER ROUTINE public.txr_sum_new(integer) SET SCHEMA txr_dest")
		exec("ALTER FUNCTION txr_dest.txr_sum_new(integer) OWNER TO CURRENT_USER")
		value("SELECT txr_dest.txr_sum_new(v) AS v FROM (VALUES(1),(2),(3)) s(v)", "INT4", 6)
		exec("ALTER ROUTINE txr_dest.txr_sum_new(integer) SET SCHEMA public")
		if explicit {
			exec("ROLLBACK TO agg_mark")
			value("SELECT public.txr_sum(v) AS v FROM (VALUES(1),(2),(3)) s(v)", "INT4", 6)
			exec("RELEASE agg_mark")
		} else {
			exec("ALTER FUNCTION public.txr_sum_new(integer) RENAME TO txr_sum")
		}
		exec("CREATE FUNCTION txr_sum_step(integer,integer) RETURNS integer RETURN $1+$2")
		exec("CREATE AGGREGATE txr_private_sum(integer) (SFUNC=txr_sum_step,STYPE=integer,INITCOND='0')")
		value("SELECT txr_private_sum(v) AS v FROM (VALUES(1),(2),(3)) s(v)", "INT4", 6)
		q := "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('txr_sum','txr_private_sum','txr_sum_step')"
		want := int64(3)
		if explicit {
			want = 0
		}
		transactionalRoutineCount(t, p, b, peer, q, want)
		finish()
		want = 0
		if commit {
			want = 3
		}
		transactionalRoutineCount(t, p, b, peer, q, want)
		if commit {
			routineValueWithNativeNullability(t, p, b, peer, "SELECT txr_sum(v) AS v FROM (VALUES(1),(2),(3)) s(v)", "v", "INT4", int64(6), true, true)
		}
	}, transactionalRoutineRouteOptions{AggregateMetadata: true})
}
