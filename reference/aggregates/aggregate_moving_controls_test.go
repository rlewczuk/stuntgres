package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func movingStoredSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	// Persistent supports capture domain casts by identity. Quoted support
	// bodies retain their caller-path lookup contract in the runtime scenarios.
	movingSetup(t, p, b, c)
	lifecycleExec(t, p, b, c,
		"CREATE DOMAIN ma_state AS integer",
		"CREATE FUNCTION ma_domain_forward(ma_state,integer) RETURNS ma_state BEGIN ATOMIC SELECT ($1::integer+$2)::ma_state; END",
		"CREATE FUNCTION ma_domain_inverse(ma_state,integer) RETURNS ma_state BEGIN ATOMIC SELECT ($1::integer-$2)::ma_state; END",
		"CREATE FUNCTION ma_domain_final(ma_state,integer) RETURNS integer BEGIN ATOMIC SELECT CASE WHEN $2 IS NULL THEN $1::integer ELSE -999 END; END",
		"CREATE AGGREGATE ma_domain(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',MSFUNC=ma_domain_forward,MINVFUNC=ma_domain_inverse,MSTYPE=ma_state,MSSPACE=-17,MINITCOND='000',MFINALFUNC=ma_domain_final,MFINALFUNC_EXTRA,MFINALFUNC_MODIFY=read_only)",
		"CREATE VIEW ma_view AS SELECT id,ma_domain(x) OVER(ORDER BY id ROWS 1 PRECEDING) AS v FROM ma_input",
		"CREATE FUNCTION ma_frozen() RETURNS integer BEGIN ATOMIC SELECT v FROM ma_view WHERE id=4; END")
}

func movingStoredCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	movingDomainEdges(t, p, db, recorder, "ma_domain")
	run := func(s fixture.Step) {
		fixture.RunStep(t, p.Ctx, db, recorder, s)
		fixture.RunStep(t, p.Ctx, db, recorder, fixture.Prepared(s))
	}
	for _, q := range []string{
		"SELECT id,ma_domain(x) OVER(ORDER BY id ROWS 1 PRECEDING) AS v FROM ma_input ORDER BY id",
		"SELECT id,v FROM ma_view ORDER BY id",
	} {
		s := movingRows(q, int64(2), int64(5), int64(8), int64(12))
		s.CheckMetadata = movingMetadata(b.PG)
		run(s)
	}
	run(createAggregateValue("SELECT ma_frozen() AS v", int64(12)))
	run(fixture.Select("SELECT p.prorettype=23::oid AS result,a.aggtranstype=23::oid AS ordinary_state,a.aggmtranstype='ma_state'::regtype AS moving_state,a.aggmtransfn='ma_domain_forward(ma_state,integer)'::regprocedure AS forward,a.aggminvtransfn='ma_domain_inverse(ma_state,integer)'::regprocedure AS inverse,a.aggmfinalfn='ma_domain_final(ma_state,integer)'::regprocedure AS final,a.aggmtransspace,a.aggminitval,a.aggmfinalextra,a.aggmfinalmodify::text AS mode FROM (SELECT oid,prorettype FROM pg_proc WHERE proname='ma_domain') p JOIN pg_aggregate a ON a.aggfnoid=p.oid", []string{"result", "ordinary_state", "moving_state", "forward", "inverse", "final", "aggmtransspace", "aggminitval", "aggmfinalextra", "mode"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "INT4", "TEXT", "BOOL", "TEXT"}, [][]any{{true, true, true, true, true, true, int64(-17), "000", true, "r"}}))
	run(fixture.Select("SELECT atttypid=23::oid AS result,atttypmod FROM pg_attribute WHERE attrelid='ma_view'::regclass AND attname='v'", []string{"result", "atttypmod"}, []string{"BOOL", "INT4"}, [][]any{{true, int64(-1)}}))
	for _, edge := range []struct{ class, ref string }{
		{"pg_proc", "'ma_domain_forward(ma_state,integer)'::regprocedure"},
		{"pg_proc", "'ma_domain_inverse(ma_state,integer)'::regprocedure"},
		{"pg_proc", "'ma_domain_final(ma_state,integer)'::regprocedure"},
	} {
		run(fixture.Select("SELECT d.deptype::text AS kind FROM (SELECT oid FROM pg_proc WHERE proname='ma_domain') p JOIN pg_depend d ON d.objid=p.oid WHERE d.classid='pg_proc'::regclass AND d.refclassid='"+edge.class+"'::regclass AND d.refobjid="+edge.ref, []string{"kind"}, []string{"TEXT"}, [][]any{{"n"}}))
	}
}

func TestReferenceAggregateMovingStored(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		movingStoredSetup(t, p, b, c)
		movingStoredCheck(t, p, b, c, b.Recorder)
		const q = "SELECT id,v FROM ma_view WHERE id<=$1 ORDER BY id"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, limit := range []int64{4, 0, 2, 4} {
			values := []any{int64(2), int64(5), int64(8), int64(12)}
			values = values[:int(limit)]
			s := movingRows(q, values...)
			s.Args = []any{limit}
			s.CheckMetadata = movingMetadata(b.PG)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
	})
}

func TestReferenceAggregateMovingPersistence(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ma_persist_owner"})
	fixture.SchemaReferenceCleanup(t, "ma_owned_scope")
	var emitted string
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		lifecycleExec(t, p, b, c, "CREATE ROLE ma_persist_owner")
		defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		defer c.ExecContext(context.Background(), "RESET search_path")
		if b.PG {
			if emitted == "" {
				t.Fatal("missing actual native moving-aggregate Dump stream; run this round-trip after native DDL is implemented")
			}
			if _, err := c.ExecContext(p.Ctx, emitted); err != nil {
				t.Fatal("replay actual moving dump", err)
			}
			lifecycleExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "SET search_path TO ma_owned_scope,public")
		} else {
			lifecycleExec(t, p, b, c, "CREATE SCHEMA ma_owned_scope AUTHORIZATION ma_persist_owner", "SET search_path TO ma_owned_scope,public", "SET SESSION AUTHORIZATION ma_persist_owner")
			movingStoredSetup(t, p, b, c)
			for _, mode := range []string{"read_only", "shareable", "read_write"} {
				lifecycleExec(t, p, b, c,
					"CREATE AGGREGATE ma_persist_"+mode+"(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',MSFUNC=pg_catalog.int4pl,MINVFUNC=pg_catalog.int4mi,MSTYPE=int4,MINITCOND='0',MFINALFUNC_MODIFY="+mode+")",
					"CREATE AGGREGATE ma_nostate_"+mode+"(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,MFINALFUNC_EXTRA,MFINALFUNC_MODIFY="+mode+")")
			}
			lifecycleExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION ma_domain_inverse(ma_state,integer) FROM PUBLIC", "RESET SESSION AUTHORIZATION")
		}
		check := func(db fixture.Executor, recorder *fixture.Recorder) {
			movingStoredCheck(t, p, b, db, recorder)
			for i, mode := range []string{"read_only", "shareable", "read_write"} {
				for _, prefix := range []string{"ma_persist_", "ma_nostate_"} {
					s := fixture.Select("SELECT a.aggmfinalextra,a.aggmfinalmodify::text AS mode FROM (SELECT oid FROM pg_proc WHERE proname='"+prefix+mode+"') p JOIN pg_aggregate a ON a.aggfnoid=p.oid", []string{"aggmfinalextra", "mode"}, []string{"BOOL", "TEXT"}, [][]any{{prefix == "ma_nostate_", []string{"r", "s", "w"}[i]}})
					fixture.RunStep(t, p.Ctx, db, recorder, s)
					fixture.RunStep(t, p.Ctx, db, recorder, fixture.Prepared(s))
				}
			}
			for _, s := range []fixture.Step{
				fixture.Select("SELECT has_function_privilege('public','ma_domain_inverse(ma_state,integer)','EXECUTE') AS v", []string{"v"}, []string{"BOOL"}, [][]any{{false}}),
				fixture.Select("SELECT pg_get_acl(1255,oid,0)::text AS acl FROM pg_proc WHERE oid='ma_domain_inverse(ma_state,integer)'::regprocedure", []string{"acl"}, []string{"TEXT"}, [][]any{{"{ma_persist_owner=X/ma_persist_owner}"}}),
				fixture.Select("SELECT p.proowner=r.oid AS owner FROM (SELECT proowner FROM pg_proc WHERE proname IN('ma_domain','ma_domain_inverse','ma_domain_forward','ma_domain_final')) p JOIN pg_roles r ON r.rolname='ma_persist_owner'", []string{"owner"}, []string{"BOOL"}, [][]any{{true}, {true}, {true}, {true}}),
			} {
				fixture.RunStep(t, p.Ctx, db, recorder, s)
				fixture.RunStep(t, p.Ctx, db, recorder, fixture.Prepared(s))
			}
		}
		check(c, b.Recorder)
		if b.PG {
			return
		}
		old, err := c.QueryContext(p.Ctx, "SELECT id,v FROM ma_view ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		var dump bytes.Buffer
		if err = stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = dump.String()
		if emitted == "" {
			t.Fatal("empty moving aggregate dump")
		}
		if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check(c, b.Recorder)
		}
		if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted+"\nCREATE AGGREGATE ma_broken(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,MSFUNC=pg_catalog.int4pl,MSTYPE=int4);")); err == nil {
			t.Fatal("invalid restore published")
		}
		check(c, b.Recorder)
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err = stuntgres.Restore(ctx, p.Native, strings.NewReader(emitted)); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled restore=%v", err)
		}
		check(c, b.Recorder)
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("aggregate-moving-fresh/" + t.Name())
		if err != nil {
			t.Fatal(err)
		}
		fresh := sql.OpenDB(connector)
		defer fresh.Close()
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		fixture.RunStep(t, p.Ctx, fresh, recorder, fixture.Exec("CREATE ROLE ma_persist_owner", 0))
		if err = stuntgres.Restore(p.Ctx, fresh, strings.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		freshConn, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer freshConn.Close()
		fixture.RunStep(t, p.Ctx, freshConn, recorder, fixture.Exec("SET search_path TO ma_owned_scope,public", 0))
		check(freshConn, recorder)
		fixture.ScalarRows(t, false, old, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(5)}, {int64(3), int64(8)}, {int64(4), int64(12)}}, false)
	})
}

func TestReferenceAggregateMovingFeatureSmoke(t *testing.T) {
	var emitted string
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if b.PG {
			if emitted == "" {
				t.Fatal("missing actual native moving feature smoke dump")
			}
			if _, err := c.ExecContext(p.Ctx, emitted); err != nil {
				t.Fatal(err)
			}
		} else {
			movingLogSetup(t, p, b, c, true)
			lifecycleExec(t, p, b, c, "CREATE AGGREGATE ma_unsafe(integer) (SFUNC=ma_regular,STYPE=int4,INITCOND='0',MSFUNC=ma_forward,MINVFUNC=ma_inverse,MSTYPE=int4,MINITCOND='0',MFINALFUNC_MODIFY=read_write)")
		}
		const q = "SELECT id,ma_logged(x) OVER(ORDER BY id ROWS $1 PRECEDING) AS v FROM ma_input ORDER BY id"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for route := range 3 {
			s := movingRows(q, int64(2), int64(5), int64(8), int64(12))
			s.Args = []any{int64(1)}
			s.CheckMetadata = movingMetadata(b.PG)
			if route == 2 {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
			} else {
				s.Prepared = route == 1
				createAggregateRun(t, p, b, c, s)
			}
			movingLog(t, p, b, c, [][]any{{"f", int64(2)}, {"f", int64(3)}, {"i", int64(2)}, {"f", int64(3)}, {"f", int64(5)}, {"i", int64(3)}, {"f", int64(7)}})
		}
		zero := movingRows(q, int64(2), int64(3), int64(5), int64(7))
		zero.Args = []any{int64(0)}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, zero)
		movingLog(t, p, b, c, [][]any{{"f", int64(2)}, {"f", int64(3)}, {"f", int64(5)}, {"f", int64(7)}})
		again := movingRows(q, int64(2), int64(5), int64(8), int64(12))
		again.Args = []any{int64(1)}
		again.CheckMetadata = movingMetadata(b.PG)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, again)
		movingLog(t, p, b, c, [][]any{{"f", int64(2)}, {"f", int64(3)}, {"i", int64(2)}, {"f", int64(3)}, {"f", int64(5)}, {"i", int64(3)}, {"f", int64(7)}})
		unsafe := fixture.ImplicitError("SELECT ma_unsafe(x) OVER(ORDER BY id ROWS 1 PRECEDING) AS v FROM ma_input", fixture.Diagnostic{Code: "0A000", Message: "aggregate function ma_unsafe(integer) does not support use as a window function"})
		unsafe.SelectRows, unsafe.ErrorStage = true, "execute"
		createAggregateBoth(t, p, b, c, unsafe)
		movingLog(t, p, b, c, nil)
		if b.PG {
			return
		}
		old, err := c.QueryContext(p.Ctx, "SELECT id,ma_sum(x) OVER(ORDER BY id ROWS 1 PRECEDING) AS v FROM ma_input ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		var dump bytes.Buffer
		if err = stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = dump.String()
		if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		createAggregateBoth(t, p, b, c, movingRows(movingWindow, int64(2), int64(5), int64(8), int64(12)))
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("moving-smoke/" + t.Name())
		if err != nil {
			t.Fatal(err)
		}
		fresh := sql.OpenDB(connector)
		defer fresh.Close()
		if err = stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		s := movingRows(movingWindow, int64(2), int64(5), int64(8), int64(12))
		s.CheckMetadata = movingMetadata(false)
		fixture.RunStep(t, p.Ctx, fresh, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, fresh, b.Recorder, fixture.Prepared(s))
		fixture.ScalarRows(t, false, old, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(5)}, {int64(3), int64(8)}, {int64(4), int64(12)}}, false)
	})
}
