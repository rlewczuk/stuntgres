package reference_test

import (
 "database/sql"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

func ltreeHierarchyMismatch(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
 t.Helper()
 for _,tc:=range []struct{q,message,token string}{
  {"/*é*/ SELECT 'a'::ltree @> 'a'::text","operator does not exist: ltree @> text","@>"},
  {"/*é*/ SELECT 'a'::text <@ 'a'::ltree","operator does not exist: text <@ ltree","<@"},
 } {
  want:=fixture.ControlsReferenceDiagnostic("42883",tc.message)
  want.Position=ltreeSQLPosition(tc.q,tc.token)
  want.Hint="No operator matches the given name and argument types. You might need to add explicit type casts."
  for _,prepared:=range []bool{false,true}{ltreeError(t,p,b,c,tc.q,prepared,true,true,want)}
 }
}
func TestReferenceLtreeHierarchyDiagnostics(t *testing.T) {
 for _,tc:=range []struct{name,input,code,message,detail string}{
  {"syntax","a..b","42601","ltree syntax error at character 3",""},
  {"trailing","a.","42601","ltree syntax error","Unexpected end of input."},
  {"long",strings.Repeat("a",1001),"42622","label string is too long","Label length is 1001, must be at most 1000, at character 1002."},
  {"deep",strings.Repeat("a.",65535)+"a","54000","number of ltree labels (65536) exceeds the maximum allowed (65535)",""},
 } {t.Run(tc.name,func(t *testing.T){
  ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
   ltreeInstall(t,p,b,c)
   run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
   lit:=queryIntLiteral(tc.input)
   for _,q:=range []string{"/*é*/ SELECT "+lit+"::ltree @> 'a'::ltree","/*é*/ SELECT 'a'::ltree <@ "+lit+"::ltree","/*é*/ SELECT "+lit+"::ltree @> NULL::ltree"}{
    want:=fixture.ControlsReferenceDiagnostic(tc.code,tc.message);want.Detail=tc.detail;want.Position=ltreeSQLPosition(q,lit)
    for _,prepared:=range []bool{false,true}{ltreeError(t,p,b,c,q,prepared,true,true,want)}
   }
   q:="SELECT $1::ltree @> $2::ltree AS ancestor,$1::ltree <@ $2::ltree AS descendant"
   stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
   held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
   for _,right:=range []bool{false,true}{
    args:=[]any{tc.input,"a"};parameter:=1
    if right{args=[]any{"a",[]byte(tc.input)};parameter=2}
    want:=queryIntParameterDiagnostic(tc.code,tc.message,tc.input,parameter);want.Detail=tc.detail
    s:=fixture.ErrorDiagnostic(q,want,args...);s.SelectRows=true;s.ErrorStage="execute"
    fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
    fixture.RunStep(t,p.Ctx,held,b.Recorder,ltreeHierarchyPair(q,"a","a.b",true,false))
   }
   // Bound conversion also precedes strictness when the opposite operand is NULL.
   wantNull:=queryIntParameterDiagnostic(tc.code,tc.message,tc.input,1);wantNull.Detail=tc.detail
   nullFailure:=fixture.ErrorDiagnostic(q,wantNull,tc.input,nil);nullFailure.SelectRows=true;nullFailure.ErrorStage="execute"
   fixture.RunStep(t,p.Ctx,held,b.Recorder,nullFailure)
   fixture.RunStep(t,p.Ctx,held,b.Recorder,ltreeHierarchyPair(q,"a","a.b",true,false))
   run(fixture.Exec("CREATE TABLE lh_atomic(id integer,v boolean)",0))
   iq:="INSERT INTO lh_atomic VALUES (1,$1::ltree @> 'a.b'::ltree),(2,$2::ltree <@ 'a'::ltree)"
   insert,err:=c.PrepareContext(p.Ctx,iq);if err!=nil{t.Fatal(err)};defer insert.Close()
   ih:=fixture.HeldStatement{Executor:c,Statement:insert,SQL:iq}
   want:=queryIntParameterDiagnostic(tc.code,tc.message,tc.input,2);want.Detail=tc.detail
   fixture.RunStep(t,p.Ctx,ih,b.Recorder,fixture.ErrorDiagnostic(iq,want,"a",tc.input))
   run(fixture.Select("SELECT count(*) AS n FROM lh_atomic",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
   fixture.RunStep(t,p.Ctx,ih,b.Recorder,fixture.Exec(iq,2,"a","a.b"))
   run(fixture.Select("SELECT id,v FROM lh_atomic ORDER BY id",[]string{"id","v"},[]string{"INT4","BOOL"},[][]any{{int64(1),true},{int64(2),true}}))
   if tc.name=="syntax"{ltreeHierarchyMismatch(t,p,b,c)}
  })
 })}
}
