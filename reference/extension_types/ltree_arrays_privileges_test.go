package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceLtreeArrayPrivileges(t *testing.T) {
 for index,tc:=range ltreeArrayCases {
  t.Run(tc.name,func(t *testing.T) {
   fixture.RoleReferenceCleanup(t,[]string{"la_actor"})
   ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
    ltreeInstall(t,p,b,c)
    run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
    for _,query:=range []string{"CREATE ROLE la_actor","GRANT USAGE ON SCHEMA public TO la_actor","CREATE TABLE la_acl(id integer,a "+tc.leftType+",b "+tc.rightType+")","GRANT SELECT ON la_acl TO la_actor"} {run(fixture.Exec(query,0))}
    run(fixture.Exec("INSERT INTO la_acl VALUES (1,"+ltreeArrayLiteral(tc.left,tc.leftType)+","+ltreeArrayLiteral(tc.right,tc.rightType)+"),(2,NULL,NULL)",2))
    query:="SELECT "+tc.expression("a","b",false)+" AS v FROM la_acl WHERE id=1"
    statement,err:=c.PrepareContext(p.Ctx,query); if err!=nil {t.Fatal(err)}; defer statement.Close()
    held:=fixture.HeldStatement{Executor:c,Statement:statement,SQL:query}
    family:="BOOL"; if index>=12 {family="LTREE"}
    success:=ltreeArraySelect(b,query,[]string{"v"},[]string{family},[][]any{{tc.want}})
    run(fixture.Exec("SET ROLE la_actor",0)); fixture.RunStep(t,p.Ctx,held,b.Recorder,success); run(fixture.Exec("RESET ROLE",0))
    signature:=tc.name+"("+tc.leftType+","+tc.rightType+")"
    run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+signature+" FROM PUBLIC,la_actor",0))
    run(fixture.Exec("SET ROLE la_actor",0))
    want:=fixture.ControlsReferenceDiagnostic("42501","permission denied for function "+tc.name)
    for _,direct:=range []bool{false,true} { for _,id:=range []string{"1","2"} {
     step:=fixture.ErrorDiagnostic("SELECT "+tc.expression("a","b",direct)+" FROM la_acl WHERE id="+id,want); step.SelectRows=true; run(step)
    } }
    step:=fixture.ErrorDiagnostic(query,want); step.SelectRows=true; fixture.RunStep(t,p.Ctx,held,b.Recorder,step)
    // Existing strict typed-literal folding is distinct from stored NULL rows.
    run(ltreeArraySelect(b,"SELECT "+tc.expression("NULL::"+tc.leftType,ltreeArrayLiteral(tc.right,tc.rightType),false)+" AS v",[]string{"v"},[]string{family},[][]any{{nil}}))
    run(fixture.Exec("RESET ROLE",0)); run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+signature+" TO la_actor",0)); run(fixture.Exec("SET ROLE la_actor",0))
    fixture.RunStep(t,p.Ctx,held,b.Recorder,success)
    run(fixture.Exec("RESET ROLE",0))
    run(fixture.Exec("REVOKE SELECT ON la_acl FROM la_actor",0)); run(fixture.Exec("SET ROLE la_actor",0))
    denied:=fixture.ErrorDiagnostic(query,fixture.ControlsReferenceDiagnostic("42501","permission denied for table la_acl")); denied.SelectRows=true; run(denied)
    run(fixture.Exec("RESET ROLE",0))
    if index==0 {
     run(fixture.Exec("GRANT SELECT ON la_acl TO la_actor",0))
     run(fixture.Exec("REVOKE USAGE ON SCHEMA public FROM PUBLIC,la_actor",0))
     run(fixture.Exec("SET ROLE la_actor",0))
     schemaQuery:="SELECT a FROM public.la_acl"
     want:=fixture.ControlsReferenceDiagnostic("42501","permission denied for schema public")
     want.Position=ltreeSQLPosition(schemaQuery,"public.la_acl")
     denied:=fixture.ErrorDiagnostic(schemaQuery,want);denied.SelectRows=true;run(denied)
     run(fixture.Exec("RESET ROLE",0))
     run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC",0))
    }
   })
  })
 }
}
