package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func movingSeedEnvironment(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		movingSetup(t, p, b, c)
		lifecycleExec(t, p, b, c,
			"SET DateStyle TO 'ISO, MDY'",
			"CREATE FUNCTION ma_date_add(date,integer) RETURNS date LANGUAGE SQL AS $$SELECT $1+$2$$",
			"CREATE FUNCTION ma_date_sub(date,integer) RETURNS date LANGUAGE SQL AS $$SELECT $1-$2$$",
			"CREATE FUNCTION ma_date_final(date) RETURNS text LANGUAGE SQL AS $$SELECT $1::text$$",
			"CREATE AGGREGATE ma_date(integer) (SFUNC=ma_date_add,STYPE=date,INITCOND='2000-01-01',FINALFUNC=ma_date_final,MSFUNC=ma_date_add,MINVFUNC=ma_date_sub,MSTYPE=date,MINITCOND='01/02/2024',MFINALFUNC=ma_date_final)")
		defer c.ExecContext(p.Ctx, "RESET DateStyle")
		const q = "SELECT id,ma_date(x) OVER(ORDER BY id ROWS CURRENT ROW) AS v FROM ma_input ORDER BY id"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			style  string
			values []string
		}{
			{"ISO, MDY", []string{"2024-01-04", "2024-01-05", "2024-01-07", "2024-01-09"}},
			{"ISO, DMY", []string{"2024-02-03", "2024-02-04", "2024-02-06", "2024-02-08"}},
			{"ISO, MDY", []string{"2024-01-04", "2024-01-05", "2024-01-07", "2024-01-09"}},
		} {
			lifecycleExec(t, p, b, c, "SET DateStyle TO '"+tc.style+"'")
			rows := make([][]any, len(tc.values))
			for i, v := range tc.values {
				rows[i] = []any{int64(i + 1), v}
			}
			s := fixture.Select(q, []string{"id", "v"}, []string{"INT4", "TEXT"}, rows)
			s.CheckMetadata = aggregateFinalMetadata(b.PG, true)
			createAggregateBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT a.aggminitval FROM (SELECT oid FROM pg_proc WHERE proname='ma_date') p JOIN pg_aggregate a ON a.aggfnoid=p.oid", []string{"aggminitval"}, []string{"TEXT"}, [][]any{{"01/02/2024"}}))
	})
}

func movingMultipleRemoval(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		movingLogSetup(t, p, b, c, true)
		createAggregateRun(t, p, b, c, fixture.Exec("INSERT INTO ma_input VALUES(5,11),(6,13)", 2))
		// Peer groups (ids 1/2, 3/4, 5/6) overlap by one group.
		// When the third group arrives inverse(2) punts, so inverse(3)
		// must not be attempted before replay of the new two-group frame.
		for _, prepared := range []bool{false, true} {
			s := movingRows("SELECT id,ma_logged(x) OVER(ORDER BY (id-1)/2 GROUPS 1 PRECEDING) AS v FROM ma_input ORDER BY id", int64(5), int64(5), int64(17), int64(17), int64(36), int64(36))
			s.Prepared = prepared
			createAggregateRun(t, p, b, c, s)
			movingLog(t, p, b, c, [][]any{{"f", int64(2)}, {"f", int64(3)}, {"f", int64(5)}, {"f", int64(7)}, {"i", int64(2)}, {"f", int64(5)}, {"f", int64(7)}, {"f", int64(11)}, {"f", int64(13)}})
		}
	})
}

func movingEmptyFinal(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		movingSetup(t, p, b, c)
		lifecycleExec(t, p, b, c,
			"CREATE TABLE ma_final_log(v integer)",
			"CREATE FUNCTION ma_empty_final(integer) RETURNS integer BEGIN ATOMIC INSERT INTO ma_final_log VALUES($1); SELECT coalesce($1,-7); END",
			"CREATE AGGREGATE ma_empty(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,MSFUNC=pg_catalog.int4pl,MINVFUNC=pg_catalog.int4mi,MSTYPE=int4,MFINALFUNC=ma_empty_final)",
			"CREATE AGGREGATE ma_skipped(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,MSFUNC=pg_catalog.int4pl,MINVFUNC=pg_catalog.int4mi,MSTYPE=int4,MFINALFUNC=pg_catalog.abs)")
		for _, prepared := range []bool{false, true} {
			s := movingRows("SELECT id,ma_empty(x) OVER(ORDER BY id ROWS BETWEEN 1 PRECEDING AND 2 PRECEDING) AS v FROM ma_input ORDER BY id", int64(-7), int64(-7), int64(-7), int64(-7))
			s.Prepared = prepared
			createAggregateRun(t, p, b, c, s)
			// Distinct empty frames may reuse a final; all recorded values
			// must be NULL, and a body must actually have been reached.
			createAggregateRun(t, p, b, c, fixture.Select("SELECT count(*)>0 AS reached,count(v)=0 AS nulls FROM ma_final_log", []string{"reached", "nulls"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
			if _, err := c.ExecContext(p.Ctx, "DELETE FROM ma_final_log"); err != nil {
				t.Fatal(err)
			}
			s = movingRows("SELECT id,ma_skipped(x) OVER(ORDER BY id ROWS BETWEEN 1 PRECEDING AND 2 PRECEDING) AS v FROM ma_input ORDER BY id", nil, nil, nil, nil)
			s.Prepared = prepared
			createAggregateRun(t, p, b, c, s)
			createAggregateRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM ma_final_log", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
	})
}

func movingStateDependency(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		movingSetup(t, p, b, c)
		lifecycleExec(t, p, b, c,
			"CREATE DOMAIN ma_state AS integer",
			"CREATE FUNCTION ma_domain_forward(ma_state,integer) RETURNS ma_state LANGUAGE SQL AS $$SELECT ($1::integer+$2)::ma_state$$",
			"CREATE FUNCTION ma_domain_inverse(ma_state,integer) RETURNS ma_state LANGUAGE SQL AS $$SELECT ($1::integer-$2)::ma_state$$",
			"CREATE FUNCTION ma_domain_final(ma_state) RETURNS integer LANGUAGE SQL AS $$SELECT $1::integer$$",
			"CREATE AGGREGATE ma_owned(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',MSFUNC=ma_domain_forward,MINVFUNC=ma_domain_inverse,MSTYPE=ma_state,MINITCOND='0',MFINALFUNC=ma_domain_final)")
		movingDomainEdges(t, p, c, b.Recorder, "ma_owned")
		d := fixture.Diagnostic{Code: "2BP01", Message: "cannot drop type ma_state because other objects depend on it", Detail: "function ma_domain_forward(ma_state,integer) depends on type ma_state\nfunction ma_domain_inverse(ma_state,integer) depends on type ma_state\nfunction ma_domain_final(ma_state) depends on type ma_state\nfunction ma_owned(integer) depends on function ma_domain_final(ma_state)", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
		createAggregateRun(t, p, b, c, fixture.ImplicitError("DROP DOMAIN ma_state", d))
		createAggregateBoth(t, p, b, c, movingRows("SELECT id,ma_owned(x) OVER(ORDER BY id ROWS 1 PRECEDING) AS v FROM ma_input ORDER BY id", int64(2), int64(5), int64(8), int64(12)))
		lifecycleExec(t, p, b, c, "DROP DOMAIN ma_state CASCADE")
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE proname IN('ma_owned','ma_domain_forward','ma_domain_inverse','ma_domain_final')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}

func movingWindowIdentity(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "ma_hidden")
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		lifecycleExec(t, p, b, c,
			"CREATE SCHEMA ma_hidden",
			"CREATE FUNCTION ma_hidden.step(integer,integer,text) RETURNS integer LANGUAGE SQL AS $$SELECT $1+$2$$",
			"CREATE FUNCTION ma_hidden.inverse(integer,integer,text) RETURNS integer LANGUAGE SQL AS $$SELECT $1-$2$$",
			"CREATE AGGREGATE ma_hidden.\"Moving Odd\"(integer,text) (SFUNC=ma_hidden.step,STYPE=int4,INITCOND='0',MSFUNC=ma_hidden.step,MINVFUNC=ma_hidden.inverse,MSTYPE=int4,MINITCOND='0',MFINALFUNC_MODIFY=shareable)")
		const q = "SELECT ma_hidden.\"Moving Odd\"(x,'label') OVER(ORDER BY x ROWS 1 PRECEDING) AS v FROM(VALUES(1)) s(x) WHERE $1::boolean"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, populated := range []bool{true, false} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "aggregate function ma_hidden.\"Moving Odd\"(integer,text) does not support use as a window function"}, populated)
			s.SelectRows, s.ErrorStage = true, "execute"
			createAggregateBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
	})
}

// PostgreSQL retains the state type through the selected support declarations,
// rather than publishing a redundant aggregate-to-state pg_depend row.
// This is a graph-set probe: SQL-standard bodies can repeat a declaration's
// state-type dependency in PostgreSQL. Physical row multiplicity is not an
// additional aggregate-to-support-to-domain path.
func movingDomainEdges(t *testing.T, p *fixture.Pair, db fixture.Executor, recorder *fixture.Recorder, aggregate string) {
	t.Helper()
	s := fixture.Select("SELECT DISTINCT s.proname,d.deptype::text AS kind FROM (SELECT oid FROM pg_proc WHERE proname=$1) p JOIN pg_depend a ON a.objid=p.oid JOIN pg_proc s ON s.oid=a.refobjid JOIN pg_depend d ON d.objid=s.oid WHERE a.classid='pg_proc'::regclass AND a.refclassid='pg_proc'::regclass AND d.classid='pg_proc'::regclass AND d.refclassid='pg_type'::regclass AND d.refobjid='ma_state'::regtype ORDER BY s.proname", []string{"proname", "kind"}, []string{"NAME", "TEXT"}, [][]any{{"ma_domain_final", "n"}, {"ma_domain_forward", "n"}, {"ma_domain_inverse", "n"}}, aggregate)
	fixture.RunStep(t, p.Ctx, db, recorder, s)
	fixture.RunStep(t, p.Ctx, db, recorder, fixture.Prepared(s))
}
