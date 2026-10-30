package reference_test

import (
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceRelationStatisticsTypedLifecycle(t *testing.T){withRelationStats(t,func(s relationStatsScenario){
 s.exec("CREATE TABLE rst_t(a int)")
 s.restore("'relpages',18,'reltuples',21.5::real,'relallvisible',24,'relallfrozen',27",true)
 q:="SELECT pg_restore_relation_stats('schemaname',$1::text,'relname',$2::text,'relpages',$3::integer,'reltuples',$4::real,'relallvisible',$5::integer,'relallfrozen',$6::integer)"
 stmt,err:=s.conn.PrepareContext(s.p.Ctx,q);if err!=nil{s.t.Fatal(err)};defer stmt.Close()
 s.stats(18,21.5,24,27)
 s.run(fixture.Select(relationStatsRestore("rst_t","'reltuples',99::real")+" LIMIT 0",[]string{"pg_restore_relation_stats"},[]string{"BOOL"},nil));s.stats(18,21.5,24,27)
 for _,n:=range []int64{31,32}{
  step:=fixture.Select(q,[]string{"pg_restore_relation_stats"},[]string{"BOOL"},[][]any{{true}},"public","rst_t",n,float64(n)+.5,n+1,n+2)
  if s.prepared{fixture.RunStep(s.t,s.p.Ctx,fixture.HeldStatement{Executor:s.conn,Statement:stmt,SQL:q},s.b.Recorder,step)}else{s.run(step)}
  s.stats(n,float64(n)+.5,n+1,n+2)
 }
 s.run(fixture.Select("SELECT * FROM pg_restore_relation_stats('schemaname','public','relname','rst_t','reltuples',40.5::real)",[]string{"pg_restore_relation_stats"},[]string{"BOOL"},[][]any{{true}}));s.stats(32,40.5,33,34)
 s.exec("CREATE VIEW rst_named_restore AS SELECT pg_restore_relation_stats(VARIADIC kwargs => '{schemaname,public,relname,rst_t}'::text[]) AS v")
 s.exec("CREATE VIEW rst_named_clear AS SELECT pg_clear_relation_stats(relname=>'rst_t',schemaname=>'public') IS NULL AS v")
 s.stats(32,40.5,33,34)
 s.run(fixture.Select("SELECT strpos(pg_get_viewdef('rst_named_restore'::regclass),'VARIADIC kwargs =>')>0 AS named",[]string{"named"},[]string{"BOOL"},[][]any{{true}}))
 s.run(fixture.Select("SELECT strpos(pg_get_viewdef('rst_named_clear'::regclass),'relname =>')>0 AS named",[]string{"named"},[]string{"BOOL"},[][]any{{true}}))
 s.run(fixture.Select("SELECT v FROM rst_named_restore",[]string{"v"},[]string{"BOOL"},[][]any{{true}}))
 s.run(fixture.Select("SELECT v FROM rst_named_clear",[]string{"v"},[]string{"BOOL"},[][]any{{false}}));s.stats(0,-1,0,0)
 // The target is resolved at execution, not captured by the prepared recipe.
 s.exec("DROP TABLE rst_t");s.exec("CREATE TABLE rst_t(a int)")
 var restored bool;if err=stmt.QueryRowContext(s.p.Ctx,"public","rst_t",41,42.5,43,44).Scan(&restored);err!=nil||!restored{s.t.Fatal(restored,err)}
 s.stats(41,42.5,43,44)
 s.exec("CREATE SEQUENCE rst_eval START 1")
 // REL_18_6 reorder_function_arguments lowers named arguments to formal order.
 // Schema consumes 1 and relation consumes 2: neither CASE returns NULL.
 s.run(fixture.Select("SELECT pg_clear_relation_stats(relname=>CASE WHEN nextval('rst_eval')=1 THEN NULL::text ELSE 'rst_t' END,schemaname=>CASE WHEN nextval('rst_eval')=2 THEN NULL::text ELSE 'public' END)",[]string{"pg_clear_relation_stats"},[]string{"VOID"},[][]any{{""}}))
 s.run(fixture.Select("SELECT last_value FROM rst_eval",[]string{"last_value"},[]string{"INT8"},[][]any{{int64(2)}}))
 s.stats(0,-1,0,0)
})}
