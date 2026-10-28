package reference_test

import (
 "database/sql"
 "fmt"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

// Each installed scalar overload emits one row, even for a strict NULL result.
func ltreePathSourceChecks(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
 t.Helper()
 expected:=[]any{"A","A","B.C",int64(0),int64(-1),int64(3),"A.B.C","D","A.B","A.B","A.B","A.B","A.B","A.B","A.B","A.B","A.B.C.A.B.C","A.B.C.D","D.A.B.C"}
 for index,decl:=range ltreePathDeclarations {
  t.Run(fmt.Sprintf("scalar_source_%02d",index),func(t *testing.T){
   roles:=strings.Split(decl.signature,",");args:=make([]any,len(roles));written:=make([]string,len(roles))
   for i,role:=range roles{
    written[i]=fmt.Sprintf("$%d::%s",i+1,role)
    switch role{
    case "ltree":args[i]="A.B.C"
    case "text":args[i]="D"
    case "ltree[]":args[i]="{A.B.C,A.B.D}"
    case "integer":args[i]=0;if i==2||index==2{args[i]=1}
    }
   }
   family:="LTREE";if decl.result=="integer"{family="INT4"};if decl.result=="text"{family="TEXT"}
   query:="SELECT v FROM "+decl.name+"("+strings.Join(written,",")+") AS f(v)"
   stmt,err:=c.PrepareContext(p.Ctx,query);if err!=nil{t.Fatal(err)};defer stmt.Close()
   held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:query}
   for _,null:=range []bool{false,true}{
    actual:=append([]any(nil),args...);want:=expected[index]
    if null{actual[0]=nil;want=nil}
    step:=ltreePathResult(b,query,family,want,actual...)
    fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
    fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(step))
    fixture.RunStep(t,p.Ctx,held,b.Recorder,step)
   }
  })
 }
}
