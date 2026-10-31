package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)
func hashAuthority(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 t.Helper();run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
 run(fixture.Exec("CREATE ROLE hash_reader",0));run(fixture.Exec("GRANT USAGE ON SCHEMA public,hash_part TO hash_reader",0))
 run(fixture.Exec("CREATE TABLE public.hash_acl_inputs(raw bytea)",0));run(fixture.Exec("INSERT INTO public.hash_acl_inputs VALUES(NULL)",1));run(fixture.Exec("GRANT SELECT ON public.hash_acl_inputs TO hash_reader",0))
 defer func(){if _,err:=c.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION");err!=nil{t.Error(err)}}()
 for i,name:=range fixture.HashMembers{
  st,err:=c.PrepareContext(p.Ctx,hashQuery(i));if err!=nil{t.Fatal(err)};held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:hashQuery(i)}
  runtimeSQL:="SELECT hash_part."+name+"(raw) FROM public.hash_acl_inputs";runtimeStatement,err:=c.PrepareContext(p.Ctx,runtimeSQL);if err!=nil{t.Fatal(err)};runtimeHeld:=fixture.HeldStatement{Executor:c,Statement:runtimeStatement,SQL:runtimeSQL}
  run(fixture.Exec("ALTER FUNCTION hash_part."+name+"(bytea) OWNER TO hash_reader",0))
  run(fixture.Exec("SET SESSION AUTHORIZATION hash_reader",0))
  for _,raw:=range [][]byte{fixture.HashOrdinary(i),{},make([]byte,8192),{1,2,3}}{bad:=hashError(i,raw,"42501","must be superuser to use raw page functions","");fixture.BRINBoth(t,p,b,c,bad);fixture.RunStep(t,p.Ctx,held,b.Recorder,bad)}
  null:=hashStep(i,nil,fixture.HashNullRows(i));hashBoth(t,p,b,c,null);fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.HashMetadata(null,b.PG,false))
  fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("ALTER ROLE hash_reader SUPERUSER",0));fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.HashMetadata(hashStep(i,fixture.HashOrdinary(i),fixture.HashRows(i)),b.PG,false))
  fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("ALTER ROLE hash_reader NOSUPERUSER",0));fixture.RunStep(t,p.Ctx,held,b.Recorder,hashError(i,fixture.HashOrdinary(i),"42501","must be superuser to use raw page functions",""))
  run(fixture.Exec("RESET SESSION AUTHORIZATION",0));run(fixture.Exec("ALTER FUNCTION hash_part."+name+"(bytea) OWNER TO CURRENT_USER",0))
  run(fixture.Exec("REVOKE EXECUTE ON FUNCTION hash_part."+name+"(bytea) FROM PUBLIC",0));run(fixture.Exec("SET SESSION AUTHORIZATION hash_reader",0))
  if i!=0{denied:=hashError(i,nil,"42501","permission denied for function "+name,"");fixture.BRINBoth(t,p,b,c,denied);fixture.RunStep(t,p.Ctx,held,b.Recorder,denied)}
  if i==0{hashBoth(t,p,b,c,fixture.Select("SELECT hash_part.hash_page_type(NULL::bytea)",[]string{"hash_page_type"},[]string{"TEXT"},[][]any{{nil}}))}
  runtime:=fixture.ImplicitError(runtimeSQL,fixture.Diagnostic{Code:"42501",Message:"permission denied for function "+name});runtime.SelectRows=true;runtime.ErrorStage="execute";fixture.BRINBoth(t,p,b,c,runtime);fixture.RunStep(t,p.Ctx,runtimeHeld,b.Recorder,runtime)
  run(fixture.Exec("RESET SESSION AUTHORIZATION",0));run(fixture.Exec("GRANT EXECUTE ON FUNCTION hash_part."+name+"(bytea) TO PUBLIC",0));run(fixture.Exec("SET SESSION AUTHORIZATION hash_reader",0));fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.HashMetadata(null,b.PG,false));run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
  if err:=st.Close();err!=nil{t.Fatal(err)}
  if err:=runtimeStatement.Close();err!=nil{t.Fatal(err)}
 }
}
func TestReferencePageinspectHashPermissions(t *testing.T){fixture.RoleReferenceCleanup(t,[]string{"hash_reader"});hashCase(t,hashAuthority)}
