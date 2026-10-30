package reference_test

import (
 "context"
 "fmt"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceRelationStatisticsTargetsPrivileges(t *testing.T){
 fixture.RoleReferenceCleanup(t,[]string{"rst_actor","rst_owner","rst_member"})
 fixture.SchemaReferenceCleanup(t,"rst_private")
 withRelationStats(t,func(s relationStatsScenario){
  s.exec("CREATE TABLE rst_t(a int)");s.exec("CREATE ROLE rst_actor");s.exec("CREATE ROLE rst_owner");s.exec("CREATE ROLE rst_member")
  defer func(){for _,q:=range []string{"RESET ROLE","DROP SCHEMA IF EXISTS rst_private CASCADE","DROP MATERIALIZED VIEW IF EXISTS rst_mat CASCADE","DROP FOREIGN TABLE IF EXISTS rst_foreign","DROP TABLE IF EXISTS rst_part CASCADE","DROP TABLE IF EXISTS rst_t CASCADE","DROP SEQUENCE IF EXISTS rst_seq","DROP SERVER IF EXISTS rst_server","DROP FOREIGN DATA WRAPPER IF EXISTS rst_fdw","REVOKE USAGE ON SCHEMA public FROM rst_actor,rst_owner,rst_member","DROP ROLE rst_member","DROP ROLE rst_actor","DROP ROLE rst_owner"}{if _,err:=s.conn.ExecContext(context.Background(),q);err!=nil{s.t.Errorf("cleanup %s: %v",q,err)}}}()
  s.exec("GRANT USAGE ON SCHEMA public TO rst_actor,rst_owner,rst_member")
  s.exec("CREATE INDEX rst_idx ON rst_t(a)")
  s.exec("CREATE TABLE rst_part(a int,CONSTRAINT rst_part_idx PRIMARY KEY(a)) PARTITION BY RANGE(a)")
  s.run(fixture.Select("SELECT relkind::text FROM pg_class WHERE oid='rst_part_idx'::regclass",[]string{"relkind"},[]string{"TEXT"},[][]any{{"I"}}))
  s.exec("CREATE MATERIALIZED VIEW rst_mat AS SELECT a FROM rst_t")
  s.exec("CREATE FOREIGN DATA WRAPPER rst_fdw");s.exec("CREATE SERVER rst_server FOREIGN DATA WRAPPER rst_fdw");s.exec("CREATE FOREIGN TABLE rst_foreign(a int) SERVER rst_server")
  for _,name:=range []string{"rst_t","rst_idx","rst_part","rst_part_idx","rst_mat","rst_foreign"}{
   s.run(fixture.Select(relationStatsRestore(name,"'relpages',0,'reltuples',17.5::real,'relallvisible',0,'relallfrozen',0"),[]string{"pg_restore_relation_stats"},[]string{"BOOL"},[][]any{{true}}))
   s.run(fixture.Select(relationStatsRead(name),[]string{"relpages","reltuples","relallvisible","relallfrozen"},[]string{"INT4","FLOAT4","INT4","INT4"},[][]any{{int64(0),float64(17.5),int64(0),int64(0)}}))
   s.run(fixture.Select(fmt.Sprintf("SELECT pg_clear_relation_stats('public','%s')",name),[]string{"pg_clear_relation_stats"},[]string{"VOID"},[][]any{{""}}))
  }
  s.restore("'reltuples',21.5::real",true)
  s.run(fixture.Select(relationStatsRestore("rst_idx","'reltuples',99::real"),[]string{"pg_restore_relation_stats"},[]string{"BOOL"},[][]any{{true}}));s.stats(0,21.5,0,0)
  s.exec("CREATE VIEW rst_view AS SELECT a FROM rst_t");s.exec("CREATE SEQUENCE rst_seq")
  for _,tc:=range []struct{name,detail string}{{"rst_view","This operation is not supported for views."},{"rst_seq","This operation is not supported for sequences."}}{
   d:=fixture.ControlsReferenceDiagnostic("42809",fmt.Sprintf(`cannot modify statistics for relation "%s"`,tc.name));d.Detail=tc.detail
   s.run(fixture.ImplicitError(relationStatsRestore(tc.name,""),d))
   s.run(fixture.ImplicitError(fmt.Sprintf("SELECT pg_clear_relation_stats('public','%s')",tc.name),d))
  }
  s.fail("SELECT pg_clear_relation_stats('rst_absent','rst_t')","3F000",`schema "rst_absent" does not exist`)
  s.fail(relationStatsRestore("rst_missing",""),"42P01",`relation "public.rst_missing" does not exist`)
  for _,q:=range []string{"SELECT pg_clear_relation_stats('pg_catalog','pg_authid')","SELECT pg_restore_relation_stats('schemaname','pg_catalog','relname','pg_authid','reltuples',1::real)"}{s.fail(q,"0A000","cannot modify statistics for shared relation")}
  s.exec("SET ROLE rst_actor");s.fail(relationStatsRestore("rst_t","'reltuples',1::real"),"42501","permission denied for table rst_t")
  s.exec("RESET ROLE");s.exec("GRANT SELECT ON rst_t TO rst_actor");s.exec("SET ROLE rst_actor");s.fail(relationStatsRestore("rst_idx",""),"42501","permission denied for table rst_t")
  s.exec("RESET ROLE");s.exec("REVOKE SELECT ON rst_t FROM rst_actor");s.exec("GRANT MAINTAIN ON rst_t TO rst_actor");s.exec("SET ROLE rst_actor")
  s.restore("'reltuples',22.5::real",true)
  s.run(fixture.Select(relationStatsRestore("rst_idx","'relpages',37"),[]string{"pg_restore_relation_stats"},[]string{"BOOL"},[][]any{{true}}))
  s.exec("RESET ROLE");s.stats(0,22.5,0,0)
  // Reuse an actual held recipe after revocation, never a newly prepared copy.
  stmt,err:=s.conn.PrepareContext(s.p.Ctx,relationStatsRestore("rst_t","'reltuples',50::real"));if err!=nil{s.t.Fatal(err)};defer stmt.Close()
  s.exec("REVOKE MAINTAIN ON rst_t FROM rst_actor");s.exec("SET ROLE rst_actor")
  var ok bool;err=stmt.QueryRowContext(s.p.Ctx).Scan(&ok);d:=fixture.ControlsReferenceDiagnostic("42501","permission denied for table rst_t");fixture.AssertDiagnostic(s.t,err,&d)
  s.exec("RESET ROLE");s.stats(0,22.5,0,0)
  s.exec("GRANT MAINTAIN ON rst_t TO rst_owner");s.exec("GRANT rst_owner TO rst_member");s.exec("SET ROLE rst_member");s.restore("'reltuples',23.5::real",true);s.exec("RESET ROLE")
  s.exec("ALTER TABLE rst_t OWNER TO rst_owner");s.exec("SET ROLE rst_member");s.clear();s.exec("RESET ROLE");s.stats(0,-1,0,0)
  s.exec("CREATE SCHEMA rst_private");s.exec("CREATE TABLE rst_private.hidden(a int)");s.exec("GRANT MAINTAIN ON rst_private.hidden TO rst_actor");s.exec("SET ROLE rst_actor")
  s.fail("SELECT pg_clear_relation_stats('rst_private','hidden')","42501","permission denied for schema rst_private");s.exec("RESET ROLE")
  var database,owner string
  if err=s.conn.QueryRowContext(s.p.Ctx,"SELECT datname,pg_get_userbyid(datdba) FROM pg_database WHERE datname=current_database()").Scan(&database,&owner);err!=nil{s.t.Fatal(err)}
  quote:=func(v string)string{return `"`+strings.ReplaceAll(v,`"`,`""`)+`"`}
  restoreOwner:="ALTER DATABASE "+quote(database)+" OWNER TO "+quote(owner)
  defer func(){if _,err:=s.conn.ExecContext(context.Background(),"RESET ROLE;"+restoreOwner);err!=nil{s.t.Error(err)}}()
  s.exec("ALTER DATABASE "+quote(database)+" OWNER TO rst_actor")
  s.exec("SET ROLE rst_actor");s.restore("'reltuples',24.5::real",true);s.clear();s.exec("RESET ROLE");s.exec(restoreOwner)
 })
}
