package reference_test

import (
 "testing"
 "stuntgres/reference/fixture"
)

// Forward witnesses deliberately differ: PG has physical mappings, native does not.
func databaseFilesStored(t *testing.T,p *fixture.Pair,b fixture.Backend,name string,temporary bool){
 t.Helper()
 for _,f:=range databaseFilesFunctions[:2]{for _,prepared:=range []bool{false,true}{
  q:="SELECT "+f.name+"('"+name+"') AS v"
  if !b.PG{s:=databaseFilesBoundary(q,f.name);s.Prepared=prepared;databaseFilesRun(t,p,b,s)}else{
   predicate:=f.name+"('"+name+"') IS NOT NULL"
   if f.oid==2999{predicate="pg_relation_filenode('"+name+"')>0"}
   s:=fixture.Select("SELECT "+predicate+" AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}});s.Prepared=prepared;databaseFilesRun(t,p,b,s)
  }
  databaseFilesRun(t,p,b,databaseFilesNull("SELECT pg_relation_filepath(NULL)","pg_relation_filepath","TEXT"))
 }}
 if b.PG {
  reverse:="pg_filenode_relation(0::oid,pg_relation_filenode('"+name+"'))='"+name+"'::regclass"
  if temporary {reverse="pg_filenode_relation(0::oid,pg_relation_filenode('"+name+"')) IS NULL"}
  databaseFilesRun(t,p,b,fixture.Select("SELECT "+reverse+" AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}}))
  if !temporary&&name!="pg_class"{
   databaseFilesRun(t,p,b,fixture.Select("SELECT pg_relation_filenode('"+name+"')=relfilenode AS v FROM pg_class WHERE oid='"+name+"'::regclass",[]string{"v"},[]string{"BOOL"},[][]any{{true}}))
   databaseFilesRun(t,p,b,fixture.Select("SELECT pg_filenode_relation(1663::oid,pg_relation_filenode('"+name+"'))='"+name+"'::regclass AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}}))
  }
 } else {databaseFilesRun(t,p,b,databaseFilesNull("SELECT pg_filenode_relation(0::oid,('"+name+"'::regclass)::oid)","pg_filenode_relation","REGCLASS"))}
}
func TestReferenceDatabaseFilesRelations(t *testing.T){
 p:=fixture.NewScenario(t)
 for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){
  for _,q:=range []string{
   "CREATE TABLE df_table(i INT)","INSERT INTO df_table VALUES(7)","CREATE INDEX df_index ON df_table(i)",
   "CREATE VIEW df_view AS SELECT i FROM df_table","CREATE SEQUENCE df_sequence",
   "CREATE MATERIALIZED VIEW df_mv AS SELECT i FROM df_table WITH NO DATA",
   "CREATE TABLE df_partition(i INT) PARTITION BY RANGE(i)",
   "CREATE TYPE df_composite AS (i INT)","CREATE UNLOGGED TABLE df_unlogged(i INT)",
  } {n:=int64(0);if q=="INSERT INTO df_table VALUES(7)"{n=1};databaseFilesRun(t,p,b,fixture.Exec(q,n))}
  storageLess:=[]string{"df_view","df_partition","df_composite"}
  // Native partitioned-index DDL is unsupported; U2 covers its descriptor class.
  // Keep the public PostgreSQL witness as well as shared public partition-root coverage.
  if b.PG{
   databaseFilesRun(t,p,b,fixture.Exec("CREATE INDEX df_partition_index ON df_partition(i)",0))
   storageLess=append(storageLess,"df_partition_index")
  }
  for _,name:=range storageLess{for _,f:=range databaseFilesFunctions[:2]{
   for _,prepared:=range []bool{false,true}{s:=databaseFilesNull("SELECT "+f.name+"('"+name+"')",f.name,f.result);s.Prepared=prepared;databaseFilesRun(t,p,b,s)}
  }}
  databaseFilesRun(t,p,b,fixture.Select("SELECT EXISTS(SELECT 1 FROM pg_class WHERE oid=4294967295::oid) AS v",[]string{"v"},[]string{"BOOL"},[][]any{{false}}))
  for _,f:=range databaseFilesFunctions[:2]{databaseFilesRun(t,p,b,databaseFilesNull("SELECT "+f.name+"(4294967295::regclass)",f.name,f.result))}
  for _,name:=range []string{"df_table","df_index","df_sequence","df_mv","df_unlogged","pg_class"}{
   databaseFilesStored(t,p,b,name,false)
   databaseFilesRun(t,p,b,fixture.Select("SELECT i FROM df_table",[]string{"i"},[]string{"INT4"},[][]any{{int64(7)}}))
  }
  if b.PG{
   databaseFilesRun(t,p,b,fixture.Select("SELECT relfilenode=0 AND pg_relation_filenode(oid)>0 AS v FROM pg_class WHERE oid='pg_class'::regclass",[]string{"v"},[]string{"BOOL"},[][]any{{true}}))
   databaseFilesRun(t,p,b,fixture.Select("SELECT pg_relation_filepath('df_table')='base/'||(SELECT oid::text FROM pg_database WHERE datname=current_database())||'/'||pg_relation_filenode('df_table')::text AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}}))
  }
  // Borrow one connection for TEMP provenance, without changing the shared fixture.
  conn,err:=b.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)}
  fixture.RunStep(t,p.Ctx,conn,b.Recorder,fixture.Exec("CREATE TEMP TABLE df_temp(i INT)",0))
  for _,f:=range databaseFilesFunctions[:2]{q:="SELECT "+f.name+"('df_temp') AS v";s:=databaseFilesBoundary(q,f.name);if b.PG{s=fixture.Select("SELECT "+f.name+"('df_temp') IS NOT NULL AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}})};fixture.RunStep(t,p.Ctx,conn,b.Recorder,s)}
  if b.PG{fixture.RunStep(t,p.Ctx,conn,b.Recorder,databaseFilesNull("SELECT pg_filenode_relation(0::oid,pg_relation_filenode('df_temp'))","pg_filenode_relation","REGCLASS"))}
  fixture.RunStep(t,p.Ctx,conn,b.Recorder,fixture.Exec("DROP TABLE df_temp",0));conn.Close()
  tx,err:=b.DB.BeginTx(p.Ctx,nil);if err!=nil{t.Fatal(err)};defer tx.Rollback()
  fixture.RunStep(t,p.Ctx,tx,b.Recorder,fixture.Exec("CREATE TABLE df_private(i INT)",0))
  s:=databaseFilesBoundary("SELECT pg_relation_filenode('df_private')","pg_relation_filenode")
  if b.PG{s=fixture.Select("SELECT pg_relation_filenode('df_private')>0 AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}})}
  fixture.RunStep(t,p.Ctx,tx,b.Recorder,s);if err:=tx.Rollback();err!=nil{t.Fatal(err)}
  databaseFilesRun(t,p,b,fixture.Select("SELECT NOT EXISTS(SELECT 1 FROM pg_class WHERE relname='df_private' AND relnamespace='public'::regnamespace) AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}}))
 })}
}
