package reference_test

import (
 "database/sql"
 "fmt"
 "testing"
 "stuntgres/reference/fixture"
)

func fsmError(query string,raw []byte,code,message,detail string)fixture.Step{s:=fixture.ImplicitError(query,fixture.Diagnostic{Code:code,Message:message,Detail:detail},raw);s.ErrorStage="execute";return s}
func fsmAuthority(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,queries []string){
 t.Helper();run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
 run(fixture.Exec("CREATE ROLE fsm_reader",0));run(fixture.Exec("GRANT USAGE ON SCHEMA fsm_part TO fsm_reader",0))
 defer func(){if _,err:=c.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION");err!=nil{t.Error(err)}}()
 for _,query:=range queries{
  st,err:=c.PrepareContext(p.Ctx,query);if err!=nil{t.Fatal(err)};held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:query}
  run(fixture.Exec("SET SESSION AUTHORIZATION fsm_reader",0))
  for _,raw:=range [][]byte{fixture.FSMSparse(),{},make([]byte,8192)}{
   s:=fsmError(query,raw,"42501","must be superuser to use raw page functions","");fsmBoth(t,p,b,c,s);fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
   // Strict NULL remains usable immediately after every authority failure.
   fsmBoth(t,p,b,c,fsmStep(query,nil,nil));fixture.RunStep(t,p.Ctx,held,b.Recorder,fsmStep(query,nil,nil))
  }
  run(fixture.Exec("RESET SESSION AUTHORIZATION",0));fixture.RunStep(t,p.Ctx,held,b.Recorder,fsmStep(query,fixture.FSMSparse(),fixture.FSMSparseText));if err:=st.Close();err!=nil{t.Fatal(err)}
 }
}
func TestReferencePageinspectFSMDiagnostics(t *testing.T){fixture.RoleReferenceCleanup(t,[]string{"fsm_reader"});fsmCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 for _,query:=range []string{fsmScalar,fsmSource}{for _,n:=range []int{0,3,8191,8193}{for _,prepared:=range []bool{false,true}{s:=fsmError(query,make([]byte,n),"22023","invalid page size",fmt.Sprintf("Expected 8192 bytes, got %d.",n));if prepared{s=fixture.Prepared(s)};fixture.RunStep(t,p.Ctx,c,b.Recorder,s);success:=fsmStep(query,fixture.FSMSparse(),fixture.FSMSparseText);if prepared{success=fixture.Prepared(success)};fixture.RunStep(t,p.Ctx,c,b.Recorder,success)}}}
 fsmAuthority(t,p,b,c,[]string{fsmScalar,fsmSource})
})}
