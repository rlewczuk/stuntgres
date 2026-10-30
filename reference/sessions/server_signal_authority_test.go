package reference_test
import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)
func TestReferenceServerSignalAuthority(t *testing.T){
 const role="signal_caller";const member="signal_member"
 fixture.RoleReferenceCleanup(t,[]string{role,member})
 signalScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,n *[]fixture.Diagnostic){
 run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
 run(fixture.Exec("CREATE ROLE "+role+" LOGIN",0));run(fixture.Exec("CREATE ROLE "+member,0));defer signalRestore(t,c,role,member)()
 admin:=func(){run(fixture.Exec("RESET SESSION AUTHORIZATION",0))};caller:=func(){run(fixture.Exec("SET SESSION AUTHORIZATION "+role,0))}
 run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+role,0));run(fixture.Exec("CREATE TABLE signal_nulls(pid integer)",0));run(fixture.Exec("INSERT INTO signal_nulls VALUES(NULL)",1));run(fixture.Exec("GRANT SELECT ON signal_nulls TO "+role,0))
 run(fixture.Exec("GRANT pg_signal_backend TO "+role,0))
 for i,call:=range signalCalls{
  q:="SELECT "+call;stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)}
  held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
  for _,grant:=range []bool{false,true,false}{admin();action:="REVOKE EXECUTE ON FUNCTION "+signalIDs[i]+" FROM "+role;if grant{action="GRANT EXECUTE ON FUNCTION "+signalIDs[i]+" TO "+role};run(fixture.Exec(action,0));caller();s:=signalError(q,"42501","permission denied for function "+signalNames[i]);if grant{s=signalPhysical(q,i,b.PG)};fixture.RunStep(t,p.Ctx,held,b.Recorder,s);if b.PG&&grant&&i==1{signalNotices(t,n,fixture.Diagnostic{Severity:"WARNING",SeverityUnlocalized:"WARNING",Code:"01000",Message:"rotation not possible because log collection not active"})}else{signalNotices(t,n)}}
  stmt.Close()
 }
 runtime:="SELECT pg_log_backend_memory_contexts(pid) AS v FROM signal_nulls"
 run(signalError(runtime,"42501","permission denied for function pg_log_backend_memory_contexts"))
 for _,mode:=range []string{"force_custom_plan","force_generic_plan"}{run(fixture.Exec("SET plan_cache_mode="+mode,0));for _,literal:=range []string{"NULL","NULL::integer"}{signalRoutes(t,p,b,c,signalNull("SELECT pg_log_backend_memory_contexts("+literal+") AS v"))};q:="SELECT pg_log_backend_memory_contexts($1) AS v";stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
  for _,granted:=range []bool{false,true,false}{admin();action:="REVOKE EXECUTE ON FUNCTION "+signalIDs[2]+" FROM "+role;if granted{action="GRANT EXECUTE ON FUNCTION "+signalIDs[2]+" TO "+role};run(fixture.Exec(action,0));caller();s:=signalNull(q,nil);if !granted&&mode=="force_generic_plan"{s=signalError(q,"42501","permission denied for function pg_log_backend_memory_contexts");s.Args=[]any{nil}};fixture.RunStep(t,p.Ctx,held,b.Recorder,s);signalNotices(t,n);if granted{run(signalNull(runtime))}else{run(signalError(runtime,"42501","permission denied for function pg_log_backend_memory_contexts"))}}
  stmt.Close()
 };run(fixture.Exec("RESET plan_cache_mode",0))
 for _,pid:=range []string{"-1","0"}{run(signalError("SELECT pg_log_backend_memory_contexts("+pid+")","42501","permission denied for function pg_log_backend_memory_contexts"));signalNotices(t,n)}
 admin();run(fixture.Exec("GRANT "+member+" TO "+role,0));run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+signalIDs[2]+" TO "+member,0));caller();run(fixture.Select("SELECT pg_log_backend_memory_contexts(-1) AS v",[]string{"v"},[]string{"BOOL"},[][]any{{false}}));signalNotices(t,n,signalWarning("-1"));admin();run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+signalIDs[2]+" FROM "+member,0))
 run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+signalIDs[2]+" TO PUBLIC",0));caller();run(signalNull(runtime));admin();run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+signalIDs[2]+" FROM PUBLIC",0));caller();run(signalError(runtime,"42501","permission denied for function pg_log_backend_memory_contexts"));admin()
 })
}
