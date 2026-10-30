package reference_test

import (
 "context"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceRelationStatisticsTransactions(t *testing.T){withRelationStats(t,func(s relationStatsScenario){
 s.exec("CREATE TABLE rst_t(a int)");s.run(fixture.Exec("INSERT INTO rst_t VALUES(7),(8)",2))
 s.restore("'relpages',18,'reltuples',21.5::real,'relallvisible',24,'relallfrozen',27",true)
 peer,err:=s.db.Conn(s.p.Ctx);if err!=nil{s.t.Fatal(err)};defer peer.Close()
 defer s.conn.ExecContext(context.Background(),"ROLLBACK")
 observe:=func(want float64){s.t.Helper();var got float64;if err:=peer.QueryRowContext(s.p.Ctx,"SELECT reltuples FROM pg_class WHERE oid='rst_t'::regclass").Scan(&got);err!=nil||got!=want{s.t.Fatalf("peer tuples %v want %v: %v",got,want,err)}}
 s.exec("BEGIN");s.restore("'reltuples',22.5::real",true);s.stats(18,22.5,24,27);observe(21.5)
 s.restore("'relpages',19",true);s.stats(19,22.5,24,27)
 s.exec("SAVEPOINT rst_save");s.clear();s.stats(0,-1,0,0);s.exec("ROLLBACK TO rst_save");s.stats(19,22.5,24,27)
 s.exec("ROLLBACK");s.stats(18,21.5,24,27)
 s.exec("BEGIN");s.restore("'reltuples',23.5::real",true);s.exec("COMMIT");observe(23.5)
 q:="SELECT CASE WHEN pg_restore_relation_stats('schemaname','public','relname','rst_t','reltuples',99::real) THEN 1/(length(current_user)-length(current_user)) ELSE 0 END"
 s.fail(q,"22012","division by zero");s.stats(18,23.5,24,27)
 for _,completion:=range []string{"ROLLBACK","COMMIT"}{
  gid:="rst_"+completion+"_"+s.b.Name
  fixture.PreparedReferenceCleanup(s.t,s.b,gid)
  s.exec("BEGIN");s.restore("'reltuples',24.5::real",true);s.exec("PREPARE TRANSACTION '"+gid+"'");observe(23.5)
  fixture.RunStep(s.t,s.p.Ctx,peer,s.b.Recorder,fixture.Exec(completion+" PREPARED '"+gid+"'",0))
  if completion=="COMMIT"{observe(24.5)}else{observe(23.5)}
 }
 s.run(fixture.Exec("INSERT INTO rst_t VALUES(9)",1));s.stats(18,24.5,24,27)
 s.exec("ANALYZE rst_t")
 s.run(fixture.Select("SELECT reltuples FROM pg_class WHERE oid='rst_t'::regclass",[]string{"reltuples"},[]string{"FLOAT4"},[][]any{{float64(3)}}))
 s.restore("'reltuples',25.5::real",true)
 relationStatsBlockedCancellation(s)
 relationStatsDisjointMerge(s)
 s.exec("BEGIN");s.exec("CREATE TABLE rst_new(a int) PARTITION BY RANGE(a)")
 s.run(fixture.Select(relationStatsRestore("rst_new","'reltuples',11.5::real"),[]string{"pg_restore_relation_stats"},[]string{"BOOL"},[][]any{{true}}));s.exec("ROLLBACK")
 s.exec("BEGIN");s.exec("CREATE TABLE rst_new(a int) PARTITION BY RANGE(a)")
 s.run(fixture.Select(relationStatsRestore("rst_new","'reltuples',12.5::real"),[]string{"pg_restore_relation_stats"},[]string{"BOOL"},[][]any{{true}}));s.exec("COMMIT")
 s.run(fixture.Select("SELECT reltuples FROM pg_class WHERE oid='rst_new'::regclass",[]string{"reltuples"},[]string{"FLOAT4"},[][]any{{float64(12.5)}}))
 s.exec("ALTER TABLE rst_t RENAME TO rst_renamed")
 s.run(fixture.Select("SELECT reltuples FROM pg_class WHERE oid='rst_renamed'::regclass",[]string{"reltuples"},[]string{"FLOAT4"},[][]any{{float64(42.5)}}))
})}
