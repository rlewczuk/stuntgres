package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAggregateOrderedAtomicity(t *testing.T) {
	for _,phase:=range []string{"step","final"}{t.Run(phase,func(t *testing.T){
		createAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
			createAggregateRun(t,p,b,c,
				fixture.Exec("CREATE TABLE ua_effect_log(v integer)",0),
				fixture.Exec("CREATE FUNCTION ua_fail_final(integer,integer) RETURNS text BEGIN ATOMIC INSERT INTO ua_effect_log VALUES($1); SELECT (12/($2-$1))::text; END",0),
				fixture.Exec("CREATE FUNCTION ua_ok_final(integer,integer) RETURNS text LANGUAGE SQL AS $$SELECT (12/($2-$1))::text$$",0),
				fixture.Exec("CREATE FUNCTION ua_fail_step(integer,integer) RETURNS integer BEGIN ATOMIC INSERT INTO ua_effect_log VALUES($2); SELECT $1+12/(6-$2); END",0),
				fixture.Exec("CREATE TABLE ua_error_source(g integer,x integer)",0),
				fixture.Exec("INSERT INTO ua_error_source VALUES(1,1),(1,2),(2,6)",3),
				fixture.Exec("CREATE TABLE ua_error_destination(g integer,v text)",0),
				fixture.Exec("INSERT INTO ua_error_destination VALUES(9,'old')",1))
			step,final,contextName:="pg_catalog.int4pl","ua_fail_final","ua_fail_final"
			if phase=="step" {step,final,contextName="ua_fail_step","ua_ok_final","ua_fail_step"}
			createAggregateRun(t,p,b,c,fixture.Exec("CREATE AGGREGATE ua_fail(integer ORDER BY integer) (SFUNC="+step+",STYPE=integer,INITCOND='0',FINALFUNC="+final+")",0))
			const q="SELECT g,ua_fail(6) WITHIN GROUP(ORDER BY x) AS v FROM ua_error_source WHERE g<=$1 GROUP BY g ORDER BY v,g"
			stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close();held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
			for _,prepared:=range []bool{false,true}{
				d:=fixture.Diagnostic{Code:"22012",Message:"division by zero",Context:"SQL function \""+contextName+"\" statement 2"}
				s:=fixture.ImplicitError(q,d,2);s.SelectRows=true;s.ErrorStage="execute"
				if prepared {fixture.RunStep(t,p.Ctx,held,b.Recorder,s)} else {fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
				insert:=fixture.ImplicitError("INSERT INTO ua_error_destination SELECT g,ua_fail(6) WITHIN GROUP(ORDER BY x) FROM ua_error_source WHERE g<=$1 GROUP BY g",d,2);insert.Prepared=prepared;insert.ErrorStage="execute";fixture.RunStep(t,p.Ctx,c,b.Recorder,insert)
				createAggregateRun(t,p,b,c,fixture.Select("SELECT count(*) AS n FROM ua_effect_log",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}),fixture.Select("SELECT g,v FROM ua_error_destination",[]string{"g","v"},[]string{"INT4","TEXT"},[][]any{{int64(9),"old"}}))
				want:="4";effects:=int64(1);if phase=="step" {want="12";effects=2}
				fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(q,[]string{"g","v"},[]string{"INT4","TEXT"},[][]any{{int64(1),want}},1))
				createAggregateRun(t,p,b,c,fixture.Select("SELECT count(*) AS n FROM ua_effect_log",[]string{"n"},[]string{"INT8"},[][]any{{effects}}),fixture.Exec("DELETE FROM ua_effect_log",effects))
			}
			if !b.PG {
				ctx,cancel:=context.WithCancel(p.Ctx);cancel()
				_,err:=c.ExecContext(ctx,"CREATE AGGREGATE ua_rejected(ORDER BY integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')")
				if !errors.Is(err,context.Canceled){t.Fatalf("canceled CREATE=%v",err)};aggregateOrderedNoResidue(t,p,b,c)
			}
		})
	})}
}

func TestReferenceAggregateOrderedCancellation(t *testing.T) {
	createAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		createAggregateRun(t,p,b,c,
			fixture.Exec("CREATE TABLE ua_cancel_log(v integer)",0),
			fixture.Exec("CREATE FUNCTION ua_sleep_final(integer,integer) RETURNS text BEGIN ATOMIC INSERT INTO ua_cancel_log VALUES($1); SELECT CASE WHEN pg_sleep(CASE WHEN $1>$2 THEN 30 ELSE 0 END) IS NULL THEN $1::text ELSE $1::text END; END",0),
			fixture.Exec("CREATE AGGREGATE ua_sleep(integer ORDER BY integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0',FINALFUNC=ua_sleep_final)",0),
			fixture.Exec("CREATE TABLE ua_cancel_source(x integer)",0),
			fixture.Exec("INSERT INTO ua_cancel_source VALUES(1),(2)",2),
			fixture.Exec("CREATE TABLE ua_cancel_destination(v text)",0))
		const q="INSERT INTO ua_cancel_destination SELECT ua_sleep(1) WITHIN GROUP(ORDER BY x) FROM ua_cancel_source WHERE x<=$1"
		stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close();held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
		for _,prepared:=range []bool{false,true}{
			fixture.SleepActiveCancelWithContext(t,p,b,c,q,"SQL function \"ua_sleep_final\" statement 2",nil,func(ctx context.Context) fixture.RelationOutcome {
				var result sql.Result;var err error
				if prepared {result,err=stmt.ExecContext(ctx,2)} else {result,err=c.ExecContext(ctx,q,2)}
				return fixture.RelationOutcome{Result:result,Err:err}
			})
			for _,table:=range []string{"ua_cancel_log","ua_cancel_destination"}{createAggregateRun(t,p,b,c,fixture.Select("SELECT count(*) AS n FROM "+table,[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))}
			fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Exec(q,1,1))
			createAggregateRun(t,p,b,c,aggregateFinalText("SELECT v FROM ua_cancel_destination","1"),fixture.Select("SELECT v FROM ua_cancel_log",[]string{"v"},[]string{"INT4"},[][]any{{int64(1)}}),fixture.Exec("DELETE FROM ua_cancel_destination",1),fixture.Exec("DELETE FROM ua_cancel_log",1))
		}
	})
}
