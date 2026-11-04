package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatUserFunctionsPrivilegesAndDemand(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"uf_reader","uf_monitor","uf_stats"});fixture.SchemaReferenceCleanup(t,"uf_private")
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		getters:=[]string{"pg_stat_get_function_calls","pg_stat_get_function_total_time","pg_stat_get_function_self_time"}
		defer func(){ctx,cancel:=context.WithTimeout(context.Background(),fixture.Liveness);defer cancel();queries:=[]string{"ROLLBACK","RESET ROLE","GRANT SELECT ON pg_stat_user_functions TO PUBLIC","REVOKE SELECT (funcid) ON pg_stat_user_functions FROM uf_reader"};for _,g:=range getters{queries=append(queries,"GRANT EXECUTE ON FUNCTION "+g+"(oid) TO PUBLIC")};for _,q:=range queries{if _,err:=c.ExecContext(ctx,q);err!=nil{t.Error("restore authority",q,err)}}}()
		for _,q:=range []string{"CREATE ROLE uf_reader","CREATE ROLE uf_monitor","CREATE ROLE uf_stats","GRANT pg_monitor TO uf_monitor","GRANT pg_read_all_stats TO uf_stats","CREATE SCHEMA uf_private","CREATE FUNCTION uf_private.uf_private_f() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 7; END$$","REVOKE ALL ON FUNCTION uf_private.uf_private_f() FROM PUBLIC","REVOKE ALL ON SCHEMA uf_private FROM PUBLIC"}{fixture.StatisticsExec(t,p,b,c,q,0)}
		w,retire:=fixture.StatisticsReportingConn(t,p,b);xactFunctionBegin(t,p,b,w);for range 2{fixture.RunStep(t,p.Ctx,w,b.Recorder,fixture.Select("SELECT uf_private.uf_private_f()",[]string{"uf_private_f"},[]string{"INT4"},[][]any{{int64(7)}}))};fixture.StatisticsExec(t,p,b,w,"COMMIT",0);retire();userFunctionAwait(t,p,b,"uf_private_f",2)
		before:=userFunctionGet(t,p,c,"uf_private_f");xactFunctionCounts(t,b,before,2,2,2)
		for _,role:=range []string{"uf_reader","uf_monitor","uf_stats"}{fixture.StatisticsExec(t,p,b,c,"SET ROLE "+role,0);got:=userFunctionGet(t,p,c,"uf_private_f");if got!=before{t.Fatal("distribution redacted by observed ACL",role,got,before)};fixture.StatisticsExec(t,p,b,c,"RESET ROLE",0)}
		fixture.StatisticsExec(t,p,b,c,"SET ROLE uf_reader",0);denied:=xactFunctionDiagnostic("42501","permission denied for schema uf_private");denied.Position="8";step:=fixture.ErrorDiagnostic("SELECT uf_private.uf_private_f()",denied);step.SelectRows=true;step.ErrorStage="execute";fixture.RunStep(t,p.Ctx,c,b.Recorder,step);fixture.StatisticsExec(t,p,b,c,"RESET ROLE",0)
		for bit,getter:=range getters{
			for _,projection:=range []struct{sql string;names,types []string;ordinary,empty uint8;count bool}{
				{"funcid",[]string{"funcid"},[]string{"OID"},1,0,false},
				{"count(*)",[]string{"count"},[]string{"INT8"},1,0,true},
				{"calls",[]string{"calls"},[]string{"INT8"},1,1,false},
				{"total_time",[]string{"total_time"},[]string{"FLOAT8"},3,2,false},
				{"self_time",[]string{"self_time"},[]string{"FLOAT8"},5,4,false},
				{"*",xactFunctionNames,xactFunctionTypes,7,7,false},
			}{
				for _,suffix:=range []string{""," AND TRUE"," LIMIT 0"," AND FALSE"," AND FALSE::boolean"," AND NULL::boolean"," AND $1::boolean"}{
					q:="SELECT "+projection.sql+" FROM pg_catalog.pg_stat_user_functions WHERE funcname='uf_demand'"+suffix
					held:=fixture.IdentityHeld(t,p.Ctx,c,q)
					fixture.StatisticsExec(t,p,b,c,"REVOKE EXECUTE ON FUNCTION "+getter+"(oid) FROM PUBLIC",0);fixture.StatisticsExec(t,p,b,c,"SET ROLE uf_reader",0)
					values:=[]any{true};if suffix==" AND $1::boolean"{values=[]any{true,false,nil,false,true}}
					for _,route:=range []int{0,1,2}{for _,value:=range values{
						mask:=projection.ordinary;if suffix==" AND FALSE"||suffix==" AND FALSE::boolean"||suffix==" AND NULL::boolean"||suffix==" AND $1::boolean"&&value!=true{mask=projection.empty}
						var args []any;if suffix==" AND $1::boolean"{args=[]any{value}}
						var want [][]any;if projection.count&&suffix!=" LIMIT 0"{want=[][]any{{int64(0)}}}
						probe:=fixture.Select(q,projection.names,projection.types,want,args...);if mask&(1<<bit)!=0{probe=fixture.ErrorDiagnostic(q,xactFunctionDiagnostic("42501","permission denied for function "+getter),args...);probe.SelectRows=true;probe.ErrorStage="execute"};probe.Prepared=route==1
						var executor fixture.Executor=c;if route==2{executor=held};fixture.RunStep(t,p.Ctx,executor,b.Recorder,probe)
					}}
					fixture.StatisticsExec(t,p,b,c,"RESET ROLE",0);fixture.StatisticsExec(t,p,b,c,"GRANT EXECUTE ON FUNCTION "+getter+"(oid) TO PUBLIC",0)
					// Regrant reuses the same handle, but not another parameter
					// execution beyond the portable first-five custom-plan window.
					if suffix!=" AND $1::boolean"{fixture.StatisticsExec(t,p,b,c,"SET ROLE uf_reader",0);var want [][]any;if projection.count&&suffix!=" LIMIT 0"{want=[][]any{{int64(0)}}};fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(q,projection.names,projection.types,want));fixture.StatisticsExec(t,p,b,c,"RESET ROLE",0)}
					held.Statement.Close()
				}
			}
			for _,tc:=range []struct{q string;mask uint8}{
				{"SELECT funcid FROM pg_stat_user_functions WHERE funcname='uf_demand' AND total_time>0",3},
				{"SELECT funcid FROM pg_stat_user_functions WHERE funcname='uf_demand' ORDER BY self_time",5},
			}{
				retained:=fixture.IdentityHeld(t,p.Ctx,c,tc.q)
				fixture.StatisticsExec(t,p,b,c,"REVOKE EXECUTE ON FUNCTION "+getter+"(oid) FROM PUBLIC",0);fixture.StatisticsExec(t,p,b,c,"SET ROLE uf_reader",0)
				for _,route:=range []int{0,1,2}{
					probe:=fixture.Select(tc.q,[]string{"funcid"},[]string{"OID"},nil)
					if tc.mask&(1<<bit)!=0{probe=fixture.ErrorDiagnostic(tc.q,xactFunctionDiagnostic("42501","permission denied for function "+getter));probe.SelectRows=true;probe.ErrorStage="execute"}
					probe.Prepared=route==1;var executor fixture.Executor=c;if route==2{executor=retained};fixture.RunStep(t,p.Ctx,executor,b.Recorder,probe)
				}
				fixture.StatisticsExec(t,p,b,c,"RESET ROLE",0);fixture.StatisticsExec(t,p,b,c,"GRANT EXECUTE ON FUNCTION "+getter+"(oid) TO PUBLIC",0);retained.Statement.Close()
			}
			fixture.StatisticsExec(t,p,b,c,"REVOKE EXECUTE ON FUNCTION "+getter+"(oid) FROM PUBLIC",0);fixture.StatisticsExec(t,p,b,c,"SET ROLE uf_reader",0)
			for _,tc:=range []struct{q,name,typ string;rows [][]any}{
				{"SELECT funcid FROM pg_stat_user_functions WHERE total_time>0 AND FALSE","funcid","OID",nil},
				{"SELECT f.funcid FROM (SELECT funcid FROM pg_stat_user_functions) f WHERE FALSE","funcid","OID",nil},
				{"SELECT v.k FROM (VALUES(1)) v(k) LEFT JOIN pg_stat_user_functions f ON FALSE","k","INT4",[][]any{{int64(1)}}},
			}{for _,prepared:=range []bool{false,true}{probe:=fixture.Select(tc.q,[]string{tc.name},[]string{tc.typ},tc.rows);probe.Prepared=prepared;fixture.RunStep(t,p.Ctx,c,b.Recorder,probe)}}
			fixture.StatisticsExec(t,p,b,c,"RESET ROLE",0);fixture.StatisticsExec(t,p,b,c,"GRANT EXECUTE ON FUNCTION "+getter+"(oid) TO PUBLIC",0)
		}
		const identity="SELECT funcid FROM pg_stat_user_functions WHERE FALSE"
		held:=fixture.IdentityHeld(t,p.Ctx,c,identity);defer held.Statement.Close()
		fixture.StatisticsExec(t,p,b,c,"REVOKE SELECT ON pg_stat_user_functions FROM PUBLIC",0);fixture.StatisticsExec(t,p,b,c,"SET ROLE uf_reader",0)
		for _,prepared:=range []bool{false,true}{xactFunctionDenied(t,p,b,c,identity,"42501","permission denied for view pg_stat_user_functions",prepared)};xactFunctionDenied(t,p,b,held,identity,"42501","permission denied for view pg_stat_user_functions",false)
		for _,q:=range []string{"SELECT f.funcid FROM (SELECT funcid FROM pg_stat_user_functions) f WHERE FALSE","SELECT v.k FROM (VALUES(1)) v(k) LEFT JOIN pg_stat_user_functions f ON FALSE"}{for _,prepared:=range []bool{false,true}{xactFunctionDenied(t,p,b,c,q,"42501","permission denied for view pg_stat_user_functions",prepared)}}
		fixture.StatisticsExec(t,p,b,c,"RESET ROLE",0);fixture.StatisticsExec(t,p,b,c,"GRANT SELECT (funcid) ON pg_stat_user_functions TO uf_reader",0);fixture.StatisticsExec(t,p,b,c,"SET ROLE uf_reader",0)
		fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(identity,[]string{"funcid"},[]string{"OID"},nil));xactFunctionDenied(t,p,b,c,"SELECT * FROM pg_stat_user_functions WHERE FALSE","42501","permission denied for view pg_stat_user_functions",false)
		fixture.StatisticsExec(t,p,b,c,"RESET ROLE",0);fixture.StatisticsExec(t,p,b,c,"GRANT SELECT ON pg_stat_user_functions TO PUBLIC",0)
		const total="SELECT total_time FROM pg_stat_user_functions WHERE funcname='uf_demand'"
		peerHeld:=fixture.IdentityHeld(t,p.Ctx,c,total);defer peerHeld.Statement.Close();peer,err:=b.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer peer.Close()
		fixture.StatisticsExec(t,p,b,c,"SET ROLE uf_reader",0);fixture.StatisticsExec(t,p,b,c,"BEGIN",0);fixture.RunStep(t,p.Ctx,peerHeld,b.Recorder,fixture.Select(total,[]string{"total_time"},[]string{"FLOAT8"},nil));fixture.StatisticsExec(t,p,b,peer,"REVOKE EXECUTE ON FUNCTION pg_stat_get_function_total_time(oid) FROM PUBLIC",0);fixture.StatisticsExec(t,p,b,c,"ROLLBACK",0)
		xactFunctionDenied(t,p,b,peerHeld,total,"42501","permission denied for function pg_stat_get_function_total_time",false)
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT funcid FROM pg_stat_user_functions WHERE funcname='uf_demand'",[]string{"funcid"},[]string{"OID"},nil))
		fixture.StatisticsExec(t,p,b,peer,"GRANT EXECUTE ON FUNCTION pg_stat_get_function_total_time(oid) TO PUBLIC",0);fixture.StatisticsExec(t,p,b,c,"RESET ROLE",0)
	})
}
