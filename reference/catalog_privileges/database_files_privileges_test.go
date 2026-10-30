package reference_test

import (
 "testing"
 "stuntgres/reference/fixture"
)
func TestReferenceDatabaseFilesPrivileges(t *testing.T){
 fixture.RoleReferenceCleanup(t,[]string{"df_reader"})
 // The fixture resets public only; drop this private schema before dropping its grantee.
 fixture.SchemaReferenceCleanup(t,"df_private")
 p:=fixture.NewScenario(t)
 for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){
  conn,err:=b.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer conn.Close()
  run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,conn,b.Recorder,s)}
  for _,q:=range []string{"CREATE ROLE df_reader","CREATE SCHEMA df_private","CREATE TABLE df_private.secret(i INT)","CREATE VIEW df_private.visible AS SELECT i FROM df_private.secret","GRANT USAGE ON SCHEMA df_private TO df_reader"}{run(fixture.Exec(q,0))}
  var tableOID int64
  if err:=conn.QueryRowContext(p.Ctx,"SELECT ('df_private.secret'::regclass)::oid::bigint").Scan(&tableOID);err!=nil{t.Fatal(err)}
  defer func(){if _,err:=conn.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION");err!=nil{t.Error(err)}}()
  for _,f:=range databaseFilesFunctions{
   signature:=f.name+"("+f.formal+")"
   defer func(){if _,err:=conn.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION; GRANT EXECUTE ON FUNCTION pg_catalog."+signature+" TO PUBLIC");err!=nil{t.Error(err)}}()
   q:="SELECT "+f.name+"($1) IS NOT NULL AS v";args:=[]any{"df_private.secret"}
   if f.oid==3454{q="SELECT pg_filenode_relation($1,$2) AS v";args=[]any{int64(0),int64(0)}}
   stmt,err:=conn.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
   held:=fixture.HeldStatement{Executor:conn,Statement:stmt,SQL:q}
   allowed:=func(){
    s:=databaseFilesNull(q,"v",f.result,args...)
    if f.oid!=3454{if b.PG{s=fixture.Select(q,[]string{"v"},[]string{"BOOL"},[][]any{{true}},args...)}else{s=databaseFilesBoundary(q,f.name);s.Args=args}}
    fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
   }
   run(fixture.Exec("SET SESSION AUTHORIZATION df_reader",0));allowed()
   run(databaseFilesNull("SELECT "+f.name+"("+func()string{if f.oid==3454{return "NULL,NULL"};return "NULL"}()+")",f.name,f.result))
   run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
   run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog."+signature+" FROM PUBLIC",0))
   run(fixture.Exec("SET SESSION AUTHORIZATION df_reader",0))
   d:=fixture.ImplicitError(q,fixture.Diagnostic{Code:"42501",Message:"permission denied for function "+f.name},args...);d.SelectRows=true;d.ErrorStage="execute"
   fixture.RunStep(t,p.Ctx,held,b.Recorder,d)
   nullArgs:=[]any{nil};if f.oid==3454{nullArgs=append(nullArgs,int64(0))}
   nullStep:=databaseFilesNull(q,"v",f.result,nullArgs...)
   if f.oid!=3454{nullStep=fixture.Select(q,[]string{"v"},[]string{"BOOL"},[][]any{{false}},nullArgs...)}
   fixture.RunStep(t,p.Ctx,held,b.Recorder,nullStep)
   if f.oid!=3454{d.Args=[]any{"df_private.visible"};fixture.RunStep(t,p.Ctx,held,b.Recorder,d);d.Args=[]any{int64(0)};fixture.RunStep(t,p.Ctx,held,b.Recorder,d)}
   run(fixture.Exec("RESET SESSION AUTHORIZATION",0));run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog."+signature+" TO PUBLIC",0))
   run(fixture.Exec("SET SESSION AUTHORIZATION df_reader",0));allowed();run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
   if f.oid!=3454{
    run(fixture.Exec("REVOKE USAGE ON SCHEMA df_private FROM df_reader",0));run(fixture.Exec("SET SESSION AUTHORIZATION df_reader",0))
    d:=fixture.ImplicitError("SELECT "+f.name+"('df_private.secret')",fixture.Diagnostic{Code:"42501",Message:"permission denied for schema df_private",Position:fixture.Position(8+len(f.name)+1)});d.SelectRows=true;d.ErrorStage="execute";run(d)
    // Numeric REGCLASS input does not perform schema-name lookup.
    args=[]any{tableOID};allowed()
    run(fixture.Exec("RESET SESSION AUTHORIZATION",0));run(fixture.Exec("GRANT USAGE ON SCHEMA df_private TO df_reader",0))
   }
  }
 })}
}
