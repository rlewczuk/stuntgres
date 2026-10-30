package reference_test

import (
 "context"
 "fmt"
 "stuntgres/reference/fixture"
)

// REL_18_6 RangeVarCallbackForStats and aclcheck_error use the effective
// parent's object kind; metadata-only foreign statistics never invoke a handler.
func attributeStatsNonheapAuthority(s attributeStatsScenario) {
 s.t.Helper()
 defer func(){
  for _,q:=range []string{"RESET ROLE","DROP MATERIALIZED VIEW IF EXISTS ast_materialized","DROP FOREIGN TABLE IF EXISTS ast_foreign","DROP TABLE IF EXISTS ast_partitioned","DROP SERVER IF EXISTS ast_stats_server","DROP FOREIGN DATA WRAPPER IF EXISTS ast_stats_fdw"}{if _,err:=s.conn.ExecContext(context.Background(),q);err!=nil{s.t.Errorf("nonheap cleanup %s: %v",q,err)}}
 }()
 s.exec("CREATE MATERIALIZED VIEW ast_materialized AS SELECT a FROM ast_t")
 s.exec("CREATE INDEX ast_materialized_idx ON ast_materialized(a)")
 s.exec("CREATE FOREIGN DATA WRAPPER ast_stats_fdw")
 s.exec("CREATE SERVER ast_stats_server FOREIGN DATA WRAPPER ast_stats_fdw")
 s.exec("CREATE FOREIGN TABLE ast_foreign(a integer) SERVER ast_stats_server")
 s.exec("CREATE TABLE ast_partitioned(a integer) PARTITION BY RANGE(a)")
 restore:=func(name string,inherited bool)string{return fmt.Sprintf("SELECT pg_restore_attribute_stats('schemaname','public','relname','%s','attname','a','inherited',%t,'null_frac',.25::real)",name,inherited)}
 clear:=func(name string,inherited bool)string{return fmt.Sprintf("SELECT pg_clear_attribute_stats('public','%s','a',%t)",name,inherited)}
 for _,tc:=range []struct{name,noun string}{{"ast_materialized","materialized view"},{"ast_foreign","foreign table"},{"ast_partitioned","table"}}{
  s.exec("SET ROLE ast_actor")
  message:="permission denied for "+tc.noun+" "+tc.name
  s.fail(restore(tc.name,false),"42501",message)
  s.fail(clear(tc.name,false),"42501",message)
  if tc.name=="ast_materialized"{
   s.fail(restore("ast_materialized_idx",false),"42501",message)
   s.fail(clear("ast_materialized_idx",false),"42501",message)
  }
  s.exec("RESET ROLE")
  s.exec("GRANT MAINTAIN,SELECT ON "+tc.name+" TO ast_actor")
  s.exec("SET ROLE ast_actor")
  for _,inherited:=range []bool{false,true}{s.run(fixture.Select(restore(tc.name,inherited),[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{true}}))}
  query:="SELECT attname,inherited,null_frac FROM pg_stats WHERE schemaname='public' AND tablename='"+tc.name+"' ORDER BY inherited"
  s.run(fixture.Select(query,[]string{"attname","inherited","null_frac"},[]string{"NAME","BOOL","FLOAT4"},[][]any{{"a",false,float64(.25)},{"a",true,float64(.25)}}))
  s.run(fixture.Select(clear(tc.name,false),[]string{"pg_clear_attribute_stats"},[]string{"VOID"},[][]any{{""}}))
  s.run(fixture.Select(query,[]string{"attname","inherited","null_frac"},[]string{"NAME","BOOL","FLOAT4"},[][]any{{"a",true,float64(.25)}}))
  s.run(fixture.Select(clear(tc.name,true),[]string{"pg_clear_attribute_stats"},[]string{"VOID"},[][]any{{""}}))
  s.run(fixture.Select(query,[]string{"attname","inherited","null_frac"},[]string{"NAME","BOOL","FLOAT4"},nil))
  s.exec("RESET ROLE")
 }
}
