package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func aggregateOrderedRanks(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	for _,tc:=range []struct{name,final string}{{"ua_rank","rank_final"},{"ua_dense","dense_rank_final"},{"ua_percent","percent_rank_final"},{"ua_cume","cume_dist_final"}} {
		createAggregateRun(t,p,b,c,fixture.Exec("CREATE AGGREGATE "+tc.name+"(VARIADIC \"any\" ORDER BY VARIADIC \"any\") (SFUNC=pg_catalog.ordered_set_transition_multi,STYPE=internal,FINALFUNC=pg_catalog."+tc.final+",FINALFUNC_EXTRA,HYPOTHETICAL)",0))
	}
}

func aggregateOrderedRankRow(q string, values ...any) fixture.Step {
	return fixture.Select(q,[]string{"r","d","p","c"},[]string{"INT8","INT8","FLOAT8","FLOAT8"},[][]any{values})
}

func TestReferenceAggregateOrderedVariadic(t *testing.T) {
	createAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		aggregateOrderedRanks(t,p,b,c)
		const scalar="SELECT ua_rank(20) WITHIN GROUP(ORDER BY x) AS r,ua_dense(20) WITHIN GROUP(ORDER BY x) AS d,ua_percent(20) WITHIN GROUP(ORDER BY x) AS p,ua_cume(20) WITHIN GROUP(ORDER BY x) AS c FROM(VALUES(10),(20),(20),(40)) s(x)"
		createAggregateBoth(t,p,b,c,aggregateOrderedRankRow(scalar,int64(2),int64(2),.25,.8))
		createAggregateBoth(t,p,b,c,aggregateOrderedRankRow(scalar+" WHERE FALSE",int64(1),int64(1),float64(0),float64(1)))
		const source=" FROM(VALUES(1,'a'::text),(1,'b'),(1,'b'),(2,'a'),(NULL,'z')) s(x,y)"
		for _,tc:=range []struct{direction string;r,d int64;p,c float64}{{"ASC",2,2,.2,4.0/6},{"DESC",1,1,0,.5}} {
			q:="SELECT ua_rank(1,'b') WITHIN GROUP(ORDER BY x ASC NULLS LAST,y "+tc.direction+") AS r,ua_dense(1,'b') WITHIN GROUP(ORDER BY x ASC NULLS LAST,y "+tc.direction+") AS d,ua_percent(1,'b') WITHIN GROUP(ORDER BY x ASC NULLS LAST,y "+tc.direction+") AS p,ua_cume(1,'b') WITHIN GROUP(ORDER BY x ASC NULLS LAST,y "+tc.direction+") AS c"+source
			createAggregateBoth(t,p,b,c,aggregateOrderedRankRow(q,tc.r,tc.d,tc.p,tc.c))
		}
		createAggregateBoth(t,p,b,c,aggregateOrderedRankRow("SELECT ua_rank(NULL::integer) WITHIN GROUP(ORDER BY x NULLS LAST) AS r,ua_dense(NULL::integer) WITHIN GROUP(ORDER BY x NULLS LAST) AS d,ua_percent(NULL::integer) WITHIN GROUP(ORDER BY x NULLS LAST) AS p,ua_cume(NULL::integer) WITHIN GROUP(ORDER BY x NULLS LAST) AS c FROM(VALUES(10),(NULL::integer),(20)) s(x)",int64(3),int64(3),2.0/3,1.0))
		createAggregateBoth(t,p,b,c,fixture.Select("SELECT g,ua_rank(20) WITHIN GROUP(ORDER BY x) AS v FROM(VALUES(1,10),(1,20),(2,30),(2,40)) s(g,x) GROUP BY g ORDER BY g",[]string{"g","v"},[]string{"INT4","INT8"},[][]any{{int64(1),int64(2)},{int64(2),int64(1)}}))
		createAggregateBoth(t,p,b,c,fixture.Select("SELECT ua_rank(1,1) WITHIN GROUP(ORDER BY x,x) AS v FROM(VALUES(1),(2)) s(x)",[]string{"v"},[]string{"INT8"},[][]any{{int64(1)}}))
		createAggregateRun(t,p,b,c,
			fixture.Exec("CREATE DOMAIN ua_key AS integer",0),
			fixture.Exec("CREATE AGGREGATE ua_rows_only(ORDER BY VARIADIC \"any\") (SFUNC=pg_catalog.ordered_set_transition_multi,STYPE=internal,FINALFUNC=pg_catalog.rank_final,FINALFUNC_EXTRA)",0),
			fixture.Exec("CREATE AGGREGATE \"ua_rank_λ\"(VARIADIC \"any\" ORDER BY VARIADIC \"any\") (SFUNC=pg_catalog.ordered_set_transition_multi,STYPE=internal,FINALFUNC=pg_catalog.dense_rank_final,FINALFUNC_EXTRA,HYPOTHETICAL)",0))
		createAggregateBoth(t,p,b,c,fixture.Select("SELECT ua_rank('20') WITHIN GROUP(ORDER BY x::ua_key) AS v FROM(VALUES(10),(20),(40)) s(x)",[]string{"v"},[]string{"INT8"},[][]any{{int64(2)}}))
		createAggregateBoth(t,p,b,c,fixture.Select("SELECT public.\"ua_rank_λ\"(30) WITHIN GROUP(ORDER BY x) AS v FROM(VALUES(10),(10),(20)) s(x)",[]string{"v"},[]string{"INT8"},[][]any{{int64(3)}}))
		for _,tc:=range []struct{name,final,kind string;direct int64}{{"ua_rank","rank_final","h",1},{"ua_dense","dense_rank_final","h",1},{"ua_percent","percent_rank_final","h",1},{"ua_cume","cume_dist_final","h",1}} {
			createAggregateBoth(t,p,b,c,fixture.Select("SELECT p.pronargs::integer AS nargs,p.provariadic=2276::oid AS variadic,a.aggkind::text AS kind,a.aggnumdirectargs::integer AS direct,a.aggfinalextra,a.aggfinalmodify::text AS modify,a.aggtransfn='pg_catalog.ordered_set_transition_multi(internal,\"any\")'::regprocedure AS step,a.aggfinalfn='pg_catalog."+tc.final+"(internal,\"any\")'::regprocedure AS final FROM(SELECT oid,pronargs,provariadic FROM pg_proc WHERE proname='"+tc.name+"') p JOIN pg_aggregate a ON a.aggfnoid=p.oid",[]string{"nargs","variadic","kind","direct","aggfinalextra","modify","step","final"},[]string{"INT4","BOOL","TEXT","INT4","BOOL","TEXT","BOOL","BOOL"},[][]any{{tc.direct,true,tc.kind,tc.direct,true,"w",true,true}}))
		}
		createAggregateBoth(t,p,b,c,fixture.Select("SELECT a.aggkind::text AS kind,a.aggnumdirectargs::integer AS direct,p.pronargs::integer AS nargs FROM(SELECT oid,pronargs FROM pg_proc WHERE proname='ua_rows_only') p JOIN pg_aggregate a ON a.aggfnoid=p.oid",[]string{"kind","direct","nargs"},[]string{"TEXT","INT4","INT4"},[][]any{{"o",int64(0),int64(1)}}))
		badPrefix:=fixture.ImplicitError(`CREATE AGGREGATE ua_rejected(integer,VARIADIC "any" ORDER BY VARIADIC "any") (SFUNC=pg_catalog.ordered_set_transition_multi,STYPE=internal,FINALFUNC=pg_catalog.rank_final,FINALFUNC_EXTRA,HYPOTHETICAL)`,fixture.Diagnostic{Code:"42883",Message:`function pg_catalog.rank_final(internal, integer, "any") does not exist`})
		badPrefix.ErrorStage="execute";createAggregateBoth(t,p,b,c,badPrefix);aggregateOrderedNoResidue(t,p,b,c)
		// A fixed leading direct parameter is legal with an actual matching
		// support signature; rank_final itself does not accept that prefix.
		aggregateOrderedHypothetical(t,p,b,c)
		createAggregateRun(t,p,b,c,fixture.Exec("CREATE AGGREGATE ua_rejected(text,integer ORDER BY integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0',FINALFUNC=ua_hfinal,FINALFUNC_EXTRA,HYPOTHETICAL)",0))
		createAggregateBoth(t,p,b,c,aggregateFinalText("SELECT ua_rejected('p:',100) WITHIN GROUP(ORDER BY x) AS v FROM(VALUES(1),(2),(3)) s(x)","state=6;prefix=p:;target=100;extra=true"))
		const heldSQL="SELECT ua_rank($1) WITHIN GROUP(ORDER BY x) AS v FROM(SELECT x FROM(VALUES(10),(20),(20),(40)) s(x) ORDER BY x LIMIT $2) ordered"
		stmt,err:=c.PrepareContext(p.Ctx,heldSQL);if err!=nil {t.Fatal(err)};defer stmt.Close()
		held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:heldSQL}
		for _,tc:=range []struct{target,limit,want int64}{{20,4,2},{20,0,1},{40,1,2},{10,4,1},{20,4,2}} {
			s:=fixture.Select(heldSQL,[]string{"v"},[]string{"INT8"},[][]any{{tc.want}},tc.target,tc.limit)
			createAggregateBoth(t,p,b,c,s);fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
		}
		bad:=fixture.ImplicitError(heldSQL,fixture.Diagnostic{Code:"22P02",Message:"invalid input syntax for type integer: \"bad\"",Context:"unnamed portal parameter $1 = '...'"},"bad",4)
		bad.SelectRows=true;bad.ErrorStage="execute";fixture.RunStep(t,p.Ctx,held,b.Recorder,bad)
		fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(heldSQL,[]string{"v"},[]string{"INT8"},[][]any{{int64(2)}},20,4))
		createAggregateBoth(t,p,b,c,fixture.Select(`SELECT oid='ua_rank("any")'::regprocedure AS v FROM pg_proc WHERE proname='ua_rank'`,[]string{"v"},[]string{"BOOL"},[][]any{{true}}))
		createAggregateRun(t,p,b,c,fixture.Exec(`CREATE AGGREGATE ua_named_rank(VARIADIC direct_values "any" ORDER BY VARIADIC row_values "any") (SFUNC=pg_catalog.ordered_set_transition_multi,STYPE=internal,FINALFUNC=pg_catalog.rank_final,FINALFUNC_EXTRA,HYPOTHETICAL)`,0))
		createAggregateBoth(t,p,b,c,fixture.Select("SELECT pronargs::integer AS nargs,proargnames::text AS names FROM pg_proc WHERE proname='ua_named_rank'",[]string{"nargs","names"},[]string{"INT4","TEXT"},[][]any{{int64(1),"{direct_values}"}}))
		createAggregateBoth(t,p,b,c,fixture.Select("SELECT ua_named_rank(20) WITHIN GROUP(ORDER BY x) AS v FROM(VALUES(10),(20),(40)) s(x)",[]string{"v"},[]string{"INT8"},[][]any{{int64(2)}}))
	})
}
