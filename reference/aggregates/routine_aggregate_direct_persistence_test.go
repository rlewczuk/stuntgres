package reference_test

import (
	"bytes"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoutineAggregateDirectPersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "ad_saved")
	fixture.RoleReferenceCleanup(t, []string{"ad_default_reader"})
	var emitted string
	aggregateDirectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		aggregateDirectExec(t, p, b, c, "CREATE ROLE ad_default_reader")
		setup := func() {
			aggregateDirectSetup(t, p, b, c)
			aggregateDirectCallers(t, p, b, c)
			aggregateDirectExec(t, p, b, c,
				"CREATE SCHEMA ad_saved",
				"ALTER FUNCTION ad_f(integer) RENAME TO ad_final",
				"ALTER FUNCTION ad_final(integer) SET SCHEMA ad_saved",
				"ALTER AGGREGATE ad_a(integer) RENAME TO ad_total",
				"ALTER AGGREGATE ad_total(integer) SET SCHEMA ad_saved",
				"CREATE FUNCTION ad_f(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1*1000$$",
				"CREATE AGGREGATE ad_a(integer)(SFUNC=ad_s,STYPE=integer,INITCOND='0',FINALFUNC=ad_f)")
		}
		if b.PG && emitted != "" {
			// The exact bytes emitted by the public native API are the input;
			// no rewritten names, grants, ordering or hand-maintained SQL clone.
			if _, err := c.ExecContext(p.Ctx, emitted); err != nil {
				t.Fatal("unchanged native SQL replay", err)
			}
			aggregateDirectExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
		} else {
			// Use the same independently specified initial SQL scenario when
			// no native-emitted replay stream is available.
			setup()
		}
		check := func(db fixture.Executor, rec *fixture.Recorder) {
			run := func(s fixture.Step) {
				fixture.RunStep(t, p.Ctx, db, rec, s)
				fixture.RunStep(t, p.Ctx, db, rec, fixture.Prepared(s))
			}
			for _, tc := range []struct {
				name string
				want int64
			}{{"ad_c", 30}, {"ad_e", 30}, {"ad_m", 60}, {"ad_reverse", 60}, {"ad_r", 120}, {"ad_d", 300}, {"ad_both", 100}} {
				run(createAggregateValue("SELECT "+tc.name+"() AS v", tc.want))
			}
			run(createAggregateValue("SELECT ad_a(v) AS v FROM ad_input", int64(3000)))
			run(createAggregateValue("SELECT ad_saved.ad_total(v) AS v FROM ad_input WHERE FALSE", int64(0)))
			run(fixture.Select("SELECT v FROM ad_input ORDER BY v NULLS LAST", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {nil}}))
			run(createAggregateValue("SELECT v FROM ad_sentinel", int64(73)))
			run(aggregateDirectBag([]string{"ad_total", "ad_a", "ad_c", "ad_e", "ad_m", "ad_reverse", "ad_r", "ad_d", "ad_both"},
				[2]string{"ad_total", "ad_s"}, [2]string{"ad_total", "ad_final"}, [2]string{"ad_a", "ad_s"}, [2]string{"ad_a", "ad_f"},
				[2]string{"ad_c", "ad_total"}, [2]string{"ad_e", "ad_s"}, [2]string{"ad_e", "ad_final"},
				[2]string{"ad_m", "ad_total"}, [2]string{"ad_m", "ad_s"}, [2]string{"ad_m", "ad_final"},
				[2]string{"ad_reverse", "ad_total"}, [2]string{"ad_reverse", "ad_s"}, [2]string{"ad_reverse", "ad_final"},
				[2]string{"ad_r", "ad_total"}, [2]string{"ad_r", "ad_s"}, [2]string{"ad_r", "ad_final"},
				[2]string{"ad_d", "ad_total"}, [2]string{"ad_d", "ad_s"}, [2]string{"ad_d", "ad_final"},
				[2]string{"ad_both", "ad_final"}, [2]string{"ad_both", "ad_final"}))
			run(fixture.Select(`SELECT a.aggtransfn=s.oid AS transition_identity,a.aggfinalfn=f.oid AS final_identity,
 a.aggfnoid=p.oid AS aggregate_identity,p.oid<>other.oid AS distinct_identity
 FROM (SELECT oid FROM pg_proc WHERE proname='ad_total') p
 JOIN pg_aggregate a ON a.aggfnoid=p.oid
 JOIN pg_proc s ON s.oid=a.aggtransfn JOIN pg_proc f ON f.oid=a.aggfinalfn
 JOIN pg_proc other ON other.proname='ad_a'
 WHERE s.proname='ad_s' AND f.proname='ad_final'`, []string{"transition_identity", "final_identity", "aggregate_identity", "distinct_identity"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
			// A real nonowner with no role or routine grants receives default
			// PUBLIC EXECUTE, never grant options, for every restored declaration.
			run(fixture.Select(`SELECT proname,proowner<>'ad_default_reader'::regrole AS nonowner,
 has_function_privilege('ad_default_reader',oid,'EXECUTE') AS reader_execute,
 has_function_privilege('ad_default_reader',oid,'EXECUTE WITH GRANT OPTION') AS reader_grant_option
 FROM pg_proc WHERE proname IN('ad_s','ad_final','ad_f','ad_total','ad_a','ad_c','ad_e','ad_m','ad_reverse','ad_r','ad_d','ad_both')
 ORDER BY proname`, []string{"proname", "nonowner", "reader_execute", "reader_grant_option"}, []string{"NAME", "BOOL", "BOOL", "BOOL"}, [][]any{
				{"ad_a", true, true, false}, {"ad_both", true, true, false},
				{"ad_c", true, true, false}, {"ad_d", true, true, false},
				{"ad_e", true, true, false}, {"ad_f", true, true, false},
				{"ad_final", true, true, false}, {"ad_m", true, true, false},
				{"ad_r", true, true, false}, {"ad_reverse", true, true, false},
				{"ad_s", true, true, false}, {"ad_total", true, true, false},
			}))
		}
		check(c, b.Recorder)
		if b.PG {
			return
		}
		for round := range 2 {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			emitted = dump.String()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			createAggregateRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE proname LIKE 'ad_%'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check(c, b.Recorder)
			ids := aggregateDirectIDs(t, p, c)
			if len(ids) != 12 {
				t.Fatalf("round %d missing declarations: %v", round, ids)
			}
		}
		held := aggregateDirectHeld(t, p, c, "SELECT ad_m() AS v")
		ids := aggregateDirectIDs(t, p, c)
		for _, script := range []string{"CREATE FUNCTION (", emitted + "\nCREATE AGGREGATE ad_bad(integer)(SFUNC=ad_missing,STYPE=integer);"} {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err == nil {
				t.Fatal("invalid replacement published")
			}
			aggregateDirectSameIDs(t, p, c, ids)
			check(c, b.Recorder)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, createAggregateValue(held.SQL, int64(60)))
		}
		readFailure := errors.New("aggregate direct restore reader failure")
		if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{Err: readFailure}); !errors.Is(err, readFailure) {
			t.Fatalf("reader error=%v", err)
		}
		aggregateDirectSameIDs(t, p, c, ids)
		check(c, b.Recorder)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, createAggregateValue(held.SQL, int64(60)))
		connector, err := stuntgres.NewDriver().OpenConnector("aggregate-direct-fresh/" + t.Name())
		if err != nil {
			t.Fatal(err)
		}
		fresh := sql.OpenDB(connector)
		defer fresh.Close()
		recorded, rec := fixture.RecordedNativePool(t, connector)
		fixture.RunStep(t, p.Ctx, recorded, rec, fixture.Exec("CREATE ROLE ad_default_reader", 0))
		if err := stuntgres.Restore(p.Ctx, fresh, strings.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		check(recorded, rec)
		fc, err := fresh.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fc.Close()
		if ids := aggregateDirectIDs(t, p, fc); len(ids) != 12 {
			t.Fatalf("fresh missing declarations: %v", ids)
		}
	})
}
