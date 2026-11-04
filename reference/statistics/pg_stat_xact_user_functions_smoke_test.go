package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatXactUserFunctionsFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"xf_smoke_actor"})
	fixture.SchemaReferenceCleanup(t,"xf_smoke_private")
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xactFunctionCreate(t,p,b,c,"xf_smoke_leaf","RETURN 7;")
		xactFunctionCreate(t,p,b,c,"xf_smoke_parent","PERFORM xf_smoke_leaf(); RETURN 9;")
		xactFunctionCreate(t,p,b,c,"xf_smoke_unused","RETURN 7;")
		fixture.StatisticsExec(t,p,b,c,"CREATE ROLE xf_smoke_actor",0)
		fixture.StatisticsExec(t,p,b,c,"REVOKE EXECUTE ON FUNCTION xf_smoke_parent() FROM PUBLIC",0)
		fixture.StatisticsExec(t,p,b,c,"CREATE SCHEMA xf_smoke_private",0)
		for _,name:=range []string{"xf_smoke_leaf","xf_smoke_parent","xf_smoke_unused"}{fixture.StatisticsExec(t,p,b,c,"ALTER FUNCTION "+name+"() SET SCHEMA xf_smoke_private",0)}
		w,retire:=fixture.StatisticsReportingConn(t,p,b);defer retire()
		defer func(){ctx,cancel:=context.WithTimeout(context.Background(),fixture.Liveness);defer cancel();for _,q:=range []string{"ROLLBACK","RESET ROLE","GRANT EXECUTE ON FUNCTION pg_stat_get_xact_function_self_time(oid) TO PUBLIC"}{if _,err:=w.ExecContext(ctx,q);err!=nil{t.Error(q,err)}}}()
		xactFunctionBegin(t,p,b,w);fixture.StatisticsExec(t,p,b,w,"SET search_path=xf_smoke_private,pg_catalog",0);held:=fixture.IdentityHeld(t,p.Ctx,w,xactFunctionSQL);defer held.Statement.Close()
		for _,name:=range []string{"xf_smoke_parent","xf_smoke_leaf","xf_smoke_unused"}{xactFunctionAbsent(t,p,b,w,name)}
		var first xactFunctionRow
		for i:=range 2 {
			xactFunctionCall(t,p,b,w,"xf_smoke_parent",9)
			for _,q:=range []fixture.Executor{w,held}{parent,leaf:=xactFunctionGet(t,p,q,"xf_smoke_parent"),xactFunctionGet(t,p,q,"xf_smoke_leaf");xactFunctionCounts(t,b,parent,int64(i+1),float64(2*(i+1)),float64(i+1));xactFunctionCounts(t,b,leaf,int64(i+1),float64(i+1),float64(i+1));if i==0{first=parent}}
		}
		rows,err:=held.QueryContext(p.Ctx,xactFunctionSQL,"xf_smoke_parent");if err!=nil{t.Fatal(err)};cols,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)};xactFunctionMetadata(t,cols,b.PG);rows.Close()
		xactFunctionAbsent(t,p,b,c,"xf_smoke_parent")
		before:=xactFunctionGet(t,p,w,"xf_smoke_parent");fixture.StatisticsExec(t,p,b,w,"SET ROLE xf_smoke_actor",0);if got:=xactFunctionGet(t,p,w,"xf_smoke_parent");got!=before{t.Fatal("nonowner visibility",got,before)}
		fixture.StatisticsExec(t,p,b,w,"RESET ROLE",0);fixture.StatisticsExec(t,p,b,w,"SET track_functions=none",0);xactFunctionCounts(t,b,xactFunctionGet(t,p,w,"xf_smoke_parent"),2,4,2)
		fixture.StatisticsExec(t,p,b,w,"REVOKE EXECUTE ON FUNCTION pg_stat_get_xact_function_self_time(oid) FROM PUBLIC",0);fixture.StatisticsExec(t,p,b,w,"SET ROLE xf_smoke_actor",0)
		fixture.StatisticsExec(t,p,b,w,"SAVEPOINT denied",0);xactFunctionDenied(t,p,b,w,"SELECT funcid FROM pg_stat_xact_user_functions","42501","permission denied for function pg_stat_get_xact_function_self_time",false)
		fixture.StatisticsExec(t,p,b,w,"ROLLBACK TO denied",0);fixture.StatisticsExec(t,p,b,w,"RESET ROLE",0);fixture.StatisticsExec(t,p,b,w,"GRANT EXECUTE ON FUNCTION pg_stat_get_xact_function_self_time(oid) TO PUBLIC",0)
		xactTableEnd(t,p,b,w,"ROLLBACK");fixture.StatisticsExec(t,p,b,w,"BEGIN",0);xactFunctionAbsent(t,p,b,w,"xf_smoke_parent");xactFunctionAbsent(t,p,b,w,"xf_smoke_leaf");fixture.StatisticsExec(t,p,b,w,"ROLLBACK",0)
		step:=fixture.ErrorDiagnostic("UPDATE pg_catalog.pg_stat_xact_user_functions SET calls=0 WHERE FALSE",xactFunctionWriteDiagnostic("UPDATE"));step.ErrorStage="execute";fixture.RunStep(t,p.Ctx,w,b.Recorder,step)
		if first.Calls!=1||first.Name!="xf_smoke_parent"{t.Fatal("retained first row changed",first)}
	})
}
