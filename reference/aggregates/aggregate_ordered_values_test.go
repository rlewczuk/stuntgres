package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func aggregateOrderedSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	createAggregateRun(t, p, b, c,
		fixture.Exec("CREATE FUNCTION ua_finish(integer[],text,boolean,integer) RETURNS text LANGUAGE SQL AS $$SELECT $2||array_to_string(array_sort($1,$3,false),',','NULL')||';extra='||($4 IS NULL)::text$$", 0),
		fixture.Exec("CREATE AGGREGATE ua_sorted(text,boolean ORDER BY integer) (SFUNC=pg_catalog.array_append,STYPE=integer[],INITCOND='{}',FINALFUNC=ua_finish,FINALFUNC_EXTRA)", 0),
		fixture.Exec("CREATE TABLE ua_input(id integer,g integer,x integer)", 0),
		fixture.Exec("INSERT INTO ua_input VALUES(1,1,3),(2,1,NULL),(3,2,1),(4,2,2)", 4))
}

func aggregateOrderedHypothetical(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	createAggregateRun(t, p, b, c,
		fixture.Exec("CREATE FUNCTION ua_hfinal(integer,text,integer,integer) RETURNS text LANGUAGE SQL AS $$SELECT 'state='||$1::text||';prefix='||$2||';target='||$3::text||';extra='||($4 IS NULL)::text$$", 0),
		fixture.Exec("CREATE AGGREGATE ua_h(text,integer ORDER BY integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0',FINALFUNC=ua_hfinal,FINALFUNC_EXTRA,HYPOTHETICAL)", 0))
}

func aggregateOrderedSorting(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	createAggregateRun(t, p, b, c,
		fixture.Exec("CREATE FUNCTION ua_raw_final(integer[]) RETURNS text LANGUAGE SQL AS $$SELECT array_to_string($1,',','NULL')$$", 0),
		fixture.Exec("CREATE AGGREGATE ua_raw(ORDER BY integer) (SFUNC=pg_catalog.array_append,STYPE=integer[],INITCOND='{}',FINALFUNC=ua_raw_final)", 0))
	createAggregateBoth(t, p, b, c, aggregateFinalText("SELECT ua_raw() WITHIN GROUP(ORDER BY x DESC) AS v FROM(SELECT x FROM(VALUES(3),(1),(2)) s(x) ORDER BY x LIMIT 100) ordered", "1,2,3"))
}

func TestReferenceAggregateOrderedValues(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		aggregateOrderedSetup(t,p,b,c)
		for _, tc := range []struct{ q, want string }{
			{"SELECT ua_sorted('p:',false) WITHIN GROUP(ORDER BY x) AS v FROM ua_input", "p:1,2,3,NULL;extra=true"},
			{"SELECT ua_sorted('p:',true) WITHIN GROUP(ORDER BY x DESC) AS v FROM ua_input", "p:3,2,1,NULL;extra=true"},
			{"SELECT ua_sorted('a:',false) WITHIN GROUP(ORDER BY x) AS v FROM ua_input WHERE FALSE", "a:;extra=true"},
		} {
			s := aggregateFinalText(tc.q,tc.want)
			s.CheckMetadata = aggregateFinalMetadata(b.PG,false)
			createAggregateBoth(t,p,b,c,s)
		}
		aggregateOrderedHypothetical(t,p,b,c)
		createAggregateBoth(t,p,b,c,aggregateFinalText("SELECT ua_h('p:',100) WITHIN GROUP(ORDER BY x) AS v FROM(VALUES(1),(2),(3)) s(x)","state=6;prefix=p:;target=100;extra=true"))
		createAggregateRun(t,p,b,c,
			fixture.Exec("CREATE FUNCTION ua_plain_final(integer,text,integer) RETURNS text LANGUAGE SQL AS $$SELECT $2||$1::text||':'||$3::text$$",0),
			fixture.Exec("CREATE AGGREGATE ua_plain(text,integer ORDER BY integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0',FINALFUNC=ua_plain_final,HYPOTHETICAL)",0),
			fixture.Exec("CREATE AGGREGATE ua_nofinal(text ORDER BY integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')",0),
			fixture.Exec("CREATE FUNCTION ua_zero_final(integer,integer) RETURNS text LANGUAGE SQL AS $$SELECT $1::text||':'||($2 IS NULL)::text$$",0),
			fixture.Exec("CREATE AGGREGATE ua_zero(ORDER BY integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0',FINALFUNC=ua_zero_final,FINALFUNC_EXTRA)",0))
		createAggregateBoth(t,p,b,c,aggregateFinalText("SELECT ua_plain('p:',100) WITHIN GROUP(ORDER BY x) AS v FROM(VALUES(1),(2),(3)) s(x)","p:6:100"))
		createAggregateBoth(t,p,b,c,createAggregateValue("SELECT ua_nofinal('ignored') WITHIN GROUP(ORDER BY x) AS v FROM(VALUES(1),(2),(3)) s(x)",int64(6)))
		createAggregateBoth(t,p,b,c,aggregateFinalText("SELECT ua_zero() WITHIN GROUP(ORDER BY x) AS v FROM(VALUES(1),(2),(3)) s(x)","6:true"))
	})
}

func TestReferenceAggregateOrderedSorting(t *testing.T) {
	createAggregateScenario(t,aggregateOrderedSorting)
}

func TestReferenceAggregateOrderedNullEmpty(t *testing.T) {
	createAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		aggregateOrderedSetup(t,p,b,c)
		for _,tc:=range []struct{from,want string}{
			{"(VALUES(NULL::integer),(NULL::integer)) s(x)","p:NULL,NULL;extra=true"},
			{"ua_input WHERE FALSE","p:;extra=true"},
		}{ createAggregateBoth(t,p,b,c,aggregateFinalText("SELECT ua_sorted('p:',false) WITHIN GROUP(ORDER BY x) AS v FROM "+tc.from,tc.want)) }
		for _,filter:=range []string{"FALSE","NULL::boolean"}{
			createAggregateBoth(t,p,b,c,aggregateFinalText("SELECT ua_sorted('p:',false) WITHIN GROUP(ORDER BY 1/(x-x)) FILTER(WHERE "+filter+") AS v FROM ua_input","p:;extra=true"))
		}
		createAggregateRun(t,p,b,c,
			fixture.Exec("CREATE FUNCTION ua_null_step(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT coalesce($1,0)+coalesce($2,100)$$",0),
			fixture.Exec("CREATE FUNCTION ua_nullable(integer,text) RETURNS text LANGUAGE SQL AS $$SELECT coalesce($2,'directNULL')||':'||coalesce($1::text,'stateNULL')$$",0),
			fixture.Exec("CREATE FUNCTION ua_null_final(integer,integer) RETURNS text LANGUAGE SQL AS $$SELECT NULL::text$$",0),
			fixture.Exec("CREATE AGGREGATE ua_null(text ORDER BY integer) (SFUNC=ua_null_step,STYPE=integer,FINALFUNC=ua_nullable)",0),
			fixture.Exec("CREATE AGGREGATE ua_null_result(integer ORDER BY integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0',FINALFUNC=ua_null_final,HYPOTHETICAL)",0),
			fixture.Exec("CREATE AGGREGATE ua_strict(integer ORDER BY integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,FINALFUNC=pg_catalog.int4pl)",0))
		createAggregateBoth(t,p,b,c,aggregateFinalText("SELECT ua_null(NULL) WITHIN GROUP(ORDER BY x) AS v FROM(VALUES(NULL::integer),(1)) s(x)","directNULL:101"))
		createAggregateBoth(t,p,b,c,aggregateFinalText("SELECT ua_null('p') WITHIN GROUP(ORDER BY x) AS v FROM ua_input WHERE FALSE","p:stateNULL"))
		nullResult:=aggregateFinalText("SELECT ua_null_result(1) WITHIN GROUP(ORDER BY x) AS v FROM ua_input",nil)
		nullResult.CheckMetadata=aggregateFinalMetadata(b.PG,false);createAggregateBoth(t,p,b,c,nullResult)
		for _,q:=range []string{"SELECT ua_strict(NULL) WITHIN GROUP(ORDER BY x) AS v FROM ua_input","SELECT ua_strict(1) WITHIN GROUP(ORDER BY x) AS v FROM ua_input WHERE FALSE"}{createAggregateBoth(t,p,b,c,createAggregateValue(q,nil))}
		createAggregateRun(t,p,b,c,
			fixture.Exec("CREATE FUNCTION ua_break_state(integer,integer) RETURNS integer STRICT LANGUAGE SQL AS $$SELECT CASE WHEN $2=2 THEN NULL::integer ELSE coalesce($1,100)+$2 END$$",0),
			fixture.Exec("CREATE AGGREGATE ua_no_reseed(ORDER BY integer) (SFUNC=ua_break_state,STYPE=integer,INITCOND='0')",0),
			fixture.Exec("CREATE AGGREGATE ua_seeded(ORDER BY integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='7')",0),
			fixture.Exec("CREATE AGGREGATE ua_unseeded(ORDER BY integer) (SFUNC=pg_catalog.int4pl,STYPE=integer)",0))
		createAggregateBoth(t,p,b,c,createAggregateValue("SELECT ua_break_state(NULL::integer,1) AS v",nil))
		createAggregateBoth(t,p,b,c,createAggregateValue("SELECT ua_no_reseed() WITHIN GROUP(ORDER BY x DESC) AS v FROM(SELECT x FROM(VALUES(1),(2),(3)) s(x) ORDER BY x LIMIT 100) ordered",nil))
		createAggregateBoth(t,p,b,c,createAggregateValue("SELECT ua_seeded() WITHIN GROUP(ORDER BY x) AS v FROM ua_input WHERE FALSE",int64(7)))
		createAggregateBoth(t,p,b,c,createAggregateValue("SELECT ua_unseeded() WITHIN GROUP(ORDER BY x) AS v FROM ua_input WHERE FALSE",nil))
	})
}

func TestReferenceAggregateOrderedTyping(t *testing.T) {
	createAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		aggregateOrderedSetup(t,p,b,c)
		for _,s:=range []fixture.Step{
			aggregateFinalText("SELECT ua_sorted('p:',false) WITHIN GROUP(ORDER BY x) AS v FROM ua_input","p:1,2,3,NULL;extra=true"),
			fixture.Select("SELECT g,ua_sorted('p:',false) WITHIN GROUP(ORDER BY x) AS v FROM ua_input WHERE FALSE GROUP BY g",[]string{"g","v"},[]string{"INT4","TEXT"},nil),
		}{s.CheckMetadata=aggregateFinalMetadata(b.PG,false);createAggregateBoth(t,p,b,c,s)}
		createAggregateRun(t,p,b,c,
			fixture.Exec("CREATE DOMAIN ua_result AS text COLLATE \"C\"",0),
			fixture.Exec("CREATE FUNCTION ua_domain_final(integer[],text) RETURNS ua_result LANGUAGE SQL AS $$SELECT ($2||array_to_string($1,','))::ua_result$$",0),
			fixture.Exec("CREATE AGGREGATE ua_domain(text ORDER BY integer) (SFUNC=pg_catalog.array_append,STYPE=integer[],INITCOND='{}',FINALFUNC=ua_domain_final)",0),
			fixture.Exec("CREATE VIEW ua_typed AS SELECT ua_domain('p:') WITHIN GROUP(ORDER BY x) AS v FROM ua_input",0))
		createAggregateBoth(t,p,b,c,fixture.Select("SELECT p.prorettype='ua_result'::regtype AS result_type,a.aggtranstype='integer[]'::regtype AS state_type FROM(SELECT oid,prorettype FROM pg_proc WHERE proname='ua_domain') p JOIN pg_aggregate a ON a.aggfnoid=p.oid",[]string{"result_type","state_type"},[]string{"BOOL","BOOL"},[][]any{{true,true}}))
		// A collatable direct argument determines the aggregate expression's
		// collation; the result domain's declared C collation does not override it.
		createAggregateBoth(t,p,b,c,fixture.Select("SELECT a.atttypid='ua_result'::regtype AS result_type,c.collname FROM(SELECT atttypid,attcollation FROM pg_attribute WHERE attrelid='ua_typed'::regclass AND attname='v') a JOIN pg_collation c ON c.oid=a.attcollation",[]string{"result_type","collname"},[]string{"BOOL","NAME"},[][]any{{true,"default"}}))
		createAggregateRun(t,p,b,c,fixture.Exec(`CREATE VIEW ua_typed_explicit AS SELECT ua_domain('p:' COLLATE "C") WITHIN GROUP(ORDER BY x) AS v FROM ua_input`,0))
		createAggregateBoth(t,p,b,c,fixture.Select("SELECT a.atttypid='ua_result'::regtype AS result_type,c.collname FROM(SELECT atttypid,attcollation FROM pg_attribute WHERE attrelid='ua_typed_explicit'::regclass AND attname='v') a JOIN pg_collation c ON c.oid=a.attcollation",[]string{"result_type","collname"},[]string{"BOOL","NAME"},[][]any{{true,"C"}}))
		createAggregateRun(t,p,b,c,fixture.Exec("CREATE AGGREGATE ua_text_state(integer ORDER BY text) (SFUNC=pg_catalog.textcat,STYPE=text,INITCOND='')",0))
		for _,tc:=range []struct{limit int;want string}{{3,"123"},{0,""},{1,"1"},{3,"123"}} {
			s:=aggregateFinalText("SELECT ua_text_state(99) WITHIN GROUP(ORDER BY x::text DESC) AS v FROM(SELECT x FROM(VALUES(1),(2),(3)) s(x) ORDER BY x LIMIT $1) ordered",tc.want,tc.limit)
			s.CheckMetadata=aggregateFinalMetadata(b.PG,true);createAggregateBoth(t,p,b,c,s)
		}
		empty:=fixture.Select("SELECT g,ua_text_state(99) WITHIN GROUP(ORDER BY x::text) AS v FROM ua_input WHERE FALSE GROUP BY g",[]string{"g","v"},[]string{"INT4","TEXT"},nil)
		empty.CheckMetadata=aggregateFinalMetadata(b.PG,true);createAggregateBoth(t,p,b,c,empty)
	})
}
