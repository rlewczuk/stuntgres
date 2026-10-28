package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceLtreeHierarchyPrivileges(t *testing.T) {
 for index,tc:=range ltreeHierarchyCases {
  t.Run(tc.name,func(t *testing.T) {
   fixture.RoleReferenceCleanup(t,[]string{"lh_actor"})
   fixture.SchemaReferenceCleanup(t,"lh_provider")
   ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
    for _,q:=range []string{"CREATE SCHEMA lh_provider","CREATE EXTENSION ltree SCHEMA lh_provider","SET search_path=lh_provider,public"}{fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0))}
    run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
    for _,query:=range []string{"CREATE ROLE lh_actor","GRANT USAGE ON SCHEMA lh_provider TO lh_actor","CREATE TABLE lh_acl(id integer,a "+tc.leftType+",b "+tc.rightType+")","GRANT SELECT ON lh_acl TO lh_actor"} {run(fixture.Exec(query,0))}
    run(fixture.Exec("INSERT INTO lh_acl VALUES (1,"+ltreeArrayLiteral(tc.left,tc.leftType)+","+ltreeArrayLiteral(tc.right,tc.rightType)+"),(2,NULL,NULL)",2))
    query:="SELECT "+tc.expression("a","b",false)+" AS v FROM lh_acl WHERE id=1"
    statement,err:=c.PrepareContext(p.Ctx,query); if err!=nil {t.Fatal(err)}; defer statement.Close()
    held:=fixture.HeldStatement{Executor:c,Statement:statement,SQL:query}
    family:=tc.family()
    success:=ltreeArraySelect(b,query,[]string{"v"},[]string{family},[][]any{{tc.want}})
    run(fixture.Exec("SET ROLE lh_actor",0)); fixture.RunStep(t,p.Ctx,held,b.Recorder,success); run(fixture.Exec("RESET ROLE",0))
    signature:=tc.name+"("+tc.leftType+","+tc.rightType+")"
    run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+signature+" FROM PUBLIC,lh_actor",0))
    run(fixture.Exec("SET ROLE lh_actor",0))
    want:=fixture.ControlsReferenceDiagnostic("42501","permission denied for function "+tc.name)
    for _,direct:=range []bool{false,true} { for _,id:=range []string{"1","2"} {
     step:=fixture.ErrorDiagnostic("SELECT "+tc.expression("a","b",direct)+" FROM lh_acl WHERE id="+id,want); step.SelectRows=true; run(step)
    } }
    step:=fixture.ErrorDiagnostic(query,want); step.SelectRows=true; fixture.RunStep(t,p.Ctx,held,b.Recorder,step)
    // Existing strict typed-literal folding is distinct from stored NULL rows.
    run(ltreeArraySelect(b,"SELECT "+tc.expression("NULL::"+tc.leftType,ltreeArrayLiteral(tc.right,tc.rightType),false)+" AS v",[]string{"v"},[]string{family},[][]any{{nil}}))
    run(fixture.Exec("RESET ROLE",0)); run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+signature+" TO lh_actor",0)); run(fixture.Exec("SET ROLE lh_actor",0))
    fixture.RunStep(t,p.Ctx,held,b.Recorder,success)
    run(fixture.Exec("RESET ROLE",0))
    run(fixture.Exec("REVOKE SELECT ON lh_acl FROM lh_actor",0)); run(fixture.Exec("SET ROLE lh_actor",0))
    denied:=fixture.ErrorDiagnostic(query,fixture.ControlsReferenceDiagnostic("42501","permission denied for table lh_acl")); denied.SelectRows=true; run(denied)
    run(fixture.Exec("RESET ROLE",0))
    if index==0 {
     run(fixture.Exec("GRANT SELECT ON lh_acl TO lh_actor",0))
     run(fixture.Exec("REVOKE USAGE ON SCHEMA lh_provider FROM PUBLIC,lh_actor",0))
     run(fixture.Exec("SET ROLE lh_actor",0))
     schemaQuery:="SELECT a FROM lh_provider.lh_acl"
     want:=fixture.ControlsReferenceDiagnostic("42501","permission denied for schema lh_provider")
     want.Position=ltreeSQLPosition(schemaQuery,"lh_provider.lh_acl")
     denied:=fixture.ErrorDiagnostic(schemaQuery,want);denied.SelectRows=true;run(denied)
     run(fixture.Exec("RESET ROLE",0))
     run(fixture.Exec("GRANT USAGE ON SCHEMA lh_provider TO lh_actor",0))
    }
   })
  })
 }
}
