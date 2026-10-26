package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAggregateOrderedBinding(t *testing.T) {
	fixture.SchemaReferenceCleanup(t,"ua_scope")
	createAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		aggregateOrderedSetup(t,p,b,c)
		createAggregateRun(t,p,b,c,
			fixture.Exec("CREATE AGGREGATE ua_named(IN prefix text,IN descending boolean ORDER BY IN value integer) (SFUNC=pg_catalog.array_append,STYPE=integer[],INITCOND='{}',FINALFUNC=ua_finish,FINALFUNC_EXTRA,HYPOTHETICAL=false)",0))
		for _,name:=range []string{"ua_named"}{
			createAggregateBoth(t,p,b,c,aggregateFinalText("SELECT "+name+"('p:',false) WITHIN GROUP(ORDER BY x) AS v FROM ua_input","p:1,2,3,NULL;extra=true"))
			createAggregateBoth(t,p,b,c,fixture.Select("SELECT a.aggkind::text AS kind,a.aggnumdirectargs::integer AS direct,a.aggfinalmodify::text AS modify FROM(SELECT oid FROM pg_proc WHERE proname='"+name+"') p JOIN pg_aggregate a ON a.aggfnoid=p.oid",[]string{"kind","direct","modify"},[]string{"TEXT","INT4","TEXT"},[][]any{{"o",int64(2),"w"}}))
		}
		aggregateOrderedHypothetical(t,p,b,c)
		createAggregateRun(t,p,b,c,fixture.Exec("CREATE AGGREGATE ua_last(text,integer ORDER BY integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0',FINALFUNC=ua_hfinal,FINALFUNC_EXTRA,HYPOTHETICAL=true,HYPOTHETICAL=false)",0))
		createAggregateBoth(t,p,b,c,aggregateFinalText("SELECT ua_last('p:',100) WITHIN GROUP(ORDER BY x) AS v FROM ua_input","state=6;prefix=p:;target=100;extra=true"))
		createAggregateBoth(t,p,b,c,fixture.Select("SELECT a.aggkind::text AS kind,a.aggnumdirectargs::integer AS direct FROM(SELECT oid FROM pg_proc WHERE proname='ua_last') p JOIN pg_aggregate a ON a.aggfnoid=p.oid",[]string{"kind","direct"},[]string{"TEXT","INT4"},[][]any{{"h",int64(2)}}))
		createAggregateBoth(t,p,b,c,fixture.Select("SELECT pronargs::integer AS nargs,proargnames::text AS names FROM pg_proc WHERE proname='ua_named'",[]string{"nargs","names"},[]string{"INT4","TEXT"},[][]any{{int64(3),"{prefix,descending,value}"}}))
		createAggregateBoth(t,p,b,c,fixture.Select("SELECT proargnames IS NULL AS unnamed FROM pg_proc WHERE proname='ua_sorted'",[]string{"unnamed"},[]string{"BOOL"},[][]any{{true}}))
		createAggregateRun(t,p,b,c,
			fixture.Exec("CREATE SCHEMA ua_scope",0),
			fixture.Exec("CREATE AGGREGATE ua_scope.ua_sorted(text,boolean ORDER BY integer) (SFUNC=pg_catalog.array_append,STYPE=integer[],INITCOND='{}',FINALFUNC=public.ua_finish,FINALFUNC_EXTRA)",0),
			fixture.Exec("SET search_path=ua_scope,public",0))
		createAggregateBoth(t,p,b,c,aggregateFinalText("SELECT ua_sorted('s:',false) WITHIN GROUP(ORDER BY x) AS v FROM ua_input","s:1,2,3,NULL;extra=true"))
		createAggregateRun(t,p,b,c,fixture.Exec("SET search_path=public",0))
	})
}

func TestReferenceAggregateOrderedDirectArguments(t *testing.T) {
	createAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		aggregateOrderedSetup(t,p,b,c)
		createAggregateBoth(t,p,b,c,fixture.Select("SELECT prefix,descending,ua_sorted(prefix,descending) WITHIN GROUP(ORDER BY x) AS v FROM(VALUES('a:'::text,false,2),('a:',false,1),('b:',true,3),('b:',true,4)) s(prefix,descending,x) GROUP BY prefix,descending ORDER BY prefix",[]string{"prefix","descending","v"},[]string{"TEXT","BOOL","TEXT"},[][]any{{"a:",false,"a:1,2;extra=true"},{"b:",true,"b:4,3;extra=true"}}))
		createAggregateBoth(t,p,b,c,fixture.Select("SELECT ua_sorted('a:',false) WITHIN GROUP(ORDER BY x) AS a,ua_sorted('b:',true) WITHIN GROUP(ORDER BY x DESC) AS b FROM ua_input",[]string{"a","b"},[]string{"TEXT","TEXT"},[][]any{{"a:1,2,3,NULL;extra=true","b:3,2,1,NULL;extra=true"}}))
		createAggregateRun(t,p,b,c,
			fixture.Exec("CREATE TABLE ua_direct_log(label text,n bigint)",0),
			fixture.Exec("CREATE FUNCTION ua_direct(text) RETURNS text VOLATILE BEGIN ATOMIC INSERT INTO ua_direct_log SELECT $1,count(*)+1 FROM ua_direct_log; SELECT $1; END",0),
			fixture.Exec("CREATE FUNCTION ua_direct_int(integer) RETURNS integer VOLATILE BEGIN ATOMIC INSERT INTO ua_direct_log SELECT coalesce($1::text,'NULL'),count(*)+1 FROM ua_direct_log; SELECT $1; END",0),
			fixture.Exec("CREATE AGGREGATE ua_demand(text ORDER BY integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')",0))
		// A non-final aggregate still demands its direct expression exactly once.
		createAggregateRun(t,p,b,c,createAggregateValue("SELECT ua_demand(ua_direct('once')) WITHIN GROUP(ORDER BY x) AS v FROM ua_input",int64(6)),fixture.Select("SELECT label FROM ua_direct_log",[]string{"label"},[]string{"TEXT"},[][]any{{"once"}}),fixture.Exec("DELETE FROM ua_direct_log",1))
		createAggregateRun(t,p,b,c,
			fixture.Exec("CREATE FUNCTION ua_strict_final(integer,integer,integer) RETURNS integer STRICT LANGUAGE SQL AS $$SELECT $1+$2+$3$$",0),
			fixture.Exec("CREATE AGGREGATE ua_strict_pair(integer,integer ORDER BY integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0',FINALFUNC=ua_strict_final)",0))
		createAggregateRun(t,p,b,c,createAggregateValue("SELECT ua_strict_pair(ua_direct_int(NULL),ua_direct_int(2)) WITHIN GROUP(ORDER BY x) AS v FROM ua_input",nil),fixture.Select("SELECT label FROM ua_direct_log ORDER BY n",[]string{"label"},[]string{"TEXT"},[][]any{{"NULL"},{"2"}}),fixture.Exec("DELETE FROM ua_direct_log",2))
		createAggregateRun(t,p,b,c,
			fixture.Exec("CREATE AGGREGATE ua_strict_empty(integer,integer ORDER BY integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,FINALFUNC=ua_strict_final)",0),
			createAggregateValue("SELECT ua_strict_empty(ua_direct_int(1),ua_direct_int(2)) WITHIN GROUP(ORDER BY x) AS v FROM ua_input WHERE FALSE",nil),
			fixture.Select("SELECT label FROM ua_direct_log ORDER BY n",[]string{"label"},[]string{"TEXT"},[][]any{{"1"},{"2"}}),
			fixture.Exec("DELETE FROM ua_direct_log",2))
		createAggregateRun(t,p,b,c,fixture.Select("SELECT g,ua_demand(ua_direct('none')) WITHIN GROUP(ORDER BY x) AS v FROM ua_input WHERE FALSE GROUP BY g",[]string{"g","v"},[]string{"INT4","INT4"},nil),fixture.Select("SELECT count(*) AS n FROM ua_direct_log",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
		createAggregateRun(t,p,b,c,createAggregateValue("SELECT ua_demand(ua_direct('empty')) WITHIN GROUP(ORDER BY 1/(x-x)) FILTER(WHERE FALSE) AS v FROM ua_input",int64(0)),fixture.Select("SELECT label FROM ua_direct_log",[]string{"label"},[]string{"TEXT"},[][]any{{"empty"}}),fixture.Exec("DELETE FROM ua_direct_log",1))
		createAggregateRun(t,p,b,c,fixture.Select("SELECT g,ua_demand(ua_direct(g::text)) WITHIN GROUP(ORDER BY x) AS v FROM ua_input GROUP BY g ORDER BY g",[]string{"g","v"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(3)},{int64(2),int64(3)}}),fixture.Select("SELECT label FROM ua_direct_log ORDER BY label",[]string{"label"},[]string{"TEXT"},[][]any{{"1"},{"2"}}))
		q:="SELECT ua_demand((1/(g-g))::text) WITHIN GROUP(ORDER BY x) FILTER(WHERE FALSE) AS v FROM ua_input GROUP BY g"
		s:=fixture.ImplicitError(q,fixture.Diagnostic{Code:"22012",Message:"division by zero"});s.SelectRows=true;s.ErrorStage="execute";createAggregateBoth(t,p,b,c,s)
	})
}

func TestReferenceAggregateOrderedConsumers(t *testing.T) {
	createAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		aggregateOrderedSetup(t,p,b,c)
		createAggregateRun(t,p,b,c,
			fixture.Exec("CREATE VIEW ua_view AS SELECT ua_sorted('p:',false) WITHIN GROUP(ORDER BY x) AS v FROM ua_input",0),
			fixture.Exec("CREATE FUNCTION ua_frozen() RETURNS text BEGIN ATOMIC SELECT ua_sorted('p:',false) WITHIN GROUP(ORDER BY x) FROM ua_input; END",0),
			fixture.Exec("CREATE TABLE ua_destination(g integer,v text)",0),
			fixture.Exec("INSERT INTO ua_destination SELECT g,ua_sorted(g::text||':',false) WITHIN GROUP(ORDER BY x) FROM ua_input GROUP BY g",2))
		for _,q:=range []string{"SELECT v FROM ua_view","SELECT ua_frozen() AS v","WITH a AS(SELECT ua_sorted('p:',false) WITHIN GROUP(ORDER BY x) AS v FROM ua_input) SELECT v FROM a","SELECT v FROM(SELECT ua_sorted('p:',false) WITHIN GROUP(ORDER BY x) AS v FROM ua_input) d"}{
			s:=aggregateFinalText(q,"p:1,2,3,NULL;extra=true");if q=="SELECT v FROM ua_view" {s.CheckMetadata=aggregateFinalMetadata(b.PG,true)};createAggregateBoth(t,p,b,c,s)
		}
		createAggregateBoth(t,p,b,c,fixture.Select("SELECT g,ua_sorted(g::text||':',false) WITHIN GROUP(ORDER BY x) AS v FROM ua_input GROUP BY g HAVING ua_sorted('p:',false) WITHIN GROUP(ORDER BY x) LIKE 'p:%' ORDER BY ua_sorted(g::text||':',false) WITHIN GROUP(ORDER BY x)",[]string{"g","v"},[]string{"INT4","TEXT"},[][]any{{int64(1),"1:3,NULL;extra=true"},{int64(2),"2:1,2;extra=true"}}))
		createAggregateBoth(t,p,b,c,fixture.Select("SELECT g,v FROM ua_destination ORDER BY g",[]string{"g","v"},[]string{"INT4","TEXT"},[][]any{{int64(1),"1:3,NULL;extra=true"},{int64(2),"2:1,2;extra=true"}}))
		createAggregateBoth(t,p,b,c,fixture.Select("SELECT o.g,(SELECT ua_sorted(o.g::text||':',false) WITHIN GROUP(ORDER BY i.x) FROM ua_input i WHERE i.g=o.g) AS v FROM(VALUES(1),(2)) o(g) ORDER BY o.g",[]string{"g","v"},[]string{"INT4","TEXT"},[][]any{{int64(1),"1:3,NULL;extra=true"},{int64(2),"2:1,2;extra=true"}}))
	})
}
