package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatUserFunctionsFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"uf_smoke_reader"})
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		defer func(){ctx,cancel:=context.WithTimeout(context.Background(),fixture.Liveness);defer cancel();if _,err:=c.ExecContext(ctx,"RESET ROLE");err!=nil{t.Error(err)};if _,err:=c.ExecContext(ctx,"GRANT USAGE ON SCHEMA public TO PUBLIC");err!=nil{t.Error(err)}}()
		for _,d:=range []struct{name,body string}{{"uf_smoke_leaf","PERFORM pg_sleep(0.005); RETURN 7;"},{"uf_smoke_parent","PERFORM uf_smoke_leaf(); RETURN 9;"},{"uf_smoke_disabled","RETURN 7;"},{"uf_smoke_unused","RETURN 7;"}}{xactFunctionCreate(t,p,b,c,d.name,d.body)}
		fixture.StatisticsExec(t,p,b,c,"CREATE ROLE uf_smoke_reader",0)
		w,retire:=fixture.StatisticsReportingConn(t,p,b);xactFunctionBegin(t,p,b,w)
		xact:=fixture.IdentityHeld(t,p.Ctx,w,xactFunctionSQL)
		for range 2{xactFunctionCall(t,p,b,w,"uf_smoke_leaf",7)}
		for _,q:=range []fixture.Executor{w,xact}{xactFunctionCounts(t,b,xactFunctionGet(t,p,q,"uf_smoke_leaf"),2,2,2)}
		fixture.StatisticsExec(t,p,b,w,"SET track_functions=none",0);xactFunctionCall(t,p,b,w,"uf_smoke_disabled",7);userFunctionUnpublished(t,p,b,c,"uf_smoke_leaf")
		xact.Statement.Close();fixture.StatisticsExec(t,p,b,w,"COMMIT",0);retire();userFunctionAwait(t,p,b,"uf_smoke_leaf",2);fixture.StatisticsClearSnapshot(t,p,b,c)
		held:=fixture.IdentityHeld(t,p.Ctx,c,userFunctionSQL);defer held.Statement.Close()
		var oid int64;if err:=c.QueryRowContext(p.Ctx,"SELECT 'uf_smoke_leaf()'::regprocedure::oid").Scan(&oid);err!=nil{t.Fatal(err)}
		for _,q:=range []fixture.Executor{c,held}{r:=userFunctionGet(t,p,q,"uf_smoke_leaf");xactFunctionCounts(t,b,r,2,2,2);if r.ID!=oid||r.Schema!="public"||r.Name!="uf_smoke_leaf"{t.Fatal("smoke identity",r,oid)};rows,err:=q.QueryContext(p.Ctx,userFunctionSQL,"uf_smoke_leaf");if err!=nil{t.Fatal(err)};types,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)};xactFunctionMetadata(t,types,b.PG);rows.Close();xactFunctionMetadata(t,types,b.PG)}
		w,retire=fixture.StatisticsReportingConn(t,p,b);xactFunctionBegin(t,p,b,w);xactFunctionCall(t,p,b,w,"uf_smoke_parent",9);fixture.StatisticsExec(t,p,b,w,"COMMIT",0);retire();userFunctionAwait(t,p,b,"uf_smoke_leaf",3);userFunctionAwait(t,p,b,"uf_smoke_parent",1);fixture.StatisticsClearSnapshot(t,p,b,c)
		parent:=userFunctionGet(t,p,held,"uf_smoke_parent");xactFunctionCounts(t,b,parent,1,2,1);if b.PG&&parent.Total<=parent.Self{t.Fatal("smoke nested inclusive timing",parent)};leaf:=userFunctionGet(t,p,held,"uf_smoke_leaf");xactFunctionCounts(t,b,leaf,3,3,3)
		for _,name:=range []string{"uf_smoke_disabled","uf_smoke_unused"}{userFunctionAbsent(t,p,b,c,name)}
		fixture.StatisticsExec(t,p,b,c,"REVOKE ALL ON FUNCTION uf_smoke_leaf() FROM PUBLIC",0);fixture.StatisticsExec(t,p,b,c,"REVOKE USAGE ON SCHEMA public FROM PUBLIC",0);fixture.StatisticsExec(t,p,b,c,"SET ROLE uf_smoke_reader",0)
		// Fully qualified monitoring is independent of the observed schema ACL.
		if got:=userFunctionGet(t,p,held,"uf_smoke_leaf");got!=leaf{t.Fatal("smoke nonowner distribution",got,leaf)};fixture.StatisticsExec(t,p,b,c,"RESET ROLE",0)
		q:="UPDATE pg_catalog.pg_stat_user_functions SET calls=0 WHERE FALSE";step:=fixture.ErrorDiagnostic(q,userFunctionWriteDiagnostic("UPDATE"));step.Prepared=true;step.ErrorStage="prepare";fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
		if got:=userFunctionGet(t,p,held,"uf_smoke_leaf");got!=leaf{t.Fatal("smoke rejected mutation",got,leaf)}
	})
}
