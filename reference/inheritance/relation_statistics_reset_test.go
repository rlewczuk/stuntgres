package reference_test

import (
 "context"
 "fmt"
 "testing"
 "stuntgres/reference/fixture"
)

// Relcache RelationSetNewRelfilenumber resets the four catalog fields. ALTER
// TYPE's new heap carries the same defaults through finish_heap_swap; unlike
// CLUSTER it does not first collect new page/tuple estimates.
func TestReferenceRelationStatisticsResetOrdering(t *testing.T){withRelationStats(t,func(s relationStatsScenario){
 s.exec("CREATE TABLE rst_t(a integer)")
 s.run(fixture.Exec("INSERT INTO rst_t VALUES(7),(8)",2))
 s.exec("ANALYZE rst_t")
 peer,err:=s.db.Conn(s.p.Ctx);if err!=nil{s.t.Fatal(err)};defer peer.Close()
 defer s.conn.ExecContext(context.Background(),"ROLLBACK")
 observe:=func(pages int64,tuples float64,visible,frozen int64){s.t.Helper();fixture.RunStep(s.t,s.p.Ctx,peer,s.b.Recorder,fixture.Select(relationStatsRead("rst_t"),[]string{"relpages","reltuples","relallvisible","relallfrozen"},[]string{"INT4","FLOAT4","INT4","INT4"},[][]any{{pages,tuples,visible,frozen}}))}
 seed:=func(){s.restore("'relpages',18,'reltuples',21.5::real,'relallvisible',24,'relallfrozen',27",true)}
 seed()
 s.exec("BEGIN");s.exec("TRUNCATE rst_t");s.stats(0,-1,0,0);observe(18,21.5,24,27)
 s.exec("COMMIT");s.stats(0,-1,0,0)
 s.run(fixture.Select("SELECT count(*) FROM rst_t",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
 s.exec("BEGIN");s.exec("TRUNCATE rst_t");s.restore("'reltuples',42.5::real",true);s.stats(0,42.5,0,0)
 s.exec("COMMIT");s.stats(0,42.5,0,0)
 s.exec("BEGIN");s.exec("TRUNCATE rst_t");s.restore("'reltuples',43.5::real",true);s.clear();s.stats(0,-1,0,0)
 s.exec("COMMIT");s.stats(0,-1,0,0)
 s.exec("BEGIN");seed();s.exec("TRUNCATE rst_t");s.stats(0,-1,0,0)
 // No second file allocation in the same subtransaction: keep later imports.
 s.restore("'reltuples',44.5::real",true);s.exec("TRUNCATE rst_t");s.stats(0,44.5,0,0);s.exec("COMMIT");s.stats(0,44.5,0,0)
 s.exec("BEGIN");s.exec("TRUNCATE rst_t");s.restore("'reltuples',44.5::real",true)
 s.exec("SAVEPOINT rst_nested")
 s.exec("TRUNCATE rst_t");s.stats(0,-1,0,0)
 s.restore("'reltuples',45.5::real",true);s.exec("TRUNCATE rst_t");s.stats(0,45.5,0,0)
 s.exec("ROLLBACK TO rst_nested");s.stats(0,44.5,0,0)
 // ROLLBACK TO restarts the child: its first allocation resets again.
 s.exec("TRUNCATE rst_t");s.stats(0,-1,0,0);s.restore("'reltuples',46.5::real",true)
 s.exec("RELEASE SAVEPOINT rst_nested")
 // The released child's new file now belongs to its parent transaction.
 s.exec("TRUNCATE rst_t");s.stats(0,46.5,0,0);s.exec("COMMIT");s.stats(0,46.5,0,0)
 for _,nested:=range []bool{false,true}{
  s.exec("BEGIN");if nested{s.exec("SAVEPOINT rst_create_nested")}
  s.exec("CREATE TABLE rst_created(a integer)")
  s.run(fixture.Select(relationStatsRestore("rst_created","'relpages',5,'reltuples',6.5::real,'relallvisible',7,'relallfrozen',8"),[]string{"pg_restore_relation_stats"},[]string{"BOOL"},[][]any{{true}}))
  s.run(fixture.Exec("INSERT INTO rst_created VALUES(7),(8)",2))
  s.exec("TRUNCATE rst_created")
  retained:=fixture.Select(relationStatsRead("rst_created"),[]string{"relpages","reltuples","relallvisible","relallfrozen"},[]string{"INT4","FLOAT4","INT4","INT4"},[][]any{{int64(5),float64(6.5),int64(7),int64(8)}})
  s.run(retained);s.run(fixture.Select("SELECT count(*) FROM rst_created",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
  if !nested {
   s.exec("SAVEPOINT rst_created_child");s.exec("TRUNCATE rst_created")
   s.run(fixture.Select(relationStatsRead("rst_created"),[]string{"relpages","reltuples","relallvisible","relallfrozen"},[]string{"INT4","FLOAT4","INT4","INT4"},[][]any{{int64(0),float64(-1),int64(0),int64(0)}}))
   s.run(fixture.Select(relationStatsRestore("rst_created","'relpages',5,'reltuples',6.5::real,'relallvisible',7,'relallfrozen',8"),[]string{"pg_restore_relation_stats"},[]string{"BOOL"},[][]any{{true}}))
   s.exec("RELEASE SAVEPOINT rst_created_child")
  }
  if nested{s.exec("RELEASE SAVEPOINT rst_create_nested")}
  s.exec("COMMIT");s.run(retained);s.exec("DROP TABLE rst_created")
 }
 seed();s.exec("BEGIN");s.exec("SAVEPOINT rst_reset_save");s.exec("TRUNCATE rst_t");s.restore("'reltuples',45.5::real",true);s.stats(0,45.5,0,0)
 s.exec("ROLLBACK TO rst_reset_save");s.stats(18,21.5,24,27);s.exec("COMMIT");s.stats(18,21.5,24,27)
 for _,completion:=range []string{"ROLLBACK","COMMIT"}{
  seed();gid:=fmt.Sprintf("rst_reset_%s_%s_%t",completion,s.b.Name,s.prepared)
  fixture.PreparedReferenceCleanup(s.t,s.b,gid)
  s.exec("BEGIN");s.exec("TRUNCATE rst_t");s.restore("'reltuples',46.5::real",true);s.stats(0,46.5,0,0)
  s.exec("PREPARE TRANSACTION '"+gid+"'");observe(18,21.5,24,27)
  fixture.RunStep(s.t,s.p.Ctx,peer,s.b.Recorder,fixture.Exec(completion+" PREPARED '"+gid+"'",0))
  if completion=="COMMIT"{s.stats(0,46.5,0,0)}else{s.stats(18,21.5,24,27)}
 }
 // Both orders around genuine ordinary ALTER TYPE heap rewrites, including
 // savepoint and prepared ownership, must retain the latest statistics update.
 s.run(fixture.Exec("INSERT INTO rst_t VALUES(7),(8)",2))
 seed();s.exec("BEGIN");s.exec("ALTER TABLE rst_t ALTER COLUMN a TYPE bigint USING a::bigint");s.stats(0,-1,0,0)
 s.restore("'reltuples',47.5::real",true);s.stats(0,47.5,0,0);s.exec("COMMIT");s.stats(0,47.5,0,0)
 s.exec("BEGIN");seed();s.exec("ALTER TABLE rst_t ALTER COLUMN a TYPE integer USING a::integer");s.stats(0,-1,0,0)
 s.clear();s.exec("COMMIT");s.stats(0,-1,0,0)
 seed();s.exec("BEGIN");s.exec("SAVEPOINT rst_rewrite_save");s.exec("ALTER TABLE rst_t ALTER COLUMN a TYPE bigint USING a::bigint")
 s.restore("'reltuples',48.5::real",true);s.exec("ROLLBACK TO rst_rewrite_save");s.stats(18,21.5,24,27);s.exec("COMMIT");s.stats(18,21.5,24,27)
 for _,completion:=range []string{"ROLLBACK","COMMIT"}{
  seed();gid:=fmt.Sprintf("rst_rewrite_%s_%s_%t",completion,s.b.Name,s.prepared)
  fixture.PreparedReferenceCleanup(s.t,s.b,gid)
  s.exec("BEGIN");s.exec("ALTER TABLE rst_t ALTER COLUMN a TYPE bigint USING a::bigint");s.restore("'reltuples',49.5::real",true)
  s.exec("PREPARE TRANSACTION '"+gid+"'");observe(18,21.5,24,27)
  fixture.RunStep(s.t,s.p.Ctx,peer,s.b.Recorder,fixture.Exec(completion+" PREPARED '"+gid+"'",0))
  if completion=="COMMIT"{s.stats(0,49.5,0,0)}else{s.stats(18,21.5,24,27)}
 }
 s.exec("BEGIN");s.exec("SAVEPOINT rst_rewrite_truncate")
 s.exec("ALTER TABLE rst_t ALTER COLUMN a TYPE integer USING a::integer")
 s.restore("'reltuples',50.5::real",true);s.exec("TRUNCATE rst_t");s.stats(0,50.5,0,0)
 s.exec("SAVEPOINT rst_after_rewrite")
 s.exec("TRUNCATE rst_t");s.stats(0,-1,0,0)
 s.exec("ROLLBACK TO rst_after_rewrite");s.stats(0,50.5,0,0)
 s.exec("ROLLBACK TO rst_rewrite_truncate");s.stats(0,49.5,0,0);s.exec("COMMIT")
 // The inner exception block enters a real subtransaction after its enclosing
 // statement has already allocated a heap and restored an estimate.
 s.exec(`CREATE FUNCTION rst_internal_reset() RETURNS void LANGUAGE plpgsql AS $$
 BEGIN
  TRUNCATE rst_t;
  PERFORM pg_restore_relation_stats('schemaname','public','relname','rst_t','reltuples',51.5::real);
  BEGIN
   TRUNCATE rst_t;
  EXCEPTION WHEN OTHERS THEN
   RAISE;
  END;
 END
 $$`)
 s.exec("BEGIN")
 s.run(fixture.Select("SELECT rst_internal_reset()",[]string{"rst_internal_reset"},[]string{"VOID"},[][]any{{""}}))
 s.stats(0,-1,0,0);s.exec("ROLLBACK");s.stats(0,49.5,0,0)
 s.run(fixture.Select("SELECT a FROM rst_t ORDER BY a",[]string{"a"},[]string{"INT8"},[][]any{{int64(7)},{int64(8)}}))
 relationStatsMaintenanceReset(s)
})}

func relationStatsMaintenanceReset(s relationStatsScenario){
 s.t.Helper()
 s.exec("CREATE TABLE rst_maintenance(a integer)")
 s.run(fixture.Exec("INSERT INTO rst_maintenance VALUES(1),(1),(2)",3))
 s.exec("ANALYZE rst_maintenance")
 restore:=func(extra string){s.t.Helper();s.run(fixture.Select(relationStatsRestore("rst_maintenance",extra),[]string{"pg_restore_relation_stats"},[]string{"BOOL"},[][]any{{true}}))}
 tuples:=func(want float64){s.t.Helper();s.run(fixture.Select("SELECT reltuples FROM pg_class WHERE oid='rst_maintenance'::regclass",[]string{"reltuples"},[]string{"FLOAT4"},[][]any{{want}}))}
 restore("'relpages',18,'reltuples',21.5::real,'relallvisible',24,'relallfrozen',27")
 s.exec("BEGIN");s.exec("TRUNCATE rst_maintenance")
 s.run(fixture.Exec("INSERT INTO rst_maintenance VALUES(7),(8)",2))
 s.exec("SAVEPOINT rst_before_maintenance")
 s.exec("ANALYZE rst_maintenance")
 s.exec("ROLLBACK TO rst_before_maintenance")
 // Successful ANALYZE cardinality is nontransactional even when its selected
 // column facts roll back. The saved pre-ANALYZE -1 import cannot hide it.
 tuples(2)
 restore("'relpages',17")
 s.run(fixture.Select(relationStatsRead("rst_maintenance"),[]string{"relpages","reltuples","relallvisible","relallfrozen"},[]string{"INT4","FLOAT4","INT4","INT4"},[][]any{{int64(17),float64(2),int64(0),int64(0)}}))
 s.exec("COMMIT");tuples(2)
 restore("'reltuples',42.5::real");tuples(42.5)
 s.exec("ANALYZE rst_maintenance");tuples(2)
 s.run(fixture.Select("SELECT a FROM rst_maintenance ORDER BY a",[]string{"a"},[]string{"INT4"},[][]any{{int64(7)},{int64(8)}}))
}
