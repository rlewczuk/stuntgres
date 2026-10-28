package reference_test

import ("database/sql";"strings";"testing";"stuntgres/reference/fixture")

func TestReferenceControlDataDiagnostics(t *testing.T) {
 recoveryScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
 const hint="No function matches the given name and argument types. You might need to add explicit type casts."
 type sample struct {q,code,message,hint string;pos int}
 var cases []sample
 for _,name:=range controlNames {
  for _,a:=range []struct{sql,types string}{{"1","integer"},{"true","boolean"},{"'x'","unknown"},{"NULL","unknown"},{"NULL::integer","integer"},{"1,2","integer, integer"},{"x=>1","x => integer"}} {
   cases=append(cases,sample{"SELECT "+name+"("+a.sql+")","42883","function "+name+"("+a.types+") does not exist",hint,8})
  }
  cases=append(cases,
   sample{"SELECT public."+name+"()","42883","function public."+name+"() does not exist",hint,8},
   sample{"SELECT missing_schema."+name+"()","3F000",`schema "missing_schema" does not exist`,"",8},
   sample{`SELECT "`+strings.ToUpper(name)+`"()`,"42883","function "+strings.ToUpper(name)+"() does not exist",hint,8},
   sample{"SELECT pg_catalog."+name+"(1)","42883","function pg_catalog."+name+"(integer) does not exist",hint,8},
   sample{"/*é🙂*/ SELECT "+name+"(NULL)","42883","function "+name+"(unknown) does not exist",hint,15},
   sample{"SELECT "+name+"(*)","42809",name+"(*) specified, but "+name+" is not an aggregate function","",8})
  q:="SELECT "+name+"('broken'::integer)"
  cases=append(cases,sample{q,"22P02",`invalid input syntax for type integer: "broken"`,"",strings.Index(q,"'broken'")+1})
  q="SELECT "+name+"(x=>1,x=>2)"
  cases=append(cases,sample{q,"42601",`argument name "x" used more than once`,"",strings.LastIndex(q,"x=>")+1})
  q="SELECT "+name+"(x=>1,2)"
  cases=append(cases,sample{q,"42601","positional argument cannot follow named argument","",strings.LastIndex(q,"2")+1})
  q="SELECT ("+name+"()).missing"
  cases=append(cases,sample{q,"42703",`could not identify column "missing" in record data type`,"",9})
 }
 for _,tc:=range cases {for _,prepared:=range []bool{false,true} {
  s:=fixture.ImplicitError(tc.q,fixture.Diagnostic{Code:tc.code,Message:tc.message,Hint:tc.hint,Position:fixture.Position(tc.pos)});s.SelectRows=true;s.Prepared=prepared;s.ErrorStage="execute";if prepared {s.ErrorStage="prepare"};fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
 }}
 })
}
