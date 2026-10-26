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

func aggregateCombinePersistentCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	createAggregateBoth(t, p, b, c, aggregateCombineValue("SELECT v FROM ac_saved", int64(30)))
	createAggregateBoth(t, p, b, c, aggregateCombineValue("SELECT ac_squares(x) AS v FROM ac_input WHERE FALSE", nil))
	createAggregateBoth(t, p, b, c, fixture.Select("SELECT p.proargtypes::text AS args,p.prorettype=2281::oid AS result_type,p.proparallel::text AS parallel,a.aggtransfn=6299::oid AS transition,a.aggcombinefn=6299::oid AS combine,a.aggserialfn=6300::oid AS serialize,a.aggdeserialfn=6301::oid AS deserialize,p.proowner=r.oid AS owner FROM (SELECT * FROM pg_proc WHERE proname='ac_internal') p JOIN pg_aggregate a ON a.aggfnoid=p.oid JOIN pg_roles r ON r.rolname='postgres'", []string{"args", "result_type", "parallel", "transition", "combine", "serialize", "deserialize", "owner"}, []string{"TEXT", "BOOL", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"2281", true, "s", true, true, true, true, true}}))
	createAggregateBoth(t, p, b, c, fixture.Select("SELECT string_agg(v,',' ORDER BY id) AS v FROM(VALUES(1,'a'::text),(2,'b')) s(id,v)", []string{"v"}, []string{"TEXT"}, [][]any{{"a,b"}}))
	createAggregateBoth(t, p, b, c, fixture.Select("SELECT o.oprname FROM (SELECT oid FROM pg_proc WHERE proname='ac_persist_max') p JOIN pg_aggregate a ON a.aggfnoid=p.oid JOIN pg_operator o ON o.oid=a.aggsortop", []string{"oprname"}, []string{"NAME"}, [][]any{{">#"}}))
}

func aggregateCombinePersistence(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) string {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	emitted := dump.String()
	old, err := c.QueryContext(p.Ctx, "SELECT v FROM ac_saved")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	const query = "SELECT v FROM ac_saved WHERE $1::integer=1"
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		aggregateCombinePersistentCheck(t, p, b, c)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, aggregateCombineValue(query, int64(30), 1))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"v"}, []string{"INT8"}, nil, 0))
	}
	for _, suffix := range []string{
		"CREATE AGGREGATE ac_bad(integer) (SFUNC=pg_catalog.int4larger,STYPE=integer,COMBINEFUNC=missing_ac_merge);",
		"CREATE AGGREGATE ac_bad(integer) (SFUNC=pg_catalog.int4larger,STYPE=integer,SORTOP = ~~);",
		"CREATE AGGREGATE ac_bad(integer) (SFUNC=pg_catalog.int4larger,STYPE=integer,PARALLEL=invalid);",
	} {
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted+"\n"+suffix)); err == nil {
			t.Fatal("invalid later aggregate published")
		}
		aggregateCombinePersistentCheck(t, p, b, c)
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.Native, strings.NewReader(emitted)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	aggregateCombinePersistentCheck(t, p, b, c)
	fixture.ScalarRows(t, false, old, []string{"v"}, []string{"INT8"}, [][]any{{int64(30)}}, false)
	freshDriver := stuntgres.NewDriver()
	connector, err := freshDriver.OpenConnector("aggregate-combine-fresh/" + t.Name())
	if err != nil {
		t.Fatal(err)
	}
	fresh := sql.OpenDB(connector)
	defer fresh.Close()
	if err := stuntgres.Restore(p.Ctx, fresh, strings.NewReader(emitted)); err != nil {
		t.Fatal(err)
	}
	rows, err := fresh.QueryContext(p.Ctx, "SELECT v FROM ac_saved")
	if err != nil {
		t.Fatal(err)
	}
	fixture.ScalarRows(t, false, rows, []string{"v"}, []string{"INT8"}, [][]any{{int64(30)}}, false)
	return emitted
}

func TestReferenceAggregateCombineLifecycle(t *testing.T) {
	var emitted string
	aggregateCombineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if b.PG {
			if emitted == "" {
				t.Fatal("missing actual native dump")
			}
			defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			if _, err := c.ExecContext(p.Ctx, emitted); err != nil {
				t.Fatal(err)
			}
			createAggregateRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		} else {
			aggregateCombineSetup(t, p, b, c)
			createAggregateRun(t, p, b, c,
				fixture.Exec(aggregateCombineInternalDDL, 0),
				fixture.Exec("CREATE FUNCTION ac_persist_merge(bigint,bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT coalesce($1,0)+coalesce($2,0)$$", 0),
				fixture.Exec("CREATE AGGREGATE ac_owned_merge(integer) (SFUNC=ac_square_step,STYPE=bigint,COMBINEFUNC=ac_persist_merge,PARALLEL=SAFE)", 0),
				fixture.Exec("CREATE FUNCTION ac_persist_gt(integer,integer) RETURNS boolean LANGUAGE SQL AS $$SELECT $1>$2$$", 0),
				fixture.Exec("CREATE OPERATOR public.># (LEFTARG=integer,RIGHTARG=integer,FUNCTION=ac_persist_gt)", 0),
				fixture.Exec("CREATE AGGREGATE ac_persist_max(integer) (SFUNC=pg_catalog.int4larger,STYPE=integer,COMBINEFUNC=pg_catalog.int4larger,SORTOP = OPERATOR(public.>#),PARALLEL=SAFE)", 0),
				fixture.Exec("CREATE VIEW ac_saved AS SELECT ac_owned_merge(x) AS v FROM ac_input", 0))
		}
		aggregateCombinePersistentCheck(t, p, b, c)
		if !b.PG {
			emitted = aggregateCombinePersistence(t, p, b, c)
		}
	})
}

func TestReferenceAggregateCombineFeatureSmoke(t *testing.T) {
	aggregateCombineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		aggregateCombineSetup(t, p, b, c)
		createAggregateBoth(t, p, b, c, aggregateCombineValue("SELECT ac_squares(x) AS v FROM ac_input", int64(30)))
		createAggregateBoth(t, p, b, c, aggregateCombineValue("SELECT ac_squares(x) AS v FROM ac_input WHERE FALSE", nil))
		aggregateCombineHeld(t, p, b, c)
		createAggregateRun(t, p, b, c, fixture.Exec(aggregateCombineInternalDDL, 0))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT string_agg('x'::text,','::text) AS v FROM ac_input", []string{"v"}, []string{"TEXT"}, [][]any{{"x,x,x,x,x,x"}}))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT string_agg('\\x0080ff'::bytea,'\\x2c'::bytea) AS v FROM ac_input WHERE id<=2", []string{"v"}, []string{"BYTEA"}, [][]any{{"\x00\x80\xff,\x00\x80\xff"}}))
		s := fixture.ImplicitError("CREATE AGGREGATE ac_smoke_retry(internal) (SFUNC=pg_catalog.string_agg_combine,STYPE=internal,COMBINEFUNC=pg_catalog.int4larger)", fixture.Diagnostic{Code: "42883", Message: "function pg_catalog.int4larger(internal, internal) does not exist"})
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		createAggregateRun(t, p, b, c, fixture.Exec("CREATE AGGREGATE ac_smoke_retry(integer) (SFUNC=pg_catalog.int4larger,STYPE=integer,COMBINEFUNC=pg_catalog.int4larger,SORTOP = >,PARALLEL=SAFE)", 0))
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ac_smoke_retry(x) AS v FROM ac_input", int64(4)))
		createAggregateBoth(t, p, b, c, aggregateCombineValue("SELECT ac_squares(x) AS v FROM ac_input", int64(30)))
		createAggregateRun(t, p, b, c, fixture.Exec("CREATE VIEW ac_smoke_saved AS SELECT ac_squares(x) AS v FROM ac_input", 0))
		check := func() {
			createAggregateBoth(t, p, b, c, aggregateCombineValue("SELECT v FROM ac_smoke_saved", int64(30)))
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT p.prorettype=2281::oid AS result_type,p.proparallel::text AS parallel,p.proowner=r.oid AS owner,a.aggcombinefn=6299::oid AS combine,a.aggserialfn=6300::oid AS serialize,a.aggdeserialfn=6301::oid AS deserialize FROM (SELECT * FROM pg_proc WHERE proname='ac_internal') p JOIN pg_aggregate a ON a.aggfnoid=p.oid JOIN pg_roles r ON r.rolname=current_user", []string{"result_type", "parallel", "owner", "combine", "serialize", "deserialize"}, []string{"BOOL", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, "s", true, true, true, true}}))
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ac_smoke_retry(x) AS v FROM ac_input", int64(4)))
		}
		check()
		if !b.PG {
			const q = "SELECT ac_squares(x) AS v FROM ac_input WHERE id<=$1"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, aggregateCombineValue(q, int64(30), 6))
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
					t.Fatal(err)
				}
				createAggregateBoth(t, p, b, c, aggregateCombineValue("SELECT ac_squares(x) AS v FROM ac_input", int64(30)))
				check()
				fixture.RunStep(t, p.Ctx, held, b.Recorder, aggregateCombineValue(q, nil, 0))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, aggregateCombineValue(q, int64(5), 3))
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"\nCREATE AGGREGATE ac_smoke_bad(integer) (SFUNC=pg_catalog.int4larger,STYPE=integer,COMBINEFUNC=missing_smoke_support);")); err == nil {
				t.Fatal("bad later restore published")
			}
			check()
		}
	})
}
