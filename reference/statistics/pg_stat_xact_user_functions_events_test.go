package reference_test

import (
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatXactUserFunctionsTracking(t *testing.T) {
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xactFunctionCreate(t,p,b,c,"xf_tracking","RETURN 7;")
		w,retire:=fixture.StatisticsReportingConn(t,p,b);defer retire();xactFunctionBegin(t,p,b,w)
		fixture.StatisticsExec(t,p,b,w,"SET track_functions=none",0);xactFunctionCall(t,p,b,w,"xf_tracking",7);xactFunctionAbsent(t,p,b,w,"xf_tracking")
		fixture.StatisticsExec(t,p,b,w,"SET track_functions=pl",0)
		held:=fixture.IdentityHeld(t,p.Ctx,w,xactFunctionSQL);defer held.Statement.Close();executor:=held
		for i:=range 2 {xactFunctionCall(t,p,b,w,"xf_tracking",7);for _,q:=range []fixture.Executor{w,executor}{r:=xactFunctionGet(t,p,q,"xf_tracking");xactFunctionCounts(t,b,r,int64(i+1),float64(i+1),float64(i+1));var oid int64;if err:=w.QueryRowContext(p.Ctx,"SELECT 'xf_tracking()'::regprocedure::oid").Scan(&oid);err!=nil||r.ID!=oid||r.Schema!="public"||r.Name!="xf_tracking"{t.Fatal("current procedure identity",r,oid,err)}}}
		fixture.StatisticsExec(t,p,b,w,"SET track_functions=none",0);xactFunctionCall(t,p,b,w,"xf_tracking",7);xactFunctionCounts(t,b,xactFunctionGet(t,p,w,"xf_tracking"),2,2,2)
		fixture.StatisticsExec(t,p,b,w,"SET track_functions=pl",0);xactFunctionCall(t,p,b,w,"xf_tracking",7);xactFunctionCounts(t,b,xactFunctionGet(t,p,w,"xf_tracking"),3,3,3)
		fixture.StatisticsExec(t,p,b,w,"CREATE FUNCTION xf_sql() RETURNS integer LANGUAGE sql VOLATILE AS 'SELECT 8'",0)
		fixture.StatisticsExec(t,p,b,w,"CREATE FUNCTION xf_configured() RETURNS integer LANGUAGE sql VOLATILE SET track_functions='all' AS 'SELECT 8'",0)
		xactFunctionCall(t,p,b,w,"xf_sql",8);xactFunctionAbsent(t,p,b,w,"xf_sql");xactFunctionCall(t,p,b,w,"xf_configured",8);xactFunctionCounts(t,b,xactFunctionGet(t,p,w,"xf_configured"),1,1,1)
		fixture.StatisticsExec(t,p,b,w,"CREATE FUNCTION xf_strict(integer) RETURNS integer LANGUAGE plpgsql STRICT AS $$BEGIN RETURN 1; END$$",0)
		fixture.RunStep(t,p.Ctx,w,b.Recorder,fixture.Select("SELECT xf_strict(NULL)",[]string{"xf_strict"},[]string{"INT4"},[][]any{{nil}}));xactFunctionAbsent(t,p,b,w,"xf_strict")
		fixture.RunStep(t,p.Ctx,w,b.Recorder,fixture.Select("SELECT abs(-1)",[]string{"abs"},[]string{"INT4"},[][]any{{int64(1)}}));xactFunctionAbsent(t,p,b,w,"abs")
		fixture.StatisticsExec(t,p,b,w,"CREATE TABLE xf_trigger_table(n integer)",0)
		fixture.StatisticsExec(t,p,b,w,"CREATE FUNCTION xf_trigger() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",0)
		fixture.StatisticsExec(t,p,b,w,"CREATE TRIGGER xf_tr BEFORE INSERT ON xf_trigger_table FOR EACH ROW EXECUTE FUNCTION xf_trigger()",0)
		fixture.StatisticsExec(t,p,b,w,"INSERT INTO xf_trigger_table VALUES(1)",1);xactFunctionCounts(t,b,xactFunctionGet(t,p,w,"xf_trigger"),1,1,1)
		fixture.StatisticsExec(t,p,b,w,"CREATE EXTENSION citext",0)
		fixture.StatisticsExec(t,p,b,w,"CREATE TABLE xf_inline(s citext,p citext)",0)
		fixture.StatisticsExec(t,p,b,w,"INSERT INTO xf_inline VALUES('AbA','a')",1)
		fixture.StatisticsExec(t,p,b,w,"SET track_functions='all'",0)
		rows,err:=w.QueryContext(p.Ctx,"SELECT public.regexp_match(s,p) FROM xf_inline");if err!=nil{t.Fatal(err)};for rows.Next(){var value any;if err:=rows.Scan(&value);err!=nil{t.Fatal(err)}};if rows.Err()!=nil{t.Fatal(rows.Err())};rows.Close()
		fixture.RunStep(t,p.Ctx,w,b.Recorder,fixture.Select("SELECT count(*) FROM pg_stat_xact_user_functions WHERE funcid='public.regexp_match(citext,citext)'::regprocedure::oid",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
		fixture.StatisticsExec(t,p,b,w,"ROLLBACK",0)
	})
}

func TestReferencePGStatXactUserFunctionsCurrentState(t *testing.T) {
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xactFunctionCreate(t,p,b,c,"xf_current","RETURN 7;")
		for _,mode:=range []string{"none","cache","snapshot"}{
			w,retire:=fixture.StatisticsReportingConn(t,p,b);xactFunctionBegin(t,p,b,w);fixture.StatisticsExec(t,p,b,w,"SET stats_fetch_consistency="+mode,0)
			held:=fixture.IdentityHeld(t,p.Ctx,w,xactFunctionSQL);q:=held
			xactFunctionCall(t,p,b,w,"xf_current",7);old:=xactFunctionGet(t,p,q,"xf_current");xactFunctionCounts(t,b,old,1,1,1)
			xactFunctionCall(t,p,b,w,"xf_current",7);xactFunctionCounts(t,b,xactFunctionGet(t,p,q,"xf_current"),2,2,2)
			fixture.StatisticsClearSnapshot(t,p,b,w);xactFunctionCounts(t,b,xactFunctionGet(t,p,w,"xf_current"),2,2,2)
			var cut sql.NullTime;if err:=w.QueryRowContext(p.Ctx,"SELECT pg_stat_get_snapshot_timestamp()").Scan(&cut);err!=nil||cut.Valid{t.Fatal("xact read created cumulative cut",cut,err)}
			xactFunctionAbsent(t,p,b,c,"xf_current");if old.Calls!=1{t.Fatal("retained row changed",old)}
			held.Statement.Close();fixture.StatisticsExec(t,p,b,w,"ROLLBACK",0);retire()
		}
	})
}

func TestReferencePGStatXactUserFunctionsNesting(t *testing.T) {
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xactFunctionCreate(t,p,b,c,"xf_leaf","PERFORM pg_sleep(0.005); RETURN 7;")
		xactFunctionCreate(t,p,b,c,"xf_parent","PERFORM xf_leaf(); RETURN 9;")
		w,retire:=fixture.StatisticsReportingConn(t,p,b);defer retire();xactFunctionBegin(t,p,b,w);xactFunctionCall(t,p,b,w,"xf_parent",9)
		parent,leaf:=xactFunctionGet(t,p,w,"xf_parent"),xactFunctionGet(t,p,w,"xf_leaf")
		xactFunctionCounts(t,b,parent,1,2,1);xactFunctionCounts(t,b,leaf,1,1,1)
		if b.PG&&parent.Total<=parent.Self{t.Fatal("actual sleeping child not included",parent,leaf)}
		fixture.StatisticsExec(t,p,b,w,"ROLLBACK",0)
	})
}

func TestReferencePGStatXactUserFunctionsPresence(t *testing.T) {
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xactFunctionCreate(t,p,b,c,"xf_observe","RETURN (SELECT calls::integer FROM pg_stat_xact_user_functions WHERE funcname='xf_observe');")
		xactFunctionCreate(t,p,b,c,"xf_fail","RAISE EXCEPTION 'xf failure';")
		xactFunctionCreate(t,p,b,c,"xf_unused","RETURN 7;")
		xactFunctionCreate(t,p,b,c,"xf_survivor","RETURN 7;")
		xactFunctionCreate(t,p,b,c,"xf_outer_fail","PERFORM xf_survivor(); RAISE EXCEPTION 'xf outer failure';")
		w,retire:=fixture.StatisticsReportingConn(t,p,b);defer retire();xactFunctionBegin(t,p,b,w)
		xactFunctionCall(t,p,b,w,"xf_observe",0);xactFunctionCounts(t,b,xactFunctionGet(t,p,w,"xf_observe"),1,1,1)
		for _,name:=range []string{"xf_fail","xf_outer_fail"}{
			fixture.StatisticsExec(t,p,b,w,"SAVEPOINT failed",0)
			rows,err:=w.QueryContext(p.Ctx,"SELECT "+name+"()");if rows!=nil{rows.Close()};if err==nil{t.Fatal("body did not fail")}
			var state interface{SQLState()string};if !errors.As(err,&state)||state.SQLState()!="P0001"{t.Fatal("body error",err)}
			fixture.StatisticsExec(t,p,b,w,"ROLLBACK TO failed",0);fixture.StatisticsExec(t,p,b,w,"RELEASE failed",0)
			r:=xactFunctionGet(t,p,w,name);xactFunctionCounts(t,b,r,0,0,0);if r.Total!=0||r.Self!=0{t.Fatal("failed body fabricated timing",r)}
		}
		xactFunctionCounts(t,b,xactFunctionGet(t,p,w,"xf_survivor"),1,1,1);xactFunctionAbsent(t,p,b,w,"xf_unused")
		fixture.StatisticsExec(t,p,b,w,"ROLLBACK",0)
	})
}

func TestReferencePGStatXactUserFunctionsTransactions(t *testing.T) {
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xactFunctionCreate(t,p,b,c,"xf_transaction","RETURN 7;")
		for _,end:=range []string{"COMMIT","ROLLBACK"}{
			w,retire:=userFunctionFreshReporter(t,p,b);xactFunctionBegin(t,p,b,w)
			xactFunctionCall(t,p,b,w,"xf_transaction",7);fixture.StatisticsExec(t,p,b,w,"SAVEPOINT child",0);xactFunctionCall(t,p,b,w,"xf_transaction",7)
			fixture.StatisticsExec(t,p,b,w,"ROLLBACK TO child",0);xactFunctionCounts(t,b,xactFunctionGet(t,p,w,"xf_transaction"),2,2,2)
			fixture.StatisticsExec(t,p,b,w,"RELEASE child",0);xactFunctionCounts(t,b,xactFunctionGet(t,p,w,"xf_transaction"),2,2,2)
			xactTableEnd(t,p,b,w,end);fixture.StatisticsExec(t,p,b,w,"BEGIN",0);xactFunctionAbsent(t,p,b,w,"xf_transaction");fixture.StatisticsExec(t,p,b,w,"ROLLBACK",0);retire()
		}
	})
}
