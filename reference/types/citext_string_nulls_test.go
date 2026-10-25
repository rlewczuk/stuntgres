package reference_test

import (
 "database/sql"
 "fmt"
 "strings"
 "testing"

 "stuntgres/reference/fixture"
)

func citextStringParameterDiagnostic(code,message string,index int) fixture.Diagnostic {
 d:=fixture.ControlsReferenceDiagnostic(code,message)
 d.Context=fmt.Sprintf("unnamed portal parameter $%d = '...'",index)
 return d
}
func TestReferenceCitextStringNulls(t *testing.T) {
 for _,d:=range citextStringDeclarations {t.Run(d.name,func(t *testing.T){
 citextStringScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 citextInstall(t,p,b,c)
 q:=d.query();stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close()
 held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
 good:=citextStringValue(q,d.result,d.expected,d.values...)
 fixture.RunStep(t,p.Ctx,held,b.Recorder,good)
 for i:=range d.args {
  args:=append([]any(nil),d.values...);args[i]=nil
  s:=citextStringValue(q,d.result,nil,args...)
  citextStringRoutes(t,p,b,c,s);fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
  fixture.RunStep(t,p.Ctx,held,b.Recorder,good)
  literals:=make([]string,len(args))
  for j,typ:=range d.args {
   if j==i {literals[j]="NULL::"+typ} else if typ=="integer" {literals[j]="2::integer"} else {literals[j]="'a'::"+typ}
  }
  citextStringRoutes(t,p,b,c,citextStringValue("SELECT public."+d.name+"("+strings.Join(literals,",")+") AS v",d.result,nil))
  literals[i]="NULL"
  citextStringRoutes(t,p,b,c,citextStringValue("SELECT public."+d.name+"("+strings.Join(literals,",")+") AS v",d.result,nil))
 }
 allNull:=make([]any,len(d.args));fixture.RunStep(t,p.Ctx,held,b.Recorder,citextStringValue(q,d.result,nil,allNull...))
 nullLiterals:=make([]string,len(d.args));for i:=range nullLiterals {nullLiterals[i]="NULL"}
 citextStringRoutes(t,p,b,c,citextStringValue("SELECT public."+d.name+"("+strings.Join(nullLiterals,",")+") AS v",d.result,nil))
 citextStringRoutes(t,p,b,c,citextStringValue("SELECT "+d.name+"("+strings.Join(nullLiterals,",")+") AS v",d.result,nil))
 for i,typ:=range d.args {
  if typ=="integer" {continue}
  for _,bad:=range []struct{value,message string}{
   {"a\x00b",`invalid byte sequence for encoding "UTF8": 0x00`},
   {string([]byte{0xff}),`invalid byte sequence for encoding "UTF8": 0xff`},
  } {
   args:=append([]any(nil),d.values...);args[i]=bad.value
   diagnostic:=fixture.ControlsReferenceDiagnostic("22021",bad.message)
   diagnostic.Context=fmt.Sprintf("unnamed portal parameter $%d",i+1)
   failure:=fixture.ErrorDiagnostic(q,diagnostic,args...);failure.SelectRows=true;failure.ErrorStage="execute"
   fixture.RunStep(t,p.Ctx,held,b.Recorder,failure)
   fixture.RunStep(t,p.Ctx,held,b.Recorder,good)
   // Bind decoding is not erased by a strict NULL in a different slot.
   other:=(i+1)%len(args);args[other]=nil
   failure=fixture.ErrorDiagnostic(q,diagnostic,args...);failure.SelectRows=true;failure.ErrorStage="execute"
   fixture.RunStep(t,p.Ctx,held,b.Recorder,failure)
   fixture.RunStep(t,p.Ctx,held,b.Recorder,good)
  }
 }
 if d.name=="split_part" {
  args:=[]any{"AbA","a","bad"}
  failure:=fixture.ErrorDiagnostic(q,citextStringParameterDiagnostic("22P02",`invalid input syntax for type integer: "bad"`,3),args...)
  failure.SelectRows=true;failure.ErrorStage="execute"
  fixture.RunStep(t,p.Ctx,held,b.Recorder,failure)
  args[0]=nil;failure.Args=args;fixture.RunStep(t,p.Ctx,held,b.Recorder,failure)
  fixture.RunStep(t,p.Ctx,held,b.Recorder,citextStringValue(q,d.result,nil,"AbA","a",nil))
  fixture.RunStep(t,p.Ctx,held,b.Recorder,good)
 }
 })})}
}
