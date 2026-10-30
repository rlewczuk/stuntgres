package reference_test

import (
 "context"
 "database/sql"
 "testing"

 "stuntgres/reference/fixture"
)

func TestReferenceRoutineTransformDefinitionReadmission(t *testing.T) {
 for _,action:=range []string{"replace","drop","acl"} {
  t.Run(action,func(t *testing.T){
   fixture.RoleReferenceCleanup(t,[]string{"td_readmission_actor"})
   definitionScenarioRuntime(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *transformRuntime){
    definitionAnnotate(t,p,b,c)
    hostExec(t,p,b,c,definitionCreate("td_a_from","td_b_to",false))
    definitionFunction(t,p,b,c,"td_readmission","selection-v1",transformsSelectionPython)
    hostBag(t,p,b,c,"td_readmission('a=>7,b=>NULL'::hstore)",transformsMapRows)
    if b.PG {return} // SQL oracle witness above; Go readmission is a native ABI phase.
    hostExec(t,p,b,c,"CREATE OR REPLACE FUNCTION td_readmission(v hstore) RETURNS hstore LANGUAGE td_lang TRANSFORM FOR TYPE hstore AS 'wait-v1'")
    hostExec(t,p,b,c,"CREATE TABLE td_readmission_effects(v integer)")
    fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO td_readmission_effects VALUES(11)",1))
    prefix:="CREATE FUNCTION td_readmission_prefix(v hstore) RETURNS hstore LANGUAGE plpgsql AS $$ BEGIN INSERT INTO td_readmission_effects VALUES(22); RETURN td_readmission(v); END $$"
    hostExec(t,p,b,c,prefix)
    if action=="acl" {
     hostExec(t,p,b,c,"CREATE ROLE td_readmission_actor NOSUPERUSER NOINHERIT")
     defer routineLanguagesCleanup(t,c,"RESET ROLE","DROP OWNED BY td_readmission_actor CASCADE","DROP ROLE td_readmission_actor")()
     hostExec(t,p,b,c,"GRANT USAGE ON SCHEMA public TO td_readmission_actor")
     hostExec(t,p,b,c,"GRANT INSERT ON td_readmission_effects TO td_readmission_actor")
     hostExec(t,p,b,c,"SET ROLE td_readmission_actor")
    }
    q:="SELECT td_readmission_prefix($1::hstore)"
    held:=transformsHeld(t,p,c,q)
    before:=r.body.Load()
    r.mu.Lock();r.gate=func(ctx context.Context)error{
     mutation:=definitionCreate("td_scaled_from","td_projected_to",true)
     if action=="drop" {mutation="DROP TRANSFORM FOR hstore LANGUAGE td_lang CASCADE"}
     if action=="acl" {mutation="REVOKE EXECUTE ON FUNCTION td_readmission(hstore) FROM PUBLIC"}
     _,err:=p.Native.ExecContext(ctx,mutation);return err
    };r.mu.Unlock()
    defer func(){r.mu.Lock();r.gate=nil;r.mu.Unlock()}()
    rows,err:=held.QueryContext(p.Ctx,q,"a=>7,b=>NULL")
    if rows!=nil {for rows.Next(){};if err==nil {err=rows.Err()};rows.Close()}
    if err==nil {t.Fatal("stale callback continuation published success")}
    if r.body.Load()!=before+1 {t.Fatal("successful competitor retried application callback")}
    if action=="acl" {
     got:=fixture.ExtractDiagnostic(t,err)
     if got.Code!="42501" || got.Message!="permission denied for function td_readmission" {t.Fatalf("post-callback current EXECUTE readmission=%#v",got)}
     hostExec(t,p,b,c,"RESET ROLE")
     hostExec(t,p,b,c,"GRANT EXECUTE ON FUNCTION td_readmission(hstore) TO PUBLIC")
    }
    r.mu.Lock();r.gate=nil;r.mu.Unlock()
    hostRows(t,p,b,c,"SELECT v FROM td_readmission_effects",[]string{"v"},[]string{"INT4"},[][]any{{int64(11)}})
    want:=transformsMapRows
    if action=="replace" {definitionPair(t,p,b,c,"td_scaled_from","td_projected_to");want=[][]any{{"a","71"},{"b",nil},{"c","3"},{"z","out"}}}else if action=="drop" {
     definitionAbsent(t,p,b,c)
     hostExec(t,p,b,c,definitionCreate("td_a_from","td_b_to",false))
     definitionFunction(t,p,b,c,"td_readmission","selection-v1",transformsSelectionPython)
     // The quoted PL body is deferred, so DROP retires the actual host
     // function but not this wrapper. Preserve its SAME held handle and let
     // ordinary execution rebind the recreated host identity.
     hostRows(t,p,b,c,"SELECT count(*) AS n FROM pg_proc WHERE proname='td_readmission_prefix'",[]string{"n"},[]string{"INT8"},[][]any{{int64(1)}})
    }
    if action!="drop" {hostExec(t,p,b,c,"CREATE OR REPLACE FUNCTION td_readmission(v hstore) RETURNS hstore LANGUAGE td_lang TRANSFORM FOR TYPE hstore AS 'selection-v1'")}
    rows,err=held.QueryContext(p.Ctx,q,"a=>7,b=>NULL");if err!=nil {t.Fatal(err)}
    if !rows.Next(){rows.Close();t.Fatal("missing held recovery row")};var value string;if err=rows.Scan(&value);err!=nil {rows.Close();t.Fatal(err)}
    if rows.Next(){t.Error("extra recovery row")};if err=rows.Err();err!=nil{t.Error(err)};rows.Close()
    hostBag(t,p,b,c,"$1::hstore",want,value)
    hostRows(t,p,b,c,"SELECT v FROM td_readmission_effects ORDER BY v",[]string{"v"},[]string{"INT4"},[][]any{{int64(11)},{int64(22)}})
   })
  })
 }
}
