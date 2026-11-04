package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatXactUserFunctionsPrivilegesAndDemand(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"xf_reader","xf_monitor","xf_stats"})
	fixture.SchemaReferenceCleanup(t,"xf_private")
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		defer func(){ctx,cancel:=context.WithTimeout(context.Background(),fixture.Liveness);defer cancel();for _,q:=range []string{"ROLLBACK","RESET ROLE","GRANT SELECT ON pg_stat_xact_user_functions TO PUBLIC","REVOKE SELECT (funcid) ON pg_stat_xact_user_functions FROM xf_reader","GRANT EXECUTE ON FUNCTION pg_stat_get_xact_function_calls(oid) TO PUBLIC","GRANT EXECUTE ON FUNCTION pg_stat_get_xact_function_total_time(oid) TO PUBLIC","GRANT EXECUTE ON FUNCTION pg_stat_get_xact_function_self_time(oid) TO PUBLIC"}{if _,err:=c.ExecContext(ctx,q);err!=nil{t.Error("restore authority",q,err)}}}()
		for _,q:=range []string{"CREATE ROLE xf_reader","CREATE ROLE xf_monitor","CREATE ROLE xf_stats","GRANT pg_monitor TO xf_monitor","GRANT pg_read_all_stats TO xf_stats","CREATE SCHEMA xf_private","CREATE FUNCTION xf_private.xf_private_f() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 7; END$$","REVOKE ALL ON FUNCTION xf_private.xf_private_f() FROM PUBLIC","REVOKE ALL ON SCHEMA xf_private FROM PUBLIC"}{fixture.StatisticsExec(t,p,b,c,q,0)}
		w,retire:=fixture.StatisticsReportingConn(t,p,b);defer retire();xactFunctionBegin(t,p,b,w)
		for range 2 {fixture.RunStep(t,p.Ctx,w,b.Recorder,fixture.Select("SELECT xf_private.xf_private_f()",[]string{"xf_private_f"},[]string{"INT4"},[][]any{{int64(7)}}))}
		before:=xactFunctionGet(t,p,w,"xf_private_f");xactFunctionCounts(t,b,before,2,2,2)
		for _,role:=range []string{"xf_reader","xf_monitor","xf_stats"}{fixture.StatisticsExec(t,p,b,w,"SET ROLE "+role,0);got:=xactFunctionGet(t,p,w,"xf_private_f");if got!=before{t.Fatal("nonowner distribution redacted",role,got,before)};fixture.StatisticsExec(t,p,b,w,"RESET ROLE",0)}
		fixture.StatisticsExec(t,p,b,w,"SET ROLE xf_reader",0);fixture.StatisticsExec(t,p,b,w,"SAVEPOINT invoke_denied",0)
		invokeDiagnostic:=xactFunctionDiagnostic("42501","permission denied for schema xf_private");invokeDiagnostic.Position="8"
		invokeDenied:=fixture.ErrorDiagnostic("SELECT xf_private.xf_private_f()",invokeDiagnostic);invokeDenied.SelectRows=true;invokeDenied.ErrorStage="execute";fixture.RunStep(t,p.Ctx,w,b.Recorder,invokeDenied)
		fixture.StatisticsExec(t,p,b,w,"ROLLBACK TO invoke_denied",0);fixture.StatisticsExec(t,p,b,w,"RESET ROLE",0);fixture.StatisticsExec(t,p,b,w,"ROLLBACK",0)
		queries:=[]string{"SELECT funcid FROM pg_stat_xact_user_functions","SELECT calls FROM pg_stat_xact_user_functions","SELECT total_time FROM pg_stat_xact_user_functions","SELECT self_time FROM pg_stat_xact_user_functions","SELECT * FROM pg_stat_xact_user_functions","SELECT count(*) FROM pg_stat_xact_user_functions","SELECT funcname FROM pg_stat_xact_user_functions WHERE total_time>0 ORDER BY self_time","SELECT funcid FROM pg_stat_xact_user_functions LIMIT 0"}
		held:=make([]fixture.HeldStatement,len(queries));for i,q:=range queries{held[i]=fixture.IdentityHeld(t,p.Ctx,c,q);defer held[i].Statement.Close()}
		for _,getter:=range []string{"pg_stat_get_xact_function_calls","pg_stat_get_xact_function_total_time","pg_stat_get_xact_function_self_time"}{
			const parameterSQL="SELECT funcid FROM pg_stat_xact_user_functions WHERE $1::boolean"
			parameterHeld:=fixture.IdentityHeld(t,p.Ctx,c,parameterSQL);defer parameterHeld.Statement.Close()
			fixture.StatisticsExec(t,p,b,c,"REVOKE EXECUTE ON FUNCTION "+getter+"(oid) FROM PUBLIC",0)
			fixture.StatisticsExec(t,p,b,c,"SET ROLE xf_reader",0)
			for i,q:=range queries {for _,prepared:=range []bool{false,true}{xactFunctionDenied(t,p,b,c,q,"42501","permission denied for function "+getter,prepared)};xactFunctionDenied(t,p,b,held[i],q,"42501","permission denied for function "+getter,false)}
			for _,prepared:=range []bool{false,true}{
				for _,value:=range []any{false,nil,true,nil,false}{
					step:=fixture.Select(parameterSQL,[]string{"funcid"},[]string{"OID"},nil,value)
					if value==true {step=fixture.ErrorDiagnostic(parameterSQL,xactFunctionDiagnostic("42501","permission denied for function "+getter),value);step.SelectRows=true;step.ErrorStage="execute"}
					step.Prepared=prepared;fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
				}
			}
			// Five executions stay within PostgreSQL's initial custom-plan window.
			for _,value:=range []any{false,nil,true,nil,false}{
				step:=fixture.Select(parameterSQL,[]string{"funcid"},[]string{"OID"},nil,value)
				if value==true {step=fixture.ErrorDiagnostic(parameterSQL,xactFunctionDiagnostic("42501","permission denied for function "+getter),value);step.SelectRows=true;step.ErrorStage="execute"}
				fixture.RunStep(t,p.Ctx,parameterHeld,b.Recorder,step)
			}
			for _,prepared:=range []bool{false,true}{step:=fixture.Select("SELECT count(*) FROM pg_stat_xact_user_functions WHERE FALSE",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}});step.Prepared=prepared;fixture.RunStep(t,p.Ctx,c,b.Recorder,step)}
			fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT funcid FROM pg_stat_xact_user_functions WHERE FALSE",[]string{"funcid"},[]string{"OID"},nil))
			fixture.StatisticsExec(t,p,b,c,"RESET ROLE",0);fixture.StatisticsExec(t,p,b,c,"GRANT EXECUTE ON FUNCTION "+getter+"(oid) TO PUBLIC",0)
			fixture.StatisticsExec(t,p,b,c,"SET ROLE xf_reader",0)
			for i,q:=range queries {rows,err:=held[i].QueryContext(p.Ctx,q);if err!=nil{t.Fatal("held regrant",getter,q,err)};for rows.Next(){};if rows.Err()!=nil{t.Fatal(rows.Err())};rows.Close()}
			fixture.StatisticsExec(t,p,b,c,"RESET ROLE",0)
		}
		const eliminatedSQL="SELECT funcid FROM pg_stat_xact_user_functions WHERE $1::boolean"
		eliminatedHeld:=fixture.IdentityHeld(t,p.Ctx,c,eliminatedSQL);defer eliminatedHeld.Statement.Close()
		fixture.StatisticsExec(t,p,b,c,"REVOKE SELECT ON pg_stat_xact_user_functions FROM PUBLIC",0);fixture.StatisticsExec(t,p,b,c,"SET ROLE xf_reader",0)
		for _,q:=range []string{queries[0],"SELECT funcid FROM pg_stat_xact_user_functions WHERE FALSE"}{xactFunctionDenied(t,p,b,c,q,"42501","permission denied for view pg_stat_xact_user_functions",false)}
		xactFunctionDenied(t,p,b,held[0],queries[0],"42501","permission denied for view pg_stat_xact_user_functions",false)
		for _,value:=range []any{false,nil}{
			for _,prepared:=range []bool{false,true}{
				step:=fixture.ErrorDiagnostic(eliminatedSQL,xactFunctionDiagnostic("42501","permission denied for view pg_stat_xact_user_functions"),value);step.SelectRows=true;step.ErrorStage="execute";step.Prepared=prepared;fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
			}
			step:=fixture.ErrorDiagnostic(eliminatedSQL,xactFunctionDiagnostic("42501","permission denied for view pg_stat_xact_user_functions"),value);step.SelectRows=true;step.ErrorStage="execute";fixture.RunStep(t,p.Ctx,eliminatedHeld,b.Recorder,step)
		}
		fixture.StatisticsExec(t,p,b,c,"RESET ROLE",0);fixture.StatisticsExec(t,p,b,c,"GRANT SELECT (funcid) ON pg_stat_xact_user_functions TO xf_reader",0);fixture.StatisticsExec(t,p,b,c,"SET ROLE xf_reader",0)
		rows,err:=held[0].QueryContext(p.Ctx,queries[0]);if err!=nil{t.Fatal("column grant",err)};for rows.Next(){};if rows.Err()!=nil{t.Fatal(rows.Err())};rows.Close()
		xactFunctionDenied(t,p,b,held[4],queries[4],"42501","permission denied for view pg_stat_xact_user_functions",false)
		fixture.StatisticsExec(t,p,b,c,"RESET ROLE",0);fixture.StatisticsExec(t,p,b,c,"GRANT SELECT ON pg_stat_xact_user_functions TO PUBLIC",0)
		// Peer ACL changes invalidate a held occurrence at a portable boundary.
		peer,err:=b.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer peer.Close()
		fixture.StatisticsExec(t,p,b,c,"SET ROLE xf_reader",0);fixture.StatisticsExec(t,p,b,c,"BEGIN",0)
		rows,err=held[0].QueryContext(p.Ctx,queries[0]);if err!=nil{t.Fatal(err)};rows.Close()
		fixture.StatisticsExec(t,p,b,peer,"REVOKE EXECUTE ON FUNCTION pg_stat_get_xact_function_self_time(oid) FROM PUBLIC",0)
		fixture.StatisticsExec(t,p,b,c,"ROLLBACK",0)
		xactFunctionDenied(t,p,b,held[0],queries[0],"42501","permission denied for function pg_stat_get_xact_function_self_time",false)
		fixture.StatisticsExec(t,p,b,peer,"GRANT EXECUTE ON FUNCTION pg_stat_get_xact_function_self_time(oid) TO PUBLIC",0);fixture.StatisticsExec(t,p,b,c,"RESET ROLE",0)
	})
}
