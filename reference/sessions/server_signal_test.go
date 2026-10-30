package reference_test

import (
 "context"
 "database/sql"
 "reflect"
 "testing"
 "time"
 "stuntgres/reference/fixture"
)
var signalNames=[]string{"pg_reload_conf","pg_rotate_logfile","pg_log_backend_memory_contexts"}
var signalIDs=[]string{"pg_reload_conf()","pg_rotate_logfile()","pg_log_backend_memory_contexts(integer)"}
var signalCalls=[]string{"pg_reload_conf()","pg_rotate_logfile()","pg_log_backend_memory_contexts(pg_backend_pid())"}
var signalMechanisms=[]string{"configuration-file reload","log-file rotation","backend memory-context logging"}
func signalScenario(t *testing.T,body func(*testing.T,*fixture.Pair,fixture.Backend,*sql.Conn,*[]fixture.Diagnostic)){
 t.Helper();p:=fixture.NewScenario(t)
 for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){
  var notices []fixture.Diagnostic
  _,c,r:=fixture.NoticeReferencePool(t,p,b.PG,&notices);b.Recorder=r
  if b.PG {defer func(){ctx,cancel:=context.WithTimeout(context.Background(),10*time.Second);defer cancel();for _,q:=range []string{"RESET SESSION AUTHORIZATION","UPDATE pg_catalog.pg_proc SET proacl='{stuntgres=X/stuntgres}'::aclitem[] WHERE oid IN (2621,2622,4543)"}{if _,err:=c.ExecContext(ctx,q);err!=nil{t.Error(err)}}}()}
  body(t,p,b,c,&notices)
 })}
}
func signalError(query,code,message string)fixture.Step{s:=fixture.ImplicitError(query,fixture.Diagnostic{Code:code,Message:message});s.SelectRows=true;s.ErrorStage="execute";return s}
func signalPhysical(query string,i int,pg bool)fixture.Step{
 if !pg{return signalError(query,"0A000",signalNames[i]+" "+signalMechanisms[i]+" is unsupported in embedded mode")}
 return fixture.Select(query,[]string{signalNames[i]},[]string{"BOOL"},[][]any{{i!=1}})
}
func signalRoutes(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,s fixture.Step){t.Helper();fixture.RunStep(t,p.Ctx,c,b.Recorder,s);fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(s));recoveryHeld(t,p,b,c,s)}
func signalRestore(t *testing.T,c *sql.Conn,roles ...string)func(){return func(){ctx,cancel:=context.WithTimeout(context.Background(),10*time.Second);defer cancel();if _,err:=c.ExecContext(ctx,"RESET SESSION AUTHORIZATION");err!=nil{t.Error(err);return};for _,id:=range signalIDs{for _,role:=range append([]string{"PUBLIC"},roles...){if _,err:=c.ExecContext(ctx,"REVOKE ALL ON FUNCTION "+id+" FROM "+role);err!=nil{t.Error(err)}}}}}
func signalNull(q string,args ...any)fixture.Step{return fixture.Select(q,[]string{"v"},[]string{"BOOL"},[][]any{{nil}},args...)}
func signalWarning(pid string)fixture.Diagnostic{return fixture.Diagnostic{Severity:"WARNING",SeverityUnlocalized:"WARNING",Code:"01000",Message:"PID "+pid+" is not a PostgreSQL server process"}}
func signalNotices(t *testing.T,got *[]fixture.Diagnostic,want ...fixture.Diagnostic){t.Helper();if len(*got)!=len(want){t.Fatalf("notices %#v want %#v",*got,want)};for i:=range want{if (*got)[i]!=want[i]{t.Fatalf("notice %#v want %#v",(*got)[i],want[i])}};*got=nil}
func signalMetadata(t *testing.T,rows *sql.Rows){t.Helper();cs,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)};for _,c:=range cs{if c.DatabaseTypeName()!="BOOL" || c.ScanType()!=reflect.TypeFor[bool](){t.Fatal(c)};if n,ok:=c.Nullable();n||ok{t.Fatal("nullability",n,ok)};if _,ok:=c.Length();ok{t.Fatal("length")};if _,_,ok:=c.DecimalSize();ok{t.Fatal("precision")}}}
func TestReferenceServerSignalTypes(t *testing.T){signalScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,n *[]fixture.Diagnostic){
 for i,q:=range []string{"SELECT pg_reload_conf(),pg_rotate_logfile(),pg_log_backend_memory_contexts(1) WHERE FALSE","SELECT pg_reload_conf() AS a,pg_rotate_logfile() AS b,pg_log_backend_memory_contexts(1) AS c WHERE FALSE"}{names:=signalNames;if i==1{names=[]string{"a","b","c"}};s:=fixture.Select(q,names,[]string{"BOOL","BOOL","BOOL"},nil);s.CheckMetadata=signalMetadata;signalRoutes(t,p,b,c,s)}
 for _,q:=range []string{"SELECT pg_log_backend_memory_contexts(NULL) AS v","SELECT pg_catalog.PG_LOG_BACKEND_MEMORY_CONTEXTS(NULL::integer) AS v","SELECT v FROM pg_log_backend_memory_contexts(NULL) AS v"}{s:=signalNull(q);s.CheckMetadata=signalMetadata;signalRoutes(t,p,b,c,s)}
 signalRoutes(t,p,b,c,fixture.Select("SELECT COALESCE(pg_log_backend_memory_contexts(NULL),false) AS v",[]string{"v"},[]string{"BOOL"},[][]any{{false}}));signalNotices(t,n)
})}
func TestReferenceServerSignalBoundary(t *testing.T){signalScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,n *[]fixture.Diagnostic){
 run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
 run(fixture.Select("SHOW logging_collector",[]string{"logging_collector"},[]string{"TEXT"},[][]any{{"off"}}))
 run(fixture.Exec("CREATE TABLE signal_sentinel(v integer)",0));run(fixture.Exec("INSERT INTO signal_sentinel VALUES(7)",1))
 var stamp any;if err:=c.QueryRowContext(p.Ctx,"SELECT pg_conf_load_time()").Scan(&stamp);err!=nil{t.Fatal(err)}
 var workMem,logLevel string
 if err:=c.QueryRowContext(p.Ctx,"SELECT current_setting('work_mem'),current_setting('log_min_messages')").Scan(&workMem,&logLevel);err!=nil{t.Fatal(err)}
 for i,call:=range signalCalls{for _,prepared:=range []bool{false,true}{s:=signalPhysical("SELECT "+call,i,b.PG);if prepared{s=fixture.Prepared(s)};run(s);if b.PG&&i==1{signalNotices(t,n,fixture.Diagnostic{Severity:"WARNING",SeverityUnlocalized:"WARNING",Code:"01000",Message:"rotation not possible because log collection not active"})}else{signalNotices(t,n)}}}
 for _,pid:=range []string{"-1","0"}{for _,prepared:=range []bool{false,true}{s:=fixture.Select("SELECT pg_log_backend_memory_contexts("+pid+") AS v",[]string{"v"},[]string{"BOOL"},[][]any{{false}});if prepared{s=fixture.Prepared(s)};run(s);signalNotices(t,n,signalWarning(pid))}}
 run(fixture.Select("SELECT pg_log_backend_memory_contexts(-1) AS a,pg_log_backend_memory_contexts(-1) AS b",[]string{"a","b"},[]string{"BOOL","BOOL"},[][]any{{false,false}}))
 signalNotices(t,n,signalWarning("-1"),signalWarning("-1"))
 run(fixture.Exec("SET client_min_messages=error",0));run(fixture.Select("SELECT pg_log_backend_memory_contexts(-1) AS v",[]string{"v"},[]string{"BOOL"},[][]any{{false}}));signalNotices(t,n);run(fixture.Exec("RESET client_min_messages",0))
 run(fixture.Select("SELECT v FROM signal_sentinel",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))
 if !b.PG{var after any;if err:=c.QueryRowContext(p.Ctx,"SELECT pg_conf_load_time()").Scan(&after);err!=nil||!reflect.DeepEqual(stamp,after){t.Fatal("changed load time",stamp,after,err)};run(fixture.Exec("BEGIN",0));run(signalPhysical("SELECT pg_reload_conf()",0,false));run(signalError("SELECT pg_reload_conf()","25P02","current transaction is aborted, commands ignored until end of transaction block"));run(fixture.Exec("ROLLBACK",0))}
 signalAtomicity(t,p,b,c,n)
 if !b.PG{var afterMem,afterLog string;if err:=c.QueryRowContext(p.Ctx,"SELECT current_setting('work_mem'),current_setting('log_min_messages')").Scan(&afterMem,&afterLog);err!=nil||afterMem!=workMem||afterLog!=logLevel{t.Fatal("physical request changed settings",workMem,afterMem,logLevel,afterLog,err)}}
})}
