package reference_test

import (
 "database/sql"
 "strings"
 "testing"

 "stuntgres/reference/fixture"
)

func TestReferenceCitextStringPrivileges(t *testing.T) {
 for _,d:=range citextStringDeclarations {t.Run(d.name,func(t *testing.T){
 fixture.RoleReferenceCleanup(t,[]string{"cs_reader"})
 citextStringScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  citextInstall(t,p,b,c)
  run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
  run(fixture.Exec("CREATE ROLE cs_reader",0));run(fixture.Exec("GRANT USAGE ON SCHEMA public TO cs_reader",0))
  q:=d.query();stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close()
  held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
  good:=citextStringValue(q,d.result,d.expected,d.values...)
  defer c.ExecContext(p.Ctx,"RESET ROLE")
  run(fixture.Exec("SET ROLE cs_reader",0))
  citextStringRoutes(t,p,b,c,good);fixture.RunStep(t,p.Ctx,held,b.Recorder,good)
  run(fixture.Exec("RESET ROLE",0))
  run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+d.signature()+" FROM PUBLIC",0))
  fixture.RunStep(t,p.Ctx,held,b.Recorder,good)
  run(fixture.Exec("SET ROLE cs_reader",0))
  denied:=fixture.ErrorDiagnostic(q,fixture.ControlsReferenceDiagnostic("42501","permission denied for function "+d.name),d.values...);denied.SelectRows=true;denied.ErrorStage="execute"
  // New prepared statements are admitted at execution, not prepared as the member owner.
  citextStringRoutes(t,p,b,c,denied);fixture.RunStep(t,p.Ctx,held,b.Recorder,denied)
  for i,typ:=range d.args {
   args:=append([]any(nil),d.values...);args[i]=nil
   nullStep:=citextStringValue(q,d.result,nil,args...)
   citextStringRoutes(t,p,b,c,nullStep);fixture.RunStep(t,p.Ctx,held,b.Recorder,nullStep)
   literals:=make([]string,len(d.args));for j,formal:=range d.args {
    if j==i {literals[j]="NULL::"+typ} else if formal=="integer" {literals[j]="2"} else {literals[j]="'a'::"+formal}
   }
   citextStringRoutes(t,p,b,c,citextStringValue("SELECT public."+d.name+"("+strings.Join(literals,",")+") AS v",d.result,nil))
  }
  // A nullable stored value does not prove that admission can be pruned.
  run(fixture.Exec("RESET ROLE",0))
  run(fixture.Exec("CREATE TABLE cs_acl_null(s citext)",0));run(fixture.Exec("INSERT INTO cs_acl_null VALUES(NULL)",1));run(fixture.Exec("GRANT SELECT ON cs_acl_null TO cs_reader",0))
  run(fixture.Exec("SET ROLE cs_reader",0))
  rowQuery:="SELECT public."+d.name+"(s,'a'::citext"
  if d.name=="replace" {rowQuery+=",'X'::citext"};if d.name=="split_part" {rowQuery+=",2"};if d.name=="translate" {rowQuery+=",'X'::text"};rowQuery+=") AS v FROM cs_acl_null"
  rowDenied:=fixture.ErrorDiagnostic(rowQuery,fixture.ControlsReferenceDiagnostic("42501","permission denied for function "+d.name));rowDenied.SelectRows=true
  citextStringRoutes(t,p,b,c,rowDenied)
  // Text sibling must remain usable by the same reader.
  textQuery:=strings.ReplaceAll(strings.ReplaceAll(q,"public.","pg_catalog."),"::citext","::text")
  textValue:=any("AbA");if d.name=="strpos" {textValue=int64(0)};if d.name=="split_part" {textValue=""}
  citextStringRoutes(t,p,b,c,citextStringValue(textQuery,d.result,textValue,d.values...))
  run(fixture.Exec("RESET ROLE",0));run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+d.signature()+" TO cs_reader",0));run(fixture.Exec("SET ROLE cs_reader",0))
  fixture.RunStep(t,p.Ctx,held,b.Recorder,good);citextStringRoutes(t,p,b,c,good)
  run(fixture.Exec("RESET ROLE",0));run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+d.signature()+" FROM cs_reader",0));run(fixture.Exec("SET ROLE cs_reader",0))
  fixture.RunStep(t,p.Ctx,held,b.Recorder,denied)
  run(fixture.Exec("RESET ROLE",0));run(fixture.Exec("REVOKE USAGE ON SCHEMA public FROM PUBLIC",0));run(fixture.Exec("REVOKE USAGE ON SCHEMA public FROM cs_reader",0));run(fixture.Exec("SET ROLE cs_reader",0))
  namespaceQuery:=strings.ReplaceAll(q,"::citext","::public.citext")
  namespace:=fixture.ControlsReferenceDiagnostic("42501","permission denied for schema public");namespace.Position=citextPosition(namespaceQuery,"public.citext")
  for _,prepared:=range []bool{false,true} {citextError(t,p,b,c,namespaceQuery,prepared,true,true,namespace,d.values...)}
 })})}
}
