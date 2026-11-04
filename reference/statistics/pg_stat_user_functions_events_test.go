package reference_test

import (
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatUserFunctionsTracking(t *testing.T) {
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xactFunctionCreate(t,p,b,c,"uf_tracking","RETURN 7;")
		w,retire:=fixture.StatisticsReportingConn(t,p,b);xactFunctionBegin(t,p,b,w)
		fixture.StatisticsExec(t,p,b,w,"SET track_functions=none",0);xactFunctionCall(t,p,b,w,"uf_tracking",7)
		fixture.StatisticsExec(t,p,b,w,"COMMIT",0);retire();userFunctionAbsent(t,p,b,c,"uf_tracking")
		userFunctionPublish(t,p,b,"uf_tracking",2,2)
		held:=fixture.IdentityHeld(t,p.Ctx,c,userFunctionSQL);defer held.Statement.Close()
		for _,q:=range []fixture.Executor{c,held}{r:=userFunctionGet(t,p,q,"uf_tracking");xactFunctionCounts(t,b,r,2,2,2);var oid int64;if err:=c.QueryRowContext(p.Ctx,"SELECT 'uf_tracking()'::regprocedure::oid").Scan(&oid);err!=nil||r.ID!=oid||r.Schema!="public"||r.Name!="uf_tracking"{t.Fatal("exact identity",r,oid,err)}}
		w,retire=fixture.StatisticsReportingConn(t,p,b);xactFunctionBegin(t,p,b,w);fixture.StatisticsExec(t,p,b,w,"SET track_functions=none",0);xactFunctionCall(t,p,b,w,"uf_tracking",7);fixture.StatisticsExec(t,p,b,w,"COMMIT",0);retire();fixture.StatisticsClearSnapshot(t,p,b,c);xactFunctionCounts(t,b,userFunctionGet(t,p,c,"uf_tracking"),2,2,2)
		userFunctionPublish(t,p,b,"uf_tracking",1,3);fixture.StatisticsClearSnapshot(t,p,b,c);xactFunctionCounts(t,b,userFunctionGet(t,p,held,"uf_tracking"),3,3,3)
		// Eligibility is decided in the installed body environment, including
		// configured SQL, strict NULL, actual trigger entry and SQL inlining.
		for _,q:=range []string{
			"CREATE FUNCTION uf_sql() RETURNS integer LANGUAGE sql VOLATILE AS 'SELECT 8'",
			"CREATE FUNCTION uf_configured() RETURNS integer LANGUAGE sql VOLATILE SET track_functions='all' AS 'SELECT 8'",
			"CREATE FUNCTION uf_strict(integer) RETURNS integer LANGUAGE plpgsql STRICT AS $$BEGIN RETURN 1; END$$",
			"CREATE TABLE uf_trigger_table(n integer)",
			"CREATE FUNCTION uf_trigger() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
			"CREATE TRIGGER uf_tr BEFORE INSERT ON uf_trigger_table FOR EACH ROW EXECUTE FUNCTION uf_trigger()",
			"CREATE EXTENSION citext","CREATE TABLE uf_inline(s citext,p citext)","INSERT INTO uf_inline VALUES('AbA','a')",
		}{count:=int64(0);if q=="INSERT INTO uf_inline VALUES('AbA','a')"{count=1};fixture.StatisticsExec(t,p,b,c,q,count)}
		w,retire=fixture.StatisticsReportingConn(t,p,b);xactFunctionBegin(t,p,b,w)
		xactFunctionCall(t,p,b,w,"uf_sql",8);xactFunctionCall(t,p,b,w,"uf_configured",8)
		fixture.RunStep(t,p.Ctx,w,b.Recorder,fixture.Select("SELECT uf_strict(NULL),abs(-1)",[]string{"uf_strict","abs"},[]string{"INT4","INT4"},[][]any{{nil,int64(1)}}))
		fixture.StatisticsExec(t,p,b,w,"INSERT INTO uf_trigger_table VALUES(1)",1);fixture.StatisticsExec(t,p,b,w,"SET track_functions='all'",0)
		rows,err:=w.QueryContext(p.Ctx,"SELECT public.regexp_match(s,p) FROM uf_inline");if err!=nil{t.Fatal(err)};for rows.Next(){var v any;if err:=rows.Scan(&v);err!=nil{t.Fatal(err)}};if rows.Err()!=nil{t.Fatal(rows.Err())};rows.Close()
		fixture.StatisticsExec(t,p,b,w,"COMMIT",0);retire();userFunctionAwait(t,p,b,"uf_configured",1);userFunctionAwait(t,p,b,"uf_trigger",1);fixture.StatisticsClearSnapshot(t,p,b,c)
		for _,name:=range []string{"uf_sql","uf_strict","abs"}{userFunctionAbsent(t,p,b,c,name)}
		for _,name:=range []string{"uf_configured","uf_trigger"}{xactFunctionCounts(t,b,userFunctionGet(t,p,c,name),1,1,1)}
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT count(*) FROM pg_stat_user_functions WHERE funcid='public.regexp_match(citext,citext)'::regprocedure::oid",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
	})
}

func TestReferencePGStatUserFunctionsNestingAndPresence(t *testing.T) {
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		for _,d:=range []struct{name,body string}{{"uf_leaf","PERFORM pg_sleep(0.005); RETURN 7;"},{"uf_parent","PERFORM uf_leaf(); RETURN 9;"},{"uf_fail","RAISE EXCEPTION 'uf failure';"},{"uf_unused","RETURN 7;"},{"uf_outer_fail","PERFORM uf_leaf(); RAISE EXCEPTION 'uf outer failure';"}}{xactFunctionCreate(t,p,b,c,d.name,d.body)}
		fixture.StatisticsExec(t,p,b,c,"CREATE FUNCTION uf_recursive(n integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN IF n=0 THEN PERFORM uf_recursive(1); END IF; RETURN 7; END$$",0)
		w,retire:=fixture.StatisticsReportingConn(t,p,b);xactFunctionBegin(t,p,b,w);xactFunctionCall(t,p,b,w,"uf_parent",9)
		fixture.RunStep(t,p.Ctx,w,b.Recorder,fixture.Select("SELECT uf_recursive(0)",[]string{"uf_recursive"},[]string{"INT4"},[][]any{{int64(7)}}))
		for _,name:=range []string{"uf_fail","uf_outer_fail"}{fixture.StatisticsExec(t,p,b,w,"SAVEPOINT failed",0);rows,err:=w.QueryContext(p.Ctx,"SELECT "+name+"()");if rows!=nil{rows.Close()};var state interface{SQLState()string};if !errors.As(err,&state)||state.SQLState()!="P0001"{t.Fatal("actual body failure",err)};fixture.StatisticsExec(t,p,b,w,"ROLLBACK TO failed",0);fixture.StatisticsExec(t,p,b,w,"RELEASE failed",0)}
		fixture.StatisticsExec(t,p,b,w,"ROLLBACK",0);retire();userFunctionAwait(t,p,b,"uf_leaf",2);userFunctionAwait(t,p,b,"uf_fail",0);fixture.StatisticsClearSnapshot(t,p,b,c)
		parent:=userFunctionGet(t,p,c,"uf_parent");xactFunctionCounts(t,b,parent,1,2,1);xactFunctionCounts(t,b,userFunctionGet(t,p,c,"uf_leaf"),2,2,2);if b.PG&&parent.Total<=parent.Self{t.Fatal("nested elapsed work excluded",parent)}
		xactFunctionCounts(t,b,userFunctionGet(t,p,c,"uf_recursive"),2,2,2)
		for _,name:=range []string{"uf_fail","uf_outer_fail"}{r:=userFunctionGet(t,p,c,name);xactFunctionCounts(t,b,r,0,0,0);if r.Total!=0||r.Self!=0{t.Fatal("failed invocation fabricated cost",r)}}
		userFunctionAbsent(t,p,b,c,"uf_unused")
	})
}
