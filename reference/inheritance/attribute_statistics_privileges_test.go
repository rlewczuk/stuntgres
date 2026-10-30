package reference_test

import (
 "context"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceAttributeStatisticsPrivileges(t *testing.T){
 fixture.RoleReferenceCleanup(t,[]string{"ast_actor"})
 withAttributeStats(t,func(s attributeStatsScenario){
  s.exec("CREATE TABLE ast_t(a INT,b INT)");s.exec("CREATE ROLE ast_actor")
  actorDropped:=false
  s.t.Cleanup(func(){
   if actorDropped{return}
   for _,q:=range []string{"RESET ROLE","DROP VIEW IF EXISTS ast_view","DROP TABLE IF EXISTS ast_t CASCADE","REVOKE USAGE ON SCHEMA public FROM ast_actor","DROP ROLE IF EXISTS ast_actor"}{if _,err:=s.conn.ExecContext(context.Background(),q);err!=nil{s.t.Errorf("authority fixture cleanup %s: %v",q,err)}}
  })
  s.exec("GRANT USAGE ON SCHEMA public TO ast_actor")
  s.restore("'null_frac',.25::real",true)
  s.fail(attributeStatsRestore("missing",false,""),"42703",`column "missing" of relation "ast_t" does not exist`)
  s.fail(attributeStatsRestore("ctid",false,""),"0A000",`cannot modify statistics on system column "ctid"`)
  s.fail("SELECT pg_clear_attribute_stats('public','ast_t','ctid',false)","0A000",`cannot clear statistics on system column "ctid"`)
  s.fail(strings.Replace(attributeStatsRestore("a",false,""),"'ast_t'","'ast_missing'",1),"42P01",`relation "public.ast_missing" does not exist`)
  s.fail(strings.Replace(attributeStatsRestore("a",false,""),"'public'","'ast_missing_schema'",1),"3F000",`schema "ast_missing_schema" does not exist`)
  s.fail("SELECT pg_clear_attribute_stats('ast_missing_schema','ast_t','a',false)","3F000",`schema "ast_missing_schema" does not exist`)
  s.exec("ALTER TABLE ast_t ADD COLUMN ast_dropped integer");s.exec("ALTER TABLE ast_t DROP COLUMN ast_dropped")
  s.fail(attributeStatsRestore("ast_dropped",false,""),"42703",`column "ast_dropped" of relation "ast_t" does not exist`)
  s.fail("SELECT pg_clear_attribute_stats('public','ast_t','ast_dropped',false)","42703",`column "ast_dropped" of relation "ast_t" does not exist`)
  for _,n:=range []string{"0","3","-7"}{s.fail("SELECT pg_restore_attribute_stats('schemaname','public','relname','ast_t','attnum',"+n+"::smallint,'inherited',false)","42703",`column `+n+` of relation "ast_t" does not exist`)}
  s.exec("CREATE VIEW ast_view AS SELECT a FROM ast_t")
  d:=fixture.ControlsReferenceDiagnostic("42809",`cannot modify statistics for relation "ast_view"`);d.Detail="This operation is not supported for views."
  s.run(fixture.ImplicitError(strings.Replace(attributeStatsRestore("a",false,""),"'ast_t'","'ast_view'",1),d))
  s.exec("SET ROLE ast_actor")
  s.fail(attributeStatsRestore("a",false,"'null_frac',.5::real"),"42501","permission denied for table ast_t")
  s.exec("RESET ROLE");s.exec("GRANT SELECT ON ast_t TO ast_actor");s.exec("SET ROLE ast_actor")
  s.fail(attributeStatsRestore("a",false,""),"42501","permission denied for table ast_t")
  s.exec("RESET ROLE");s.exec("REVOKE SELECT ON ast_t FROM ast_actor");s.exec("GRANT MAINTAIN ON ast_t TO ast_actor");s.exec("SET ROLE ast_actor")
  s.restore("'null_frac',.5::real",true);s.scalars()
  s.exec("RESET ROLE");s.exec("GRANT SELECT(a) ON ast_t TO ast_actor");s.exec("SET ROLE ast_actor")
  s.scalars([]any{"a",false,float64(.5),int64(0),float64(0)})
  s.exec("RESET ROLE");s.exec("ALTER TABLE ast_t ENABLE ROW LEVEL SECURITY");s.exec("SET ROLE ast_actor");s.scalars();s.exec("RESET ROLE")
  s.scalars([]any{"a",false,float64(.5),int64(0),float64(0)})
  s.exec("ALTER TABLE ast_t FORCE ROW LEVEL SECURITY")
  // The fixture actor is superuser and therefore still bypasses forced RLS.
  s.scalars([]any{"a",false,float64(.5),int64(0),float64(0)})
  attributeStatsAuthorityEdges(s)
  attributeStatsNonheapAuthority(s)
  s.exec("DROP VIEW ast_view");s.exec("DROP TABLE ast_t");s.exec("REVOKE USAGE ON SCHEMA public FROM ast_actor");s.exec("DROP ROLE ast_actor");actorDropped=true
 })
}
