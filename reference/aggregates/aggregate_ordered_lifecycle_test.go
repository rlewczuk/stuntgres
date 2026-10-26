package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func aggregateOrderedHeld(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	const q="SELECT ua_sorted($2,$3) WITHIN GROUP(ORDER BY x) AS v FROM(SELECT x FROM(VALUES(1),(2),(3)) s(x) ORDER BY x LIMIT $1) ordered"
	stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close()
	held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
	for _,tc:=range []struct{limit int64;prefix string;desc bool;want string}{
		{3,"p:",false,"p:1,2,3;extra=true"},{0,"p:",false,"p:;extra=true"},{1,"q:",true,"q:1;extra=true"},{3,"r:",true,"r:3,2,1;extra=true"},{3,"p:",false,"p:1,2,3;extra=true"},
	}{
		s:=aggregateFinalText(q,tc.want,tc.limit,tc.prefix,tc.desc);s.CheckMetadata=aggregateFinalMetadata(b.PG,false)
		createAggregateBoth(t,p,b,c,s);fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
	}
}

func TestReferenceAggregateOrderedLifecycle(t *testing.T) {
	createAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		aggregateOrderedSetup(t,p,b,c);aggregateOrderedHeld(t,p,b,c)
		const q="SELECT ua_sorted($1,$2) WITHIN GROUP(ORDER BY x) AS v FROM ua_input"
		stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close()
		held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
		fixture.RunStep(t,p.Ctx,held,b.Recorder,aggregateFinalText(q,"p:1,2,3,NULL;extra=true","p:",false))
		createAggregateRun(t,p,b,c,fixture.Exec("INSERT INTO ua_input VALUES(5,2,4)",1))
		fixture.RunStep(t,p.Ctx,held,b.Recorder,aggregateFinalText(q,"q:4,3,2,1,NULL;extra=true","q:",true))
		createAggregateRun(t,p,b,c,
			fixture.Exec("DROP AGGREGATE ua_sorted(text,boolean ORDER BY integer)",0),
			fixture.Exec("DROP FUNCTION ua_finish(integer[],text,boolean,integer)",0),
			fixture.Exec("CREATE FUNCTION ua_finish(integer[],text,boolean,integer) RETURNS text LANGUAGE SQL AS $$SELECT 'new:'||$2||array_to_string(array_sort($1,$3,false),',','NULL')||';extra='||($4 IS NULL)::text$$",0),
			fixture.Exec("CREATE AGGREGATE ua_sorted(text,boolean ORDER BY integer) (SFUNC=pg_catalog.array_append,STYPE=integer[],INITCOND='{}',FINALFUNC=ua_finish,FINALFUNC_EXTRA)",0))
		fixture.RunStep(t,p.Ctx,held,b.Recorder,aggregateFinalText(q,"new:p:1,2,3,4,NULL;extra=true","p:",false))
	})
}

func TestReferenceAggregateOrderedFeatureSmoke(t *testing.T) {
	createAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		aggregateOrderedSetup(t,p,b,c)
		s:=aggregateFinalText("SELECT ua_sorted('p:',false) WITHIN GROUP(ORDER BY x) AS v FROM ua_input","p:1,2,3,NULL;extra=true");s.CheckMetadata=aggregateFinalMetadata(b.PG,false);createAggregateBoth(t,p,b,c,s)
		createAggregateBoth(t,p,b,c,aggregateFinalText("SELECT ua_sorted('p:',false) WITHIN GROUP(ORDER BY x) FILTER(WHERE FALSE) AS v FROM ua_input","p:;extra=true"))
		aggregateOrderedSorting(t,p,b,c);aggregateOrderedHypothetical(t,p,b,c)
		createAggregateBoth(t,p,b,c,aggregateFinalText("SELECT ua_h('p:',100) WITHIN GROUP(ORDER BY x) AS v FROM(VALUES(1),(2),(3)) s(x)","state=6;prefix=p:;target=100;extra=true"))
		aggregateOrderedRanks(t,p,b,c)
		const rankSQL="SELECT ua_rank($1) WITHIN GROUP(ORDER BY x) AS v FROM(VALUES(10),(20),(20),(40)) s(x) WHERE $2::boolean"
		stmt,err:=c.PrepareContext(p.Ctx,rankSQL);if err!=nil {t.Fatal(err)};defer stmt.Close()
		held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:rankSQL}
		for _,tc:=range []struct{populated bool;want int64}{{true,2},{false,1}} {
			s:=fixture.Select(rankSQL,[]string{"v"},[]string{"INT8"},[][]any{{tc.want}},20,tc.populated);createAggregateBoth(t,p,b,c,s);fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
		}
		createAggregateBoth(t,p,b,c,fixture.Select("SELECT ua_rank(1,'b') WITHIN GROUP(ORDER BY x,y) AS v FROM(VALUES(1,'a'::text),(1,'b'),(1,'b'),(2,'a'),(NULL,'z')) s(x,y)",[]string{"v"},[]string{"INT8"},[][]any{{int64(2)}}))
		q:="SELECT ua_rank(1) WITHIN GROUP(ORDER BY x,g) FROM ua_input"
		orderedSetBoth(t,p,c,b,orderedSetDiagnostic(q,"42883","function ua_rank(integer, integer, integer) does not exist","ua_rank","To use the hypothetical-set aggregate ua_rank, the number of hypothetical direct arguments (here 1) must match the number of ordering columns (here 2)."))
		fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(rankSQL,[]string{"v"},[]string{"INT8"},[][]any{{int64(2)}},20,true))
		aggregateOrderedHeld(t,p,b,c)
		createAggregateRun(t,p,b,c,fixture.Exec("CREATE VIEW ua_smoke_view AS SELECT ua_sorted('v:',false) WITHIN GROUP(ORDER BY x) AS v FROM ua_input",0))
		if !b.PG {
			var dump bytes.Buffer
			if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil {t.Fatal(err)}
			if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil {t.Fatal(err)}
			if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil {t.Fatal(err)}
			createAggregateBoth(t,p,b,c,aggregateFinalText("SELECT v FROM ua_smoke_view","v:1,2,3,NULL;extra=true"))
			createAggregateBoth(t,p,b,c,aggregateFinalText("SELECT ua_sorted('p:',false) WITHIN GROUP(ORDER BY x) AS v FROM ua_input","p:1,2,3,NULL;extra=true"))
			fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(rankSQL,[]string{"v"},[]string{"INT8"},[][]any{{int64(2)}},20,true))
		}
	})
}
