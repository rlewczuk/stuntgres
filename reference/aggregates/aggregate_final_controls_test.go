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

func TestReferenceAggregateFinalPersistence(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"fa_persist_owner"})
	fixture.SchemaReferenceCleanup(t, "fa_owned_scope")
	var emitted string
	var explicit string
	aggregateFinalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateRun(t, p, b, c, fixture.Exec("CREATE ROLE fa_persist_owner", 0))
		if b.PG {
			defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			if emitted == "" {
				t.Fatal("missing actual native emitted SQL")
			}
			if _, err := c.ExecContext(p.Ctx, emitted); err != nil {
				t.Fatal(err)
			}
			createAggregateRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		} else {
			aggregateFinalSetup(t, p, b, c)
			createAggregateRun(t, p, b, c,
				fixture.Exec(`CREATE DOMAIN fa_result AS text COLLATE "C"`, 0),
				fixture.Exec("CREATE FUNCTION fa_domain_final(integer) RETURNS fa_result LANGUAGE SQL AS $$SELECT ('total='||$1::text)::fa_result$$", 0),
				fixture.Exec("CREATE AGGREGATE fa_domain(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',FINALFUNC=fa_domain_final)", 0),
				fixture.Exec("CREATE VIEW fa_persist_view AS SELECT fa_domain(x) AS v FROM fa_input", 0),
				fixture.Exec("CREATE FUNCTION fa_persist_total() RETURNS fa_result BEGIN ATOMIC SELECT fa_domain(x) FROM fa_input; END", 0),
				fixture.Exec("CREATE FUNCTION fa_extra_final(integer,integer) RETURNS text LANGUAGE SQL AS $$SELECT 'total='||$1::text||';null='||($2 IS NULL)::text$$", 0),
				fixture.Exec("CREATE AGGREGATE fa_persist_extra(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',FINALFUNC=fa_extra_final,FINALFUNC_EXTRA)", 0),
				fixture.Exec("CREATE SCHEMA fa_owned_scope AUTHORIZATION fa_persist_owner", 0),
				fixture.Exec("SET ROLE fa_persist_owner", 0),
				fixture.Exec("CREATE FUNCTION fa_owned_scope.final(integer) RETURNS text LANGUAGE SQL AS $$SELECT 'owned='||$1::text$$", 0),
				fixture.Exec("CREATE AGGREGATE fa_owned_scope.total(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',FINALFUNC=fa_owned_scope.final)", 0),
				fixture.Exec("RESET ROLE", 0))
			for _, mode := range []string{"read_only", "shareable", "read_write"} {
				createAggregateRun(t, p, b, c,
					fixture.Exec("CREATE AGGREGATE fa_persist_"+mode+"(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',FINALFUNC=fa_final,FINALFUNC_MODIFY="+mode+")", 0),
					fixture.Exec("CREATE AGGREGATE fa_nofinal_"+mode+"(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',FINALFUNC_EXTRA,FINALFUNC_MODIFY="+mode+")", 0))
			}
		}
		check := func(db fixture.Executor, recorder *fixture.Recorder) {
			run := func(s fixture.Step) {
				fixture.RunStep(t, p.Ctx, db, recorder, s)
				fixture.RunStep(t, p.Ctx, db, recorder, fixture.Prepared(s))
			}
			for _, q := range []string{"SELECT fa_sum(x) AS v FROM fa_input", "SELECT v::text AS v FROM fa_persist_view", "SELECT fa_persist_total()::text AS v"} {
				run(aggregateFinalText(q, "total=6"))
			}
			view := aggregateFinalText("SELECT v FROM fa_persist_view", "total=6")
			view.CheckMetadata = aggregateFinalMetadata(b.PG, true)
			run(view)
			run(aggregateFinalText("SELECT fa_domain(x)::text AS v FROM fa_input WHERE FALSE", "total=0"))
			run(aggregateFinalText("SELECT fa_persist_extra(x) AS v FROM fa_input", "total=6;null=true"))
			run(aggregateFinalText("SELECT fa_owned_scope.total(x) AS v FROM fa_input", "owned=6"))
			run(fixture.Select("SELECT count(*) AS n FROM fa_input", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
			run(fixture.Select("SELECT p.prorettype='fa_result'::regtype AS result_type,a.aggtranstype=23::oid AS state_type,a.aggfinalfn=f.oid AS final FROM (SELECT oid,prorettype FROM pg_proc WHERE proname='fa_domain') p JOIN pg_aggregate a ON a.aggfnoid=p.oid JOIN pg_proc f ON f.oid=a.aggfinalfn WHERE f.proname='fa_domain_final'", []string{"result_type", "state_type", "final"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
			run(fixture.Select("SELECT a.atttypid='fa_result'::regtype AS result_type,c.collname FROM (SELECT atttypid,attcollation FROM pg_attribute WHERE attrelid='fa_persist_view'::regclass AND attname='v') a JOIN pg_collation c ON c.oid=a.attcollation", []string{"result_type", "collname"}, []string{"BOOL", "NAME"}, [][]any{{true, "C"}}))
			run(fixture.Select("SELECT p.proowner=r.oid AS owner FROM (SELECT proowner,proname FROM pg_proc WHERE proname IN('total','final')) p JOIN pg_roles r ON r.rolname='fa_persist_owner' ORDER BY p.proname", []string{"owner"}, []string{"BOOL"}, [][]any{{true}, {true}}))
			for i, mode := range []string{"read_only", "shareable", "read_write"} {
				run(aggregateFinalText("SELECT fa_persist_"+mode+"(x) AS v FROM fa_input", "total=6"))
				run(createAggregateValue("SELECT fa_nofinal_"+mode+"(x) AS v FROM fa_input", int64(6)))
				for _, name := range []string{"fa_persist_" + mode, "fa_nofinal_" + mode} {
					run(fixture.Select("SELECT a.aggfinalmodify::text AS modify,a.aggfinalextra FROM (SELECT oid FROM pg_proc WHERE proname='"+name+"') p JOIN pg_aggregate a ON a.aggfnoid=p.oid", []string{"modify", "aggfinalextra"}, []string{"TEXT", "BOOL"}, [][]any{{[]string{"r", "s", "w"}[i], strings.HasPrefix(name, "fa_nofinal_")}}))
				}
			}
			for _, edge := range []struct{ class, ref string }{{"pg_proc", "'fa_domain_final(integer)'::regprocedure"}, {"pg_type", "'fa_result'::regtype"}} {
				run(fixture.Select("SELECT d.deptype::text AS kind FROM (SELECT oid FROM pg_proc WHERE proname='fa_domain') p JOIN pg_depend d ON d.objid=p.oid WHERE d.classid='pg_proc'::regclass AND d.refclassid='"+edge.class+"'::regclass AND d.refobjid="+edge.ref, []string{"kind"}, []string{"TEXT"}, [][]any{{"n"}}))
			}
		}
		checkExplicit:=func(db fixture.Executor,recorder *fixture.Recorder){
			for _,step:=range []fixture.Step{
				fixture.Select("SELECT has_function_privilege('public','fa_domain_final(integer)','EXECUTE') AS v",[]string{"v"},[]string{"BOOL"},[][]any{{false}}),
				fixture.Select("SELECT pg_get_acl(1255,oid,0)::text AS acl FROM pg_proc WHERE oid='fa_domain_final(integer)'::regprocedure",[]string{"acl"},[]string{"TEXT"},[][]any{{"{postgres=X/postgres}"}}),
			} {fixture.RunStep(t,p.Ctx,db,recorder,step);fixture.RunStep(t,p.Ctx,db,recorder,fixture.Prepared(step))}
		}
		check(c,b.Recorder)
		if b.PG {
			if explicit=="" {t.Fatal("missing actual explicit final-function ACL stream")}
			for _,query:=range []string{"DROP SCHEMA fa_owned_scope CASCADE","DROP SCHEMA public CASCADE","CREATE SCHEMA public","GRANT USAGE ON SCHEMA public TO PUBLIC"} {
				if _,err:=c.ExecContext(p.Ctx,query);err!=nil {t.Fatal("prepare final-function ACL replay",query,err)}
			}
			if _,err:=c.ExecContext(p.Ctx,explicit);err!=nil {t.Fatal("replay actual final-function ACL stream",err)}
			createAggregateRun(t,p,b,c,fixture.Exec("RESET SESSION AUTHORIZATION",0))
			checkExplicit(c,b.Recorder)
			check(c,b.Recorder)
			return
		}
		old, err := c.QueryContext(p.Ctx, "SELECT v::text AS v FROM fa_persist_view")
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
		for range 2 {
			if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check(c,b.Recorder)
		}
		if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted+"\nCREATE AGGREGATE fa_broken(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,FINALFUNC=fa_absent);")); err == nil {
			t.Fatal("invalid restore published")
		}
		check(c,b.Recorder)
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err = stuntgres.Restore(ctx, p.Native, strings.NewReader(emitted)); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled restore=%v", err)
		}
		check(c,b.Recorder)
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("aggregate-final-fresh/" + t.Name())
		if err != nil {
			t.Fatal(err)
		}
		fresh := sql.OpenDB(connector)
		defer fresh.Close()
		freshRecorded,freshRecorder:=fixture.RecordedNativePool(t,connector)
		fixture.RunStep(t, p.Ctx, fresh, b.Recorder, fixture.Exec("CREATE ROLE fa_persist_owner", 0))
		if err = stuntgres.Restore(p.Ctx, fresh, strings.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		check(freshRecorded,freshRecorder)
		fixture.ScalarRows(t, false, old, []string{"v"}, []string{"TEXT"}, [][]any{{"total=6"}}, false)
		createAggregateRun(t, p, b, c, fixture.Exec("REVOKE EXECUTE ON FUNCTION fa_domain_final(integer) FROM PUBLIC", 0))
		var saved bytes.Buffer
		if err = stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
			t.Fatal(err)
		}
		explicit=saved.String()
		if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(saved.Bytes())); err != nil {
			t.Fatal(err)
		}
		checkExplicit(c,b.Recorder)
		check(c,b.Recorder)
		if err:=stuntgres.Restore(p.Ctx,fresh,bytes.NewReader(saved.Bytes()));err!=nil {t.Fatal("fresh explicit final-function ACL replay",err)}
		checkExplicit(freshRecorded,freshRecorder)
		check(freshRecorded,freshRecorder)
	})
}
