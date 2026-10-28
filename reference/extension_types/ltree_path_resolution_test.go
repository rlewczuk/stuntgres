package reference_test

import (
 "database/sql"
 "fmt"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceLtreePathResolutionAndPrivileges(t *testing.T) {
 t.Run("selection_and_relocation",func(t *testing.T) {
  fixture.SchemaReferenceCleanup(t,"lp_provider");fixture.SchemaReferenceCleanup(t,"lp_moved")
  ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
   run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
   for _,q:=range []string{"CREATE SCHEMA lp_provider","CREATE SCHEMA lp_moved","CREATE EXTENSION ltree SCHEMA lp_provider","SET search_path=public,lp_provider","CREATE DOMAIN lp_domain AS ltree"}{run(fixture.Exec(q,0))}
   for _,q:=range []string{
    "SELECT subpath('A.B.C',1) AS v","SELECT lp_provider.subpath('A.B.C'::ltree,1) AS v",
    "SELECT subpath('A.B.C'::lp_domain,1) AS v","SELECT 'B'::ltree OPERATOR(lp_provider.||) 'C'::text AS v",
   }{s:=ltreePathResult(b,q,"LTREE","B.C");run(s);run(fixture.Prepared(s))}
   for _,tc:=range []struct{q,message,token string}{
    {"SELECT lca('A'::ltree)","function lca(ltree) does not exist","lca"},
    {"SELECT lca('A'::ltree,'A'::ltree,'A'::ltree,'A'::ltree,'A'::ltree,'A'::ltree,'A'::ltree,'A'::ltree,'A'::ltree)","function lca(ltree, ltree, ltree, ltree, ltree, ltree, ltree, ltree, ltree) does not exist","lca"},
    {"SELECT subpath('A'::text,1)","function subpath(text, integer) does not exist","subpath"},
    {"SELECT nlevel('A'::text)","function nlevel(text) does not exist","nlevel"},
   }{want:=fixture.ControlsReferenceDiagnostic("42883",tc.message);want.Position=ltreeSQLPosition(tc.q,tc.token);want.Hint="No function matches the given name and argument types. You might need to add explicit type casts.";for _,prepared:=range []bool{false,true}{ltreeError(t,p,b,c,tc.q,prepared,true,true,want)}}
   run(fixture.Exec("ALTER FUNCTION lp_provider.subpath(ltree,integer) SET SCHEMA lp_moved",0))
   run(fixture.Exec("ALTER FUNCTION lp_provider.ltree_textadd(text,ltree) SET SCHEMA lp_moved",0))
   run(fixture.Exec("ALTER OPERATOR lp_provider.||(ltree,text) SET SCHEMA lp_moved",0))
   run(fixture.Exec("ALTER FUNCTION lp_moved.subpath(ltree,integer) RENAME TO lp_slice",0))
   run(ltreePathResult(b,"SELECT lp_moved.lp_slice('A.B.C'::ltree,1) AS v","LTREE","B.C"))
   run(ltreePathResult(b,"SELECT 'A'::ltree OPERATOR(lp_moved.||) 'B'::text AS v","LTREE","A.B"))
   run(ltreePathResult(b,"SELECT lp_moved.ltree_textadd('A'::text,'B'::ltree) AS v","LTREE","A.B"))
  })
 })
 for index,tc:=range ltreePathDeclarations {t.Run(fmt.Sprintf("privilege_%02d",index),func(t *testing.T) {
  fixture.RoleReferenceCleanup(t,[]string{"lp_actor"})
  ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
   ltreeInstall(t,p,b,c);run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
   for _,q:=range []string{"CREATE ROLE lp_actor","GRANT USAGE ON SCHEMA public TO lp_actor","CREATE TABLE lp_acl(id integer,p ltree,t text,a ltree[])","GRANT SELECT ON lp_acl TO lp_actor"}{run(fixture.Exec(q,0))}
   run(fixture.Exec("INSERT INTO lp_acl VALUES (1,'A.B.C','D','{A.B.C,A.B.D}'),(2,NULL,NULL,NULL)",2))
   formals:=strings.Split(tc.signature,",");args:=make([]string,len(formals));nulls:=make([]string,len(formals))
   for i,formal:=range formals{switch formal{case "ltree":args[i]="p";case "text":args[i]="t";case "ltree[]":args[i]="a";case "integer":args[i]="0";if tc.name=="subltree"&&i==2{args[i]="1"}};nulls[i]="NULL::"+formal}
   call:=tc.name+"("+strings.Join(args,",")+")";q:="SELECT "+call+" AS v FROM lp_acl WHERE id=$1::integer"
   stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
   held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
   run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+tc.name+"("+tc.signature+") FROM PUBLIC,lp_actor",0));run(fixture.Exec("SET ROLE lp_actor",0))
   want:=fixture.ControlsReferenceDiagnostic("42501","permission denied for function "+tc.name)
   for _,id:=range []int{1,2}{s:=fixture.ErrorDiagnostic(q,want,id);s.SelectRows=true;run(s);run(fixture.Prepared(s));fixture.RunStep(t,p.Ctx,held,b.Recorder,s)}
   // A strict literal-NULL call has no invocation; preserve the shared fold contract.
   family:="LTREE";if tc.result=="integer"{family="INT4"};if tc.result=="text"{family="TEXT"}
   folded:=ltreePathResult(b,"SELECT "+tc.name+"("+strings.Join(nulls,",")+") AS v",family,nil);run(folded);run(fixture.Prepared(folded))
   run(fixture.Exec("RESET ROLE",0));run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+tc.name+"("+tc.signature+") TO lp_actor",0));run(fixture.Exec("SET ROLE lp_actor",0))
   fixture.RunStep(t,p.Ctx,held,b.Recorder,ltreePathResult(b,q,family,nil,2))
   run(fixture.Exec("RESET ROLE",0))
  })
 })}
}
