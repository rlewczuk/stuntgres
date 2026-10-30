package reference_test

import (
 "bytes"
 "context"
 "errors"
 "strings"
 "testing"
 "stuntgres"
 "stuntgres/reference/fixture"
)

func TestReferenceDatabaseFilesLifecycle(t *testing.T){
 p:=fixture.NewScenario(t)
 for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){
  for _,q:=range []string{
   "CREATE TABLE df_life(i INT)","CREATE INDEX df_life_index ON df_life(i)","CREATE SEQUENCE df_life_sequence",
   "CREATE MATERIALIZED VIEW df_life_mv AS SELECT i FROM df_life WITH NO DATA",
   "CREATE VIEW df_life_view AS SELECT i FROM df_life",
   "ALTER FUNCTION pg_relation_filepath(regclass) COST 7 PARALLEL RESTRICTED",
   // Stored calls must capture the configured declaration, not a stale pre-ALTER carrier.
   "CREATE VIEW df_stored_calls AS SELECT pg_relation_filenode('df_life_view') AS node,pg_relation_filepath('df_life_view') AS path,pg_filenode_relation(0::oid,0::oid) AS relation",
   "CREATE FUNCTION df_location() RETURNS oid LANGUAGE SQL STABLE RETURN pg_relation_filenode('df_life_view')",
   "COMMENT ON TABLE df_life IS 'logical location boundary'",
   "REVOKE EXECUTE ON FUNCTION pg_relation_filepath(regclass) FROM PUBLIC",
  }{databaseFilesRun(t,p,b,fixture.Exec(q,0))}
  defer func(){if _,err:=b.DB.ExecContext(p.Ctx,"GRANT EXECUTE ON FUNCTION pg_relation_filepath(regclass) TO PUBLIC; ALTER FUNCTION pg_relation_filepath(regclass) COST 1 PARALLEL SAFE");err!=nil{t.Error(err)}}()
  tableColumns:=[][]any{{int64(1),"i",int64(23),int64(-1),false,false,false}}
  check:=func(){
   // Fixed SQL-visible facts, not a snapshot learned from the pre-Restore catalog.
   // Include every scenario-created relation and reject extra lifecycle relations.
   databaseFilesRun(t,p,b,fixture.Select("SELECT relname,relkind,relpersistence FROM pg_class WHERE relnamespace='public'::regnamespace AND (relname LIKE 'df_life%' OR relname='df_stored_calls') ORDER BY relname",[]string{"relname","relkind","relpersistence"},[]string{"NAME","CHAR","CHAR"},[][]any{
    {"df_life","r","p"},{"df_life_index","i","p"},{"df_life_mv","m","p"},{"df_life_sequence","S","p"},{"df_life_view","v","p"},{"df_stored_calls","v","p"},
   }))
   databaseFilesRun(t,p,b,fixture.Select("SELECT indrelid='df_life'::regclass AS parent,indisunique,indisprimary,indisvalid,indisready,pg_get_indexdef(indexrelid) AS definition FROM pg_index WHERE indexrelid='df_life_index'::regclass",[]string{"parent","indisunique","indisprimary","indisvalid","indisready","definition"},[]string{"BOOL","BOOL","BOOL","BOOL","BOOL","TEXT"},[][]any{{true,false,false,true,true,"CREATE INDEX df_life_index ON public.df_life USING btree (i)"}}))
   databaseFilesRun(t,p,b,fixture.Select("SELECT seqtypid::bigint AS seqtypid,seqstart,seqincrement,seqmax,seqmin,seqcache,seqcycle FROM pg_sequence WHERE seqrelid='df_life_sequence'::regclass",[]string{"seqtypid","seqstart","seqincrement","seqmax","seqmin","seqcache","seqcycle"},[]string{"INT8","INT8","INT8","INT8","INT8","INT8","BOOL"},[][]any{{int64(20),int64(1),int64(1),int64(9223372036854775807),int64(1),int64(1),false}}))
   databaseFilesRun(t,p,b,fixture.Select("SELECT pg_get_viewdef('df_life_mv'::regclass,true) AS definition",[]string{"definition"},[]string{"TEXT"},[][]any{{" SELECT i\n   FROM df_life;"}}))
   fixture.MaterializedViewPopulation(func(s fixture.Step){databaseFilesRun(t,p,b,s)},"df_life_mv",false)
   databaseFilesRun(t,p,b,fixture.MaterializedViewUnpopulated("df_life_mv"))
   databaseFilesRun(t,p,b,fixture.Select("SELECT attnum,attname,atttypid::bigint AS atttypid,atttypmod,attnotnull,atthasdef,attisdropped FROM pg_attribute WHERE attrelid='df_life'::regclass AND attnum>0 ORDER BY attnum",[]string{"attnum","attname","atttypid","atttypmod","attnotnull","atthasdef","attisdropped"},[]string{"INT2","NAME","INT8","INT4","BOOL","BOOL","BOOL"},tableColumns))
   databaseFilesRun(t,p,b,fixture.Select("SELECT node,path,relation FROM df_stored_calls",[]string{"node","path","relation"},[]string{"OID","TEXT","REGCLASS"},[][]any{{nil,nil,nil}}))
   databaseFilesRun(t,p,b,databaseFilesNull("SELECT df_location() AS v","v","OID"))
   // Owners are independent bootstrap facts, not values learned from either backend.
   acl:="{postgres=X/postgres}"
   if b.PG{acl="{stuntgres=X/stuntgres}"}
   databaseFilesRun(t,p,b,fixture.Select("SELECT procost,proparallel,proacl::text AS acl FROM pg_proc WHERE oid=3034",[]string{"procost","proparallel","acl"},[]string{"FLOAT4","CHAR","TEXT"},[][]any{{float64(7),"r",acl}}))
   // Routine comments are not supported native syntax; use the existing table-comment seam.
   databaseFilesRun(t,p,b,fixture.Select("SELECT obj_description('df_life'::regclass,'pg_class') AS comment",[]string{"comment"},[]string{"TEXT"},[][]any{{"logical location boundary"}}))
  }
  check()
  databaseFilesRun(t,p,b,fixture.Exec("ALTER TABLE df_life ADD COLUMN s TEXT",0))
  tableColumns=append(tableColumns,[]any{int64(2),"s",int64(25),int64(-1),false,false,false})
  check()
  if b.PG{return}
  q:="SELECT pg_relation_filenode('df_life_view') AS v"
  literal,err:=b.DB.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer literal.Close()
  parameterSQL:="SELECT pg_relation_filenode($1) AS v"
  parameter,err:=b.DB.PrepareContext(p.Ctx,parameterSQL);if err!=nil{t.Fatal(err)};defer parameter.Close()
  retained,err:=parameter.QueryContext(p.Ctx,"df_life_view");if err!=nil{t.Fatal(err)};defer retained.Close()
  var dump bytes.Buffer
  if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil{t.Fatal(err)}
  for range 2 {
   if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
   if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil{t.Fatal(err)}
   check()
   databaseFilesStored(t,p,b,"df_life",false)
   fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:b.DB,Statement:parameter,SQL:parameterSQL},b.Recorder,databaseFilesNull(parameterSQL,"v","OID","df_life_view"))
   var next bytes.Buffer;if err:=stuntgres.Dump(p.Ctx,p.Native,&next);err!=nil{t.Fatal(err)};dump=next
  }
  types,err:=retained.ColumnTypes();if err!=nil||len(types)!=1||types[0].DatabaseTypeName()!="OID"{t.Fatal(types,err)}
  if !retained.Next(){t.Fatal(retained.Err())};var old any;if err:=retained.Scan(&old);err!=nil||old!=nil{t.Fatal(old,err)};retained.Close()
  rows,err:=literal.QueryContext(p.Ctx);if rows!=nil{rows.Close();t.Fatal("stale literal exposed rows")};if err==nil||err.Error()!="stuntgres: catalog replaced before publication"{t.Fatal(err)}
  var state interface{SQLState()string};if errors.As(err,&state){t.Fatal("ordinary fence acquired SQLSTATE")}
  for _,bad:=range []string{"CREATE TABLE","CREATE TABLE df_failed(i INT); INSERT INTO df_absent VALUES(1)"}{if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(bad));err==nil{t.Fatal("invalid restore accepted")};check()}
  ctx,cancel:=context.WithCancel(p.Ctx);cancel()
  if err:=stuntgres.Restore(ctx,p.Native,bytes.NewReader(dump.Bytes()));!errors.Is(err,context.Canceled){t.Fatal(err)}
  for _,prepared:=range []bool{false,true}{
   if prepared{rows,err=parameter.QueryContext(ctx,"df_life_view")}else{rows,err=b.DB.QueryContext(ctx,parameterSQL,"df_life_view")}
   if rows!=nil{rows.Close();t.Fatal("canceled invocation exposed rows")};if !errors.Is(err,context.Canceled){t.Fatal(err)}
  };check()
 })}
}

func TestReferenceDatabaseFilesSmoke(t *testing.T){
 p:=fixture.NewScenario(t)
 for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){
  databaseFilesRun(t,p,b,fixture.Exec("CREATE TABLE df_smoke(i INT)",0))
  databaseFilesRun(t,p,b,fixture.Exec("INSERT INTO df_smoke VALUES(7)",1))
  databaseFilesRun(t,p,b,fixture.Exec("CREATE VIEW df_smoke_view AS SELECT pg_relation_filenode(NULL::regclass) AS n,pg_relation_filepath(NULL::regclass) AS p",0))
  for _,prepared:=range []bool{false,true}{
   s:=fixture.Select("SELECT n,p FROM df_smoke_view",[]string{"n","p"},[]string{"OID","TEXT"},[][]any{{nil,nil}});s.Prepared=prepared;databaseFilesRun(t,p,b,s)
   s=databaseFilesNull("SELECT pg_relation_filenode('df_smoke_view')","pg_relation_filenode","OID");s.Prepared=prepared;databaseFilesRun(t,p,b,s)
  }
  databaseFilesStored(t,p,b,"df_smoke",false)
  databaseFilesRun(t,p,b,databaseFilesNull("SELECT pg_filenode_relation(0::oid,0::oid)","pg_filenode_relation","REGCLASS"))
  for _,tc:=range []struct{q,code,message,hint string;pos int}{
   {"SELECT pg_relation_filenode('database_files_missing')","42P01",`relation "database_files_missing" does not exist`,"",29},
   {"SELECT pg_relation_filepath(true)","42883","function pg_relation_filepath(boolean) does not exist","No function matches the given name and argument types. You might need to add explicit type casts.",8},
  }{s:=fixture.ImplicitError(tc.q,fixture.Diagnostic{Code:tc.code,Message:tc.message,Hint:tc.hint,Position:fixture.Position(tc.pos)});s.SelectRows=true;s.ErrorStage="execute";databaseFilesRun(t,p,b,s)}
  databaseFilesRun(t,p,b,fixture.Select("SELECT i FROM df_smoke",[]string{"i"},[]string{"INT4"},[][]any{{int64(7)}}))
  if !b.PG{
   var dump bytes.Buffer;if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil{t.Fatal(err)}
   if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)};if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil{t.Fatal(err)}
   databaseFilesRun(t,p,b,fixture.Select("SELECT n,p FROM df_smoke_view",[]string{"n","p"},[]string{"OID","TEXT"},[][]any{{nil,nil}}));databaseFilesStored(t,p,b,"df_smoke",false)
  }
 })}
}
