package reference_test
import (
 "bytes"
 "context"
 "database/sql"
 "errors"
 "strings"
 "testing"
 "stuntgres"
 "stuntgres/reference/fixture"
)
func TestReferenceServerSignalACLRestore(t *testing.T){
 const role="signal_restore_caller";fixture.RoleReferenceCleanup(t,[]string{role})
 signalScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,n *[]fixture.Diagnostic){
 run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
 run(fixture.Exec("CREATE ROLE "+role+" LOGIN",0));defer signalRestore(t,c,role)()
 run(fixture.Exec("CREATE VIEW signal_restore_view AS SELECT pg_log_backend_memory_contexts(NULL::integer) AS v",0))
 run(fixture.Exec("GRANT SELECT ON signal_restore_view TO "+role,0))
 run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+role,0))
 // Every logical ACL state has an independently asserted effective privilege.
 for _,state:=range []string{"seed","grant","revoke","null","empty"}{
  if state=="grant"{for _,id:=range signalIDs{run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+id+" TO "+role+" WITH GRANT OPTION",0))}}
  if state=="revoke"{for _,id:=range signalIDs{run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+id+" FROM "+role,0))}}
  raw:="";if state=="null"{raw="NULL::pg_catalog.aclitem[]"};if state=="empty"{raw=`pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid)::pg_catalog.aclitem[]`}
  var dump bytes.Buffer
  if b.PG {if raw!=""{run(fixture.Exec("UPDATE pg_catalog.pg_proc SET proacl="+raw+" WHERE oid IN (2621,2622,4543)",3))}} else {
   if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil{t.Fatal(err)}
   if state=="seed"{for _,id:=range signalIDs{if strings.Contains(dump.String(),"SET proacl=")&&strings.Contains(dump.String(),"'pg_catalog."+id+"'::"){t.Fatal("seed serialized",dump.String())}}}
   if raw!=""{for _,id:=range signalIDs{dump.WriteString("\nUPDATE pg_catalog.pg_proc SET proacl="+raw+" WHERE oid='pg_catalog."+id+"'::pg_catalog.regprocedure;\n")}}
   if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
   if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump.String()));err!=nil{t.Fatal(err)}
  }
  permitted:=state=="grant"||state=="null"
  for _,id:=range signalIDs{run(fixture.Select("SELECT has_function_privilege('"+role+"','"+id+"','EXECUTE') AS allowed",[]string{"allowed"},[]string{"BOOL"},[][]any{{permitted}}));run(fixture.Select("SELECT proacl IS NULL AS absent FROM pg_proc WHERE oid='"+id+"'::regprocedure",[]string{"absent"},[]string{"BOOL"},[][]any{{state=="null"}}));if state=="empty"{run(fixture.Select("SELECT proacl::text AS acl FROM pg_proc WHERE oid='"+id+"'::regprocedure",[]string{"acl"},[]string{"TEXT"},[][]any{{"{}"}}))}}
  for _,id:=range signalIDs{
   run(fixture.Select("SELECT has_function_privilege('"+role+"','"+id+"','EXECUTE WITH GRANT OPTION') AS allowed",[]string{"allowed"},[]string{"BOOL"},[][]any{{state=="grant"}}))
   if state=="seed"||state=="revoke"{run(fixture.Select("SELECT proacl::text AS acl FROM pg_proc WHERE oid='"+id+"'::regprocedure",[]string{"acl"},[]string{"TEXT"},[][]any{{recoveryControlOwnerACL(b.PG)}}))}
   if state=="grant"{owner:="postgres";if b.PG{owner="stuntgres"};run(fixture.Select("SELECT proacl::text AS acl FROM pg_proc WHERE oid='"+id+"'::regprocedure",[]string{"acl"},[]string{"TEXT"},[][]any{{"{"+owner+"=X/"+owner+","+role+"=X*/"+owner+"}"}}))}
  }
  run(signalInitial(b.PG));run(fixture.Exec("SET SESSION AUTHORIZATION "+role,0))
  run(signalNull("SELECT v FROM signal_restore_view"))
  for i:=range signalNames{q:="SELECT "+signalCalls[i];s:=signalError(q,"42501","permission denied for function "+signalNames[i]);if permitted{s=signalPhysical(q,i,b.PG)};run(s);if permitted&&b.PG&&i==1{signalNotices(t,n,fixture.Diagnostic{Severity:"WARNING",SeverityUnlocalized:"WARNING",Code:"01000",Message:"rotation not possible because log collection not active"})}else{signalNotices(t,n)}}
  run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
  if !b.PG{
   var a,z bytes.Buffer
   if err:=stuntgres.Dump(p.Ctx,p.Native,&a);err!=nil{t.Fatal(err)}
   if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(a.String()));err!=nil{t.Fatal(err)}
   if err:=stuntgres.Dump(p.Ctx,p.Native,&z);err!=nil||a.String()!=z.String(){t.Fatal("nondeterministic redump",err)}
   connector,err:=stuntgres.NewDriver().OpenConnector("signal_destination_"+state);if err!=nil{t.Fatal(err)}
   target:=sql.OpenDB(connector)
   if _,err:=target.ExecContext(p.Ctx,"CREATE ROLE "+role+" LOGIN");err!=nil{t.Fatal(err)}
   if err:=stuntgres.Restore(p.Ctx,target,strings.NewReader(a.String()));err!=nil{t.Fatal(err)}
   for _,id:=range signalIDs{var allowed,option bool;if err:=target.QueryRowContext(p.Ctx,"SELECT has_function_privilege('"+role+"','"+id+"','EXECUTE'),has_function_privilege('"+role+"','"+id+"','EXECUTE WITH GRANT OPTION')").Scan(&allowed,&option);err!=nil||allowed!=permitted||option!=(state=="grant"){t.Fatal("remapped ACL",state,id,allowed,option,err)}}
   if err:=target.Close();err!=nil{t.Fatal(err)}
  }
 }
 if b.PG{return}
 run(fixture.Exec("CREATE TABLE signal_restore_sentinel(v integer)",0));run(fixture.Exec("INSERT INTO signal_restore_sentinel VALUES(7)",1))
 rows,err:=c.QueryContext(p.Ctx,"SELECT pg_log_backend_memory_contexts(NULL) AS v");if err!=nil{t.Fatal(err)};defer rows.Close();signalMetadata(t,rows)
 var retained bytes.Buffer
 if err:=stuntgres.Dump(p.Ctx,p.Native,&retained);err!=nil{t.Fatal(err)}
 if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
 if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(retained.String()));err!=nil{t.Fatal(err)}
 // Failed and pre-canceled staged restores must not replace committed roots.
 if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader("CREATE TABLE broken ("));err==nil{t.Fatal("invalid restore succeeded")}
 canceled,cancel:=context.WithCancel(p.Ctx);cancel();if err:=stuntgres.Restore(canceled,p.Native,strings.NewReader(""));!errors.Is(err,context.Canceled){t.Fatal("canceled restore",err)}
 if !rows.Next(){t.Fatal("retained NULL row",rows.Err())};var v any;if err:=rows.Scan(&v);err!=nil||v!=nil{t.Fatal(v,err)};signalMetadata(t,rows);rows.Close()
 run(fixture.Select("SELECT v FROM signal_restore_sentinel",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))
 for _,id:=range signalIDs{run(fixture.Select("SELECT has_function_privilege('"+role+"','"+id+"','EXECUTE') AS allowed",[]string{"allowed"},[]string{"BOOL"},[][]any{{false}}))}
 })
}
