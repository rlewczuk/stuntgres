package reference_test
import (
 "database/sql"
 "strconv"
 "testing"
 "stuntgres/reference/fixture"
)
func TestReferenceServerSignalSmoke(t *testing.T){
 const role="signal_smoke_caller";const database="signal_smoke_database"
 fixture.RoleReferenceCleanup(t,[]string{role});names:=[]string{database};fixture.DatabaseReferenceCleanup(t,&names)
 signalScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,n *[]fixture.Diagnostic){
 run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
 run(fixture.Exec("CREATE ROLE "+role+" LOGIN PASSWORD 'role-core-fixture'",0));defer signalRestore(t,c,role)()
 run(fixture.Exec("CREATE DATABASE "+database,0))
 pool,_,_:=fixture.DatabaseScenarioPool(t,p,b.PG,database,"");peer,err:=pool.Conn(p.Ctx);if err!=nil{t.Fatal(err)}
 var pid int;if err:=peer.QueryRowContext(p.Ctx,"SELECT pg_backend_pid()").Scan(&pid);err!=nil{t.Fatal(err)}
 same,err:=b.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer same.Close();var samePID int;if err:=same.QueryRowContext(p.Ctx,"SELECT pg_backend_pid()").Scan(&samePID);err!=nil{t.Fatal(err)}
 s:=fixture.Select("SELECT pg_reload_conf(),pg_rotate_logfile(),pg_log_backend_memory_contexts(1) WHERE FALSE",signalNames,[]string{"BOOL","BOOL","BOOL"},nil);s.CheckMetadata=signalMetadata;signalRoutes(t,p,b,c,s)
 signalRoutes(t,p,b,c,signalNull("SELECT pg_log_backend_memory_contexts(NULL) AS v"))
 run(fixture.Select("SELECT pg_log_backend_memory_contexts(-1) AS v",[]string{"v"},[]string{"BOOL"},[][]any{{false}}));signalNotices(t,n,signalWarning("-1"))
 for _,target:=range []int{pid,samePID}{q:="SELECT pg_log_backend_memory_contexts("+strconv.Itoa(target)+")";run(signalPhysical(q,2,b.PG));signalNotices(t,n)}
 q:="SELECT pg_log_backend_memory_contexts("+strconv.Itoa(pid)+")";stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close();held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
 for _,grant:=range []bool{false,true,false}{action:="REVOKE EXECUTE ON FUNCTION "+signalIDs[2]+" FROM "+role;if grant{action="GRANT EXECUTE ON FUNCTION "+signalIDs[2]+" TO "+role};run(fixture.Exec(action,0));run(fixture.Exec("SET SESSION AUTHORIZATION "+role,0));s:=signalError(q,"42501","permission denied for function pg_log_backend_memory_contexts");if grant{s=signalPhysical(q,2,b.PG)};fixture.RunStep(t,p.Ctx,held,b.Recorder,s);signalNotices(t,n);run(fixture.Exec("RESET SESSION AUTHORIZATION",0))}
 loginPool,_,loginRecord:=fixture.DatabaseScenarioPool(t,p,b.PG,database,role)
 login,err:=loginPool.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer login.Close()
 var adminPID int;if err:=c.QueryRowContext(p.Ctx,"SELECT pg_backend_pid()").Scan(&adminPID);err!=nil{t.Fatal(err)}
 adminQuery:="SELECT pg_log_backend_memory_contexts("+strconv.Itoa(adminPID)+")"
 for _,grant:=range []bool{false,true,false}{
  action:="REVOKE EXECUTE ON FUNCTION "+signalIDs[2]+" FROM "+role
  if grant{action="GRANT EXECUTE ON FUNCTION "+signalIDs[2]+" TO "+role}
  run(fixture.Exec(action,0))
  // ACLs are per database even though PID lookup spans the whole cluster.
  if _,err:=peer.ExecContext(p.Ctx,action);err!=nil{t.Fatal(err)}
  s:=signalError(adminQuery,"42501","permission denied for function pg_log_backend_memory_contexts")
  if grant{s=signalPhysical(adminQuery,2,b.PG)}
  fixture.RunStep(t,p.Ctx,login,loginRecord,s)
 }
 // Close the pool as well as its borrowed handle: returning a handle alone
 // retains a live physical backend and must not be mistaken for process exit.
 if err:=peer.Close();err!=nil{t.Fatal(err)};if err:=pool.Close();err!=nil{t.Fatal(err)}
 if !b.PG{run(fixture.Select(q,[]string{signalNames[2]},[]string{"BOOL"},[][]any{{false}}));signalNotices(t,n,signalWarning(strconv.Itoa(pid)))}
 })
}
