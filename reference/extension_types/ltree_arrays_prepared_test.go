package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceLtreeArrayPrepared(t *testing.T) {
 t.Run("where_repeated_formal",ltreeArraysPreparedWhere)
 for index,tc:=range ltreeArrayCases {
  t.Run(tc.name,func(t *testing.T) {
   ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
    ltreeInstall(t,p,b,c)
    query:="SELECT "+tc.expression("$1::"+tc.leftType,"$2::"+tc.rightType,false)+" AS v"
    family:="BOOL"; if index>=12 { family="LTREE" }
    statement,err:=c.PrepareContext(p.Ctx,query); if err!=nil {t.Fatal(err)}; defer statement.Close()
    held:=fixture.HeldStatement{Executor:c,Statement:statement,SQL:query}
    for _,args:=range [][]any{{tc.left,tc.right},{nil,tc.right},{[]byte(tc.left),[]byte(tc.right)},{tc.left,nil},{tc.left,tc.right}} {
     var want any=tc.want; if args[0]==nil || args[1]==nil {want=nil}
     step:=ltreeArraySelect(b,query,[]string{"v"},[]string{family},[][]any{{want}},args...)
     fixture.RunStep(t,p.Ctx,held,b.Recorder,step)
    }
    invalid:=tc.left; badLeft:=tc.leftType=="ltree[]" || tc.leftType=="lquery[]"
    if badLeft {invalid="{a,NULL}"}
    if badLeft {
     step:=fixture.ErrorDiagnostic(query,fixture.ControlsReferenceDiagnostic("22004","array must not contain nulls"),invalid,tc.right); step.SelectRows,step.ErrorStage=true,"execute"
     fixture.RunStep(t,p.Ctx,held,b.Recorder,step)
     fixture.RunStep(t,p.Ctx,held,b.Recorder,ltreeArraySelect(b,query,[]string{"v"},[]string{family},[][]any{{tc.want}},tc.left,tc.right))
    }
   })
  })
 }
 t.Run("owned_inputs_and_recreation",func(t *testing.T) {
  ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
   ltreeInstall(t,p,b,c)
   run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
   run(fixture.Exec("CREATE TABLE la_owned(paths ltree[],queries lquery[])",0))
   paths,queries:=[]byte("{a.b}"),[]byte("{*.b}")
   run(fixture.Exec("INSERT INTO la_owned VALUES ($1,$2)",1,paths,queries))
   clear(paths); clear(queries)
   run(ltreeArrayResult(b,"SELECT paths ? queries AS v FROM la_owned",true))
   query:="SELECT $1::text::ltree[] ? $2::text::lquery[] AS v"
   statement,err:=c.PrepareContext(p.Ctx,query); if err!=nil {t.Fatal(err)}; defer statement.Close()
   held:=fixture.HeldStatement{Executor:c,Statement:statement,SQL:query}
   literalQuery:="SELECT '{a.b}'::ltree[] ? '{*.b}'::lquery[] AS v"
   literal,err:=c.PrepareContext(p.Ctx,literalQuery); if err!=nil {t.Fatal(err)}; defer literal.Close()
   for _,q:=range []string{"DROP TABLE la_owned","DROP EXTENSION ltree","CREATE EXTENSION ltree"} {run(fixture.Exec(q,0))}
   fixture.RunStep(t,p.Ctx,held,b.Recorder,ltreeArrayResult(b,query,true,"{a.b}","{*.b}"))
   fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:literal,SQL:literalQuery},b.Recorder,ltreeArrayResult(b,literalQuery,true))
  })
 })
}

func ltreeArraysPreparedWhere(t *testing.T){
 ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  ltreeInstall(t,p,b,c)
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE la_where(id integer,paths ltree[])",0))
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO la_where VALUES (1,'{Top,Top.Science}'),(2,'{Other}'),(3,'{}'),(4,NULL)",4))
  query:="SELECT id FROM la_where WHERE paths @> $1::ltree AND $1::ltree <@ paths ORDER BY id"
  statement,err:=c.PrepareContext(p.Ctx,query);if err!=nil{t.Fatal(err)};defer statement.Close()
  held:=fixture.HeldStatement{Executor:c,Statement:statement,SQL:query}
  for _,arg:=range []any{"Top.Science",nil,[]byte("Top.Science"),"Missing"}{
   want:=[][]any{};if arg!=nil && arg!="Missing"{want=[][]any{{int64(1)}}}
   step:=fixture.Select(query,[]string{"id"},[]string{"INT4"},want,arg)
   fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
   fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(step))
   fixture.RunStep(t,p.Ctx,held,b.Recorder,step)
  }
 })
}
