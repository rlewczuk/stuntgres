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

func TestReferenceAggregateOrderedPersistence(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"ua_persist_owner"})
	fixture.SchemaReferenceCleanup(t,"ua_owned")
	var emitted string
	createAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		createAggregateRun(t,p,b,c,fixture.Exec("CREATE ROLE ua_persist_owner",0))
		if b.PG {
			defer c.ExecContext(context.Background(),"RESET SESSION AUTHORIZATION")
			if emitted=="" {t.Fatal("missing actual native emitted ordered-aggregate SQL")}
			if _,err:=c.ExecContext(p.Ctx,emitted);err!=nil {t.Fatal("replay actual native Dump",err)}
			createAggregateRun(t,p,b,c,fixture.Exec("RESET SESSION AUTHORIZATION",0))
		} else {
			aggregateOrderedSetup(t,p,b,c);aggregateOrderedHypothetical(t,p,b,c);aggregateOrderedRanks(t,p,b,c)
			aggregateOrderedModesSetup(t,p,b,c)
			aggregateOrderedStrictDeclarationSetup(t,p,b,c)
			aggregateOrderedMovingSetup(t,p,b,c)
			for _,mode:=range []string{"read_only","shareable","read_write"} {
				createAggregateRun(t,p,b,c,fixture.Exec("CREATE AGGREGATE ua_mode_"+mode+"(text,boolean ORDER BY integer) (SFUNC=pg_catalog.array_append,STYPE=integer[],INITCOND='{}',FINALFUNC=ua_finish,FINALFUNC_EXTRA,FINALFUNC_MODIFY="+mode+")",0))
			}
			createAggregateRun(t,p,b,c,
				fixture.Exec("CREATE FUNCTION ua_break_state(integer,integer) RETURNS integer STRICT LANGUAGE SQL AS $$SELECT CASE WHEN $2=2 THEN NULL::integer ELSE coalesce($1,100)+$2 END$$",0),
				fixture.Exec("CREATE AGGREGATE ua_no_reseed(ORDER BY integer) (SFUNC=ua_break_state,STYPE=integer,INITCOND='0')",0),
				fixture.Exec("CREATE AGGREGATE ua_persist_named(IN prefix text,IN descending boolean ORDER BY IN value integer) (SFUNC=pg_catalog.array_append,STYPE=integer[],INITCOND='{}',FINALFUNC=ua_finish,FINALFUNC_EXTRA)",0),
				fixture.Exec(`CREATE AGGREGATE ua_persist_named_rank(VARIADIC direct_values "any" ORDER BY VARIADIC row_values "any") (SFUNC=pg_catalog.ordered_set_transition_multi,STYPE=internal,FINALFUNC=pg_catalog.rank_final,FINALFUNC_EXTRA,HYPOTHETICAL)`,0),
				fixture.Exec("CREATE DOMAIN ua_result AS text COLLATE \"C\"",0),
				fixture.Exec("CREATE FUNCTION ua_domain_final(integer[],text,boolean,integer) RETURNS ua_result LANGUAGE SQL AS $$SELECT ($2||array_to_string(array_sort($1,$3,false),',','NULL')||';extra='||($4 IS NULL)::text)::ua_result$$",0),
				fixture.Exec("CREATE AGGREGATE ua_domain(text,boolean ORDER BY integer) (SFUNC=pg_catalog.array_append,STYPE=integer[],INITCOND='{}',FINALFUNC=ua_domain_final,FINALFUNC_EXTRA)",0),
				fixture.Exec(`CREATE VIEW ua_persist_view AS SELECT ua_domain('v:' COLLATE "C",false) WITHIN GROUP(ORDER BY x) AS v FROM ua_input`,0),
				fixture.Exec(`CREATE FUNCTION ua_persist_total() RETURNS ua_result BEGIN ATOMIC SELECT ua_domain('f:' COLLATE "C",false) WITHIN GROUP(ORDER BY x) FROM ua_input; END`,0),
				fixture.Exec("CREATE AGGREGATE ua_rows_only(ORDER BY VARIADIC \"any\") (SFUNC=pg_catalog.ordered_set_transition_multi,STYPE=internal,FINALFUNC=pg_catalog.rank_final,FINALFUNC_EXTRA)",0),
				fixture.Exec("CREATE SCHEMA ua_owned AUTHORIZATION ua_persist_owner",0),
				fixture.Exec(`CREATE AGGREGATE ua_owned.rank(VARIADIC "any" ORDER BY VARIADIC "any") (SFUNC=pg_catalog.ordered_set_transition_multi,STYPE=internal,FINALFUNC=pg_catalog.rank_final,FINALFUNC_EXTRA,HYPOTHETICAL)`,0),
				fixture.Exec(`ALTER AGGREGATE ua_owned.rank(VARIADIC "any" ORDER BY VARIADIC "any") OWNER TO ua_persist_owner`,0),
				fixture.Exec("SET ROLE ua_persist_owner",0),
				fixture.Exec("CREATE FUNCTION ua_owned.finish(integer,text) RETURNS text LANGUAGE SQL AS $$SELECT $2||$1::text$$",0),
				fixture.Exec("CREATE AGGREGATE ua_owned.total(text ORDER BY integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0',FINALFUNC=ua_owned.finish)",0),
				fixture.Exec("RESET ROLE",0),
				fixture.Exec("REVOKE EXECUTE ON FUNCTION ua_domain_final(integer[],text,boolean,integer) FROM PUBLIC",0))
		}
		check:=func(db fixture.Executor,recorder *fixture.Recorder){
			run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,db,recorder,s);fixture.RunStep(t,p.Ctx,db,recorder,fixture.Prepared(s))}
			run(fixture.Select("SELECT proisstrict FROM pg_proc WHERE oid='ua_break_state(integer,integer)'::regprocedure",[]string{"proisstrict"},[]string{"BOOL"},[][]any{{true}}))
			run(createAggregateValue("SELECT ua_break_state(NULL::integer,1) AS v",nil))
			run(createAggregateValue("SELECT ua_no_reseed() WITHIN GROUP(ORDER BY x DESC) AS v FROM(SELECT x FROM(VALUES(1),(2),(3)) s(x) ORDER BY x LIMIT 100) ordered",nil))
			run(createAggregateValue("SELECT ua_no_reseed() WITHIN GROUP(ORDER BY x) AS v FROM(VALUES(1),(NULL::integer),(3)) s(x)",int64(4)))
			for _,tc:=range []struct{q,want string}{
				{"SELECT ua_sorted('p:',false) WITHIN GROUP(ORDER BY x) AS v FROM ua_input","p:1,2,3,NULL;extra=true"},
				{"SELECT v::text AS v FROM ua_persist_view","v:1,2,3,NULL;extra=true"},
				{"SELECT ua_persist_total()::text AS v","f:1,2,3,NULL;extra=true"},
				{"SELECT ua_h('p:',100) WITHIN GROUP(ORDER BY x) AS v FROM ua_input","state=6;prefix=p:;target=100;extra=true"},
				{"SELECT ua_owned.total('owned:') WITHIN GROUP(ORDER BY x) AS v FROM ua_input","owned:6"},
			}{run(aggregateFinalText(tc.q,tc.want))}
			view:=aggregateFinalText("SELECT v FROM ua_persist_view","v:1,2,3,NULL;extra=true");view.CheckMetadata=aggregateFinalMetadata(b.PG,true);run(view)
			run(aggregateOrderedRankRow("SELECT ua_rank(20) WITHIN GROUP(ORDER BY x) AS r,ua_dense(20) WITHIN GROUP(ORDER BY x) AS d,ua_percent(20) WITHIN GROUP(ORDER BY x) AS p,ua_cume(20) WITHIN GROUP(ORDER BY x) AS c FROM(VALUES(10),(20),(20),(40)) s(x)",int64(2),int64(2),.25,.8))
			run(fixture.Select("SELECT p.prorettype='ua_result'::regtype AS result_type,a.aggtranstype='integer[]'::regtype AS state_type,a.aggkind::text AS kind,a.aggnumdirectargs::integer AS direct,a.aggfinalextra,a.aggfinalmodify::text AS modify,a.aggfinalfn='ua_domain_final(integer[],text,boolean,integer)'::regprocedure AS final FROM(SELECT oid,prorettype FROM pg_proc WHERE proname='ua_domain') p JOIN pg_aggregate a ON a.aggfnoid=p.oid",[]string{"result_type","state_type","kind","direct","aggfinalextra","modify","final"},[]string{"BOOL","BOOL","TEXT","INT4","BOOL","TEXT","BOOL"},[][]any{{true,true,"o",int64(2),true,"w",true}}))
			run(fixture.Select("SELECT a.aggkind::text AS kind,a.aggnumdirectargs::integer AS direct,p.pronargs::integer AS nargs,p.provariadic=2276::oid AS variadic,a.aggtransfn='pg_catalog.ordered_set_transition_multi(internal,\"any\")'::regprocedure AS step,a.aggfinalfn='pg_catalog.rank_final(internal,\"any\")'::regprocedure AS final FROM(SELECT oid,pronargs,provariadic FROM pg_proc WHERE proname='ua_rank') p JOIN pg_aggregate a ON a.aggfnoid=p.oid",[]string{"kind","direct","nargs","variadic","step","final"},[]string{"TEXT","INT4","INT4","BOOL","BOOL","BOOL"},[][]any{{"h",int64(1),int64(1),true,true,true}}))
			run(fixture.Select("SELECT a.aggkind::text AS kind,a.aggnumdirectargs::integer AS direct FROM(SELECT oid FROM pg_proc WHERE proname='ua_rows_only') p JOIN pg_aggregate a ON a.aggfnoid=p.oid",[]string{"kind","direct"},[]string{"TEXT","INT4"},[][]any{{"o",int64(0)}}))
			run(fixture.Select("SELECT a.atttypid='ua_result'::regtype AS result_type,c.collname FROM(SELECT atttypid,attcollation FROM pg_attribute WHERE attrelid='ua_persist_view'::regclass AND attname='v') a JOIN pg_collation c ON c.oid=a.attcollation",[]string{"result_type","collname"},[]string{"BOOL","NAME"},[][]any{{true,"C"}}))
			run(fixture.Select("SELECT has_function_privilege('public','ua_domain_final(integer[],text,boolean,integer)','EXECUTE') AS v",[]string{"v"},[]string{"BOOL"},[][]any{{false}}))
			run(fixture.Select("SELECT pg_get_acl(1255,oid,0)::text AS acl FROM pg_proc WHERE oid='ua_domain_final(integer[],text,boolean,integer)'::regprocedure",[]string{"acl"},[]string{"TEXT"},[][]any{{"{postgres=X/postgres}"}}))
			run(fixture.Select("SELECT p.proowner=r.oid AS owner FROM(SELECT proowner,proname FROM pg_proc WHERE proname IN('total','finish')) p JOIN pg_roles r ON r.rolname='ua_persist_owner' ORDER BY p.proname",[]string{"owner"},[]string{"BOOL"},[][]any{{true},{true}}))
			for i,mode:=range []string{"read_only","shareable","read_write"} {
				run(aggregateFinalText("SELECT ua_mode_"+mode+"('m:',false) WITHIN GROUP(ORDER BY x) AS v FROM ua_input","m:1,2,3,NULL;extra=true"))
				run(fixture.Select("SELECT a.aggfinalmodify::text AS mode FROM(SELECT oid FROM pg_proc WHERE proname='ua_mode_"+mode+"') p JOIN pg_aggregate a ON a.aggfnoid=p.oid",[]string{"mode"},[]string{"TEXT"},[][]any{{[]string{"r","s","w"}[i]}}))
			}
			run(aggregateFinalText("SELECT ua_persist_named('n:',false) WITHIN GROUP(ORDER BY x) AS v FROM ua_input","n:1,2,3,NULL;extra=true"))
			run(fixture.Select("SELECT ua_persist_named_rank(20) WITHIN GROUP(ORDER BY x) AS v FROM(VALUES(10),(20),(40)) s(x)",[]string{"v"},[]string{"INT8"},[][]any{{int64(2)}}))
			run(fixture.Select("SELECT proname,pronargs::integer AS nargs,proargnames::text AS names FROM pg_proc WHERE proname IN('ua_persist_named','ua_persist_named_rank') ORDER BY proname",[]string{"proname","nargs","names"},[]string{"NAME","INT4","TEXT"},[][]any{{"ua_persist_named",int64(3),"{prefix,descending,value}"},{"ua_persist_named_rank",int64(1),"{direct_values}"}}))
			run(fixture.Select(`SELECT p.proowner=r.oid AS owner,a.aggtranstype='internal'::regtype AS state_type,a.aggfinalfn='pg_catalog.rank_final(internal,"any")'::regprocedure AS final FROM(SELECT oid,proowner FROM pg_proc WHERE oid='ua_owned.rank("any")'::regprocedure) p JOIN pg_aggregate a ON a.aggfnoid=p.oid JOIN pg_roles r ON r.rolname='ua_persist_owner'`,[]string{"owner","state_type","final"},[]string{"BOOL","BOOL","BOOL"},[][]any{{true,true,true}}))
			for _,tc:=range []struct{condition string;want int64}{{"TRUE",2},{"FALSE",1}} {
				run(fixture.Select("SELECT ua_owned.rank(20) WITHIN GROUP(ORDER BY x) AS v FROM(VALUES(10),(20),(40)) s(x) WHERE "+tc.condition,[]string{"v"},[]string{"INT8"},[][]any{{tc.want}}))
			}
			aggregateOrderedModesCheck(t,p,db,recorder)
			aggregateOrderedStrictDeclarationCheck(t,p,db,recorder)
			aggregateOrderedMovingCheck(t,p,db,recorder)
		}
		check(c,b.Recorder)
		if b.PG {return}
		const heldSQL="SELECT ua_sorted($1,$2) WITHIN GROUP(ORDER BY x) AS v FROM ua_input"
		stmt,err:=c.PrepareContext(p.Ctx,heldSQL);if err!=nil {t.Fatal(err)};defer stmt.Close()
		held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:heldSQL}
		old,err:=c.QueryContext(p.Ctx,"SELECT v::text AS v FROM ua_persist_view");if err!=nil {t.Fatal(err)};defer old.Close()
		var dump bytes.Buffer
		if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil {t.Fatal(err)};emitted=dump.String()
		if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil {t.Fatal(err)}
		for range 2 {
			if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(emitted));err!=nil {t.Fatal(err)}
			check(c,b.Recorder)
			fixture.RunStep(t,p.Ctx,held,b.Recorder,aggregateFinalText(heldSQL,"h:3,2,1,NULL;extra=true","h:",true))
		}
		var again bytes.Buffer
		if err:=stuntgres.Dump(p.Ctx,p.Native,&again);err!=nil {t.Fatal(err)}
		if again.String()!=emitted {t.Fatal("ordered aggregate redump changed")}
		if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(emitted+"\nCREATE AGGREGATE ua_broken(ORDER BY integer) (SFUNC=ua_absent,STYPE=integer);"));err==nil {t.Fatal("invalid restore published")};check(c,b.Recorder)
		ctx,cancel:=context.WithCancel(p.Ctx);cancel()
		if err:=stuntgres.Restore(ctx,p.Native,strings.NewReader(emitted));!errors.Is(err,context.Canceled){t.Fatalf("precanceled restore=%v",err)};check(c,b.Recorder)
		ctx,cancel=context.WithCancel(p.Ctx)
		reader:=&fixture.EnumCancelReader{Reader:bytes.NewReader(dump.Bytes()),Cancel:cancel}
		if err:=stuntgres.Restore(ctx,p.Native,reader);!errors.Is(err,context.Canceled){t.Fatalf("admitted reader cancellation=%v",err)};cancel();check(c,b.Recorder)
		driver:=stuntgres.NewDriver();connector,err:=driver.OpenConnector("ordered-fresh/"+t.Name());if err!=nil {t.Fatal(err)}
		fresh:=sql.OpenDB(connector);defer fresh.Close();recorded,recorder:=fixture.RecordedNativePool(t,connector)
		fixture.RunStep(t,p.Ctx,fresh,b.Recorder,fixture.Exec("CREATE ROLE ua_persist_owner",0))
		if err:=stuntgres.Restore(p.Ctx,fresh,strings.NewReader(emitted));err!=nil {t.Fatal(err)};check(recorded,recorder)
		fixture.ScalarRows(t,false,old,[]string{"v"},[]string{"TEXT"},[][]any{{"v:1,2,3,NULL;extra=true"}},false)
	})
}
