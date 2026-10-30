package reference_test
import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)
func TestReferenceServerSignalDiagnostics(t *testing.T){signalScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,n *[]fixture.Diagnostic){
 const hint="No function matches the given name and argument types. You might need to add explicit type casts."
 type sample struct{q,code,message,hint string;pos int}
 cases:=[]sample{
 {"SELECT pg_reload_conf(1)","42883","function pg_reload_conf(integer) does not exist",hint,8},
 {"SELECT pg_rotate_logfile(NULL)","42883","function pg_rotate_logfile(unknown) does not exist",hint,8},
 {"SELECT pg_log_backend_memory_contexts('oops')","22P02",`invalid input syntax for type integer: "oops"`,"",39},
 {"SELECT pg_log_backend_memory_contexts('2147483648')","22003",`value "2147483648" is out of range for type integer`,"",39},
 {`SELECT "PG_RELOAD_CONF"()`,"42883","function PG_RELOAD_CONF() does not exist",hint,8},
 {"SELECT public.pg_reload_conf()","42883","function public.pg_reload_conf() does not exist",hint,8},
 {"SELECT missing_signal_schema.pg_reload_conf()","3F000",`schema "missing_signal_schema" does not exist`,"",8},
 {"/*é🙂*/ SELECT pg_reload_conf(1)","42883","function pg_reload_conf(integer) does not exist",hint,15},
 }
 for _,a:=range []struct{sql,typ string}{{"",""},{"1,2","integer, integer"},{"true","boolean"},{"'1'::text","text"},{"1::bigint","bigint"},{"pid=>1","pid => integer"},{"pid:=1","pid => integer"}}{cases=append(cases,sample{"SELECT pg_log_backend_memory_contexts("+a.sql+")","42883","function pg_log_backend_memory_contexts("+a.typ+") does not exist",hint,8})}
 for _,tc:=range cases{for _,prepared:=range []bool{false,true}{s:=fixture.ImplicitError(tc.q,fixture.Diagnostic{Code:tc.code,Message:tc.message,Hint:tc.hint,Position:fixture.Position(tc.pos)});s.SelectRows=true;s.Prepared=prepared;s.ErrorStage="execute";if prepared{s.ErrorStage="prepare"};fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}}
 for _,arg:=range []string{"-1::smallint","'-1'"}{fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT pg_log_backend_memory_contexts("+arg+") AS v",[]string{"v"},[]string{"BOOL"},[][]any{{false}}));signalNotices(t,n,signalWarning("-1"))}
 q:="SELECT pg_log_backend_memory_contexts($1) AS v"
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(fixture.Select(q,[]string{"v"},[]string{"BOOL"},[][]any{{false}},-1)));signalNotices(t,n,signalWarning("-1"))
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(signalNull(q,nil)));signalNotices(t,n)
 d:=fixture.Diagnostic{Code:"22P02",Message:`invalid input syntax for type integer: "oops"`,Context:"unnamed portal parameter $1 = '...'"};s:=fixture.ImplicitError(q,d,"oops");s.SelectRows=true;s.Prepared=true;s.ErrorStage="execute";fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
})}
