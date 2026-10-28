package reference_test

import (
 "database/sql"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceLtreeArrayDiagnostics(t *testing.T) {
 t.Run("array_input",ltreeArraysInputDiagnostics)
 for _,tc:=range ltreeArrayCases {
  t.Run(tc.name,func(t *testing.T) {
   ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
    ltreeInstall(t,p,b,c)
    for operand,typ:=range []string{tc.leftType,tc.rightType} {
     if !strings.HasSuffix(typ,"[]") { continue }
     for _,invalid:=range []struct{ input,code,message string }{
      {"{{a,NULL}}","2202E","array must be one-dimensional"},
      {"{a,NULL}","22004","array must not contain nulls"},
     } {
      left,right:=ltreeArrayLiteral(tc.left,tc.leftType),ltreeArrayLiteral(tc.right,tc.rightType)
      if operand==0 { left="$1::"+typ } else { right="$1::"+typ }
      query:="SELECT "+tc.expression(left,right,false)+" AS v"
      step:=fixture.ErrorDiagnostic(query,fixture.ControlsReferenceDiagnostic(invalid.code,invalid.message),invalid.input)
      step.SelectRows,step.ErrorStage=true,"execute"
      fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
      fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(step))
      // Whole-argument strictness precedes visited-array validation.
      if operand==0 { right="NULL::"+tc.rightType } else { left="NULL::"+tc.leftType }
      family:="BOOL"; if tc.op=="?@>" || tc.op=="?<@" { family="LTREE" }
      fixture.RunStep(t,p.Ctx,c,b.Recorder,ltreeArraySelect(b,"SELECT "+tc.expression(left,right,false)+" AS v",[]string{"v"},[]string{family},[][]any{{nil}},invalid.input))
     }
    }
   })
  })
 }
 t.Run("precedence_and_signatures",func(t *testing.T) {
  ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
   ltreeInstall(t,p,b,c)
   for _,expression:=range []string{"'{}'::ltree[] ? '{NULL}'::lquery[]","'{NULL}'::lquery[] ? '{}'::ltree[]"} {
    step:=fixture.ErrorDiagnostic("SELECT "+expression,fixture.ControlsReferenceDiagnostic("22004","array must not contain nulls")); step.SelectRows=true
    fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
   }
   for _,expression:=range []string{"'{NULL}'::ltree[] ? '{{a}}'::lquery[]","'{{a}}'::lquery[] ? '{NULL}'::ltree[]"} {
    step:=fixture.ErrorDiagnostic("SELECT "+expression,fixture.ControlsReferenceDiagnostic("2202E","array must be one-dimensional")); step.SelectRows=true
    fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
   }
   for _,tc:=range []struct{ left,op,right,signature string }{
    {"'{}'::ltree[]","@>","'a'::text","ltree[] @> text"},
    {"'a'::ltree","?@>","'{}'::ltree[]","ltree ?@> ltree[]"},
    {"'{}'::ltree[]","~","'a'::ltxtquery","ltree[] ~ ltxtquery"},
    {"'{}'::ltree[]","?","'a'::lquery","ltree[] ? lquery"},
    {"'{}'::ltree[]","@","'a'::lquery","ltree[] @ lquery"},
   } {
    query:="SELECT "+tc.left+" "+tc.op+" "+tc.right
    want:=fixture.ControlsReferenceDiagnostic("42883","operator does not exist: "+tc.signature)
    want.Hint="No operator matches the given name and argument types. You might need to add explicit type casts."
    want.Position=ltreeSQLPosition(query," "+tc.op+" ")
    // The error cursor points at the operator, not its preceding space.
    want.Position=ltreeSQLPosition(query,tc.op+" ")
    ltreeError(t,p,b,c,query,false,true,false,want)
   }
   for _,tc:=range []struct{ input,typ,code,message,detail string }{
    {"a..b","ltree","42601","ltree syntax error at character 3",""},
    {"a.","ltree","42601","ltree syntax error","Unexpected end of input."},
    {"a..b","lquery","42601","lquery syntax error at character 3",""},
    {"a&","ltxtquery","42601","operand syntax error",""},
   } {
    query:="SELECT "+ltreeArrayLiteral(tc.input,tc.typ)
    want:=fixture.ControlsReferenceDiagnostic(tc.code,tc.message); want.Detail=tc.detail; want.Position=ltreeSQLPosition(query,ltreeArrayLiteral(tc.input,tc.typ))
    ltreeError(t,p,b,c,query,false,true,false,want)
   }
  })
 })
 t.Run("atomicity",func(t *testing.T) {
  ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
   ltreeInstall(t,p,b,c)
   run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
   run(fixture.Exec("CREATE TABLE la_bad(id integer,paths ltree[])",0))
   run(fixture.Exec("CREATE TABLE la_dest(id integer,matched boolean)",0))
   run(fixture.Exec("INSERT INTO la_bad VALUES (1,'{a}'),(2,'{a,NULL}')",2))
   run(fixture.Exec("INSERT INTO la_dest VALUES (99,true)",1))
   step:=fixture.ErrorDiagnostic("INSERT INTO la_dest SELECT id,paths @> 'a'::ltree FROM la_bad ORDER BY id",fixture.ControlsReferenceDiagnostic("22004","array must not contain nulls"))
   run(step)
   run(fixture.Select("SELECT id,matched FROM la_dest ORDER BY id",[]string{"id","matched"},[]string{"INT4","BOOL"},[][]any{{int64(99),true}}))
   run(fixture.Exec("ALTER TABLE la_bad ADD COLUMN matched boolean",0))
   run(fixture.ErrorDiagnostic("UPDATE la_bad SET matched=paths @> 'a'::ltree",fixture.ControlsReferenceDiagnostic("22004","array must not contain nulls")))
   run(fixture.Select("SELECT id,matched FROM la_bad ORDER BY id",[]string{"id","matched"},[]string{"INT4","BOOL"},[][]any{{int64(1),nil},{int64(2),nil}}))
  })
 })
}
