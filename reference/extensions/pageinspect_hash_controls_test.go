package reference_test

import (
 "context"
 "database/sql"
 "errors"
 "reflect"
 "strings"
 "testing"
 "stuntgres"
 "stuntgres/reference/fixture"
)
var hashViews=[]string{"hash_type_view","hash_stats_view","hash_items_view","hash_meta_view"}
var hashMoved=[]string{"kind","stats","items","meta"}
func hashViewStep(i int,rows [][]any)fixture.Step{q:="SELECT * FROM "+hashViews[i];if i==2{q+=" ORDER BY itemoffset"};return fixture.Select(q,fixture.HashNames[i],fixture.HashTypes[i],rows)}
func hashControls(t *testing.T,smoke bool){
 fixture.RoleReferenceCleanup(t,[]string{"hash_owner","hash_reader"})
 hashCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
  for _,q:=range []string{"CREATE ROLE hash_owner SUPERUSER","CREATE SCHEMA hash_moved","CREATE TABLE hash_images(bucket bytea,meta bytea)"}{run(fixture.Exec(q,0))}
  run(fixture.Exec("INSERT INTO hash_images VALUES($1,$2)",1,fixture.HashBucket(),fixture.HashMeta()))
  for _,q:=range []string{
   "CREATE VIEW hash_type_view AS SELECT hash_part.hash_page_type(bucket) AS hash_page_type FROM hash_images",
   "CREATE VIEW hash_stats_view AS SELECT (hash_part.hash_page_stats(bucket)).* FROM hash_images",
   "CREATE VIEW hash_items_view AS SELECT i.* FROM hash_images CROSS JOIN LATERAL hash_part.hash_page_items(bucket) AS i",
   "CREATE VIEW hash_meta_view AS SELECT (hash_part.hash_metapage_info(meta)).* FROM hash_images",
  }{run(fixture.Exec(q,0))}
  held:=make([]fixture.HeldStatement,4)
  for i:=range fixture.HashMembers{
   q:=hashViewStep(i,nil).Query;st,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer st.Close();held[i]=fixture.HeldStatement{Executor:c,Statement:st,SQL:q}
   raw,err:=c.PrepareContext(p.Ctx,hashQuery(i));if err!=nil{t.Fatal(err)};rh:=fixture.HeldStatement{Executor:c,Statement:raw,SQL:hashQuery(i)}
   changed,want:=hashChanged(i)
   for _,s:=range []fixture.Step{hashStep(i,fixture.HashOrdinary(i),fixture.HashRows(i)),hashStep(i,changed,want),hashStep(i,nil,fixture.HashNullRows(i)),hashStep(i,fixture.HashOrdinary(i),fixture.HashRows(i))}{hashBoth(t,p,b,c,s);fixture.RunStep(t,p.Ctx,rh,b.Recorder,fixture.HashMetadata(s,b.PG,false))}
   if i==0{newPage:=hashStep(0,make([]byte,8192),[][]any{{"unused"}});hashBoth(t,p,b,c,newPage);fixture.RunStep(t,p.Ctx,rh,b.Recorder,newPage)}else{
    message:="page is not a hash bucket or overflow page";if i==3{message="page is not a hash meta page"}
    bad:=hashError(i,make([]byte,8192),"22023",message,"");fixture.BRINBoth(t,p,b,c,bad);fixture.RunStep(t,p.Ctx,rh,b.Recorder,bad)
   }
   if i==2{empty:=hashStep(2,fixture.HashPage(2),nil);hashBoth(t,p,b,c,empty);fixture.RunStep(t,p.Ctx,rh,b.Recorder,empty)}
   bad:=hashError(i,[]byte{1,2,3},"22023","invalid page size","Expected 8192 bytes, got 3.");fixture.RunStep(t,p.Ctx,rh,b.Recorder,bad);fixture.RunStep(t,p.Ctx,rh,b.Recorder,fixture.HashMetadata(hashStep(i,fixture.HashOrdinary(i),fixture.HashRows(i)),b.PG,false));raw.Close()
  }
  if smoke{hashAuthority(t,p,b,c)}else{run(fixture.Exec("CREATE ROLE hash_reader",0))}
  for _,q:=range []string{"GRANT USAGE ON SCHEMA public,hash_part,hash_moved TO hash_reader","GRANT SELECT ON hash_type_view,hash_stats_view,hash_items_view,hash_meta_view TO hash_reader"}{run(fixture.Exec(q,0))}
  check:=func(target *sql.Conn,backend fixture.Backend,moved bool){
   principal:="postgres";if backend.PG{principal="stuntgres"}
   for i,name:=range fixture.HashMembers{
    schema,owner:="hash_part",principal;if moved{name,schema,owner=hashMoved[i],"hash_moved","hash_owner"}
    fixture.BRINBoth(t,p,backend,target,fixture.HashDeclaration(i,name,schema,owner))
    fixture.BRINBoth(t,p,backend,target,fixture.HashMetadata(hashViewStep(i,fixture.HashRows(i)),backend.PG,true))
    s:=hashStep(i,fixture.HashOrdinary(i),fixture.HashRows(i));s.Query="SELECT * FROM "+schema+"."+name+"($1)";if i==0{s.Query="SELECT "+schema+"."+name+"($1) AS hash_page_type"};if i==2{s.Query+=" ORDER BY itemoffset"};hashBoth(t,p,backend,target,s)
   }
  }
  check(c,b,false);hashStoredInputs(t,p,b,c,held)
  defaultDump:="";if !b.PG{defaultDump=fixture.ExtensionDump(t,p.Ctx,p.Native)}
  identities:=make([]int64,4)
  for i,name:=range fixture.HashMembers{
   var oid int64;q:="SELECT 'hash_part."+name+"(bytea)'::regprocedure::bigint";if err:=c.QueryRowContext(p.Ctx,q).Scan(&oid);err!=nil||oid<=0{t.Fatal(oid,err)}
   identities[i]=oid
   for _,q:=range []string{"ALTER FUNCTION hash_part."+name+"(bytea) OWNER TO hash_owner","ALTER FUNCTION hash_part."+name+"(bytea) RENAME TO "+hashMoved[i],"ALTER FUNCTION hash_part."+hashMoved[i]+"(bytea) SET SCHEMA hash_moved"}{run(fixture.Exec(q,0))}
   var after int64;if err:=c.QueryRowContext(p.Ctx,"SELECT 'hash_moved."+hashMoved[i]+"(bytea)'::regprocedure::bigint").Scan(&after);err!=nil||after!=oid{t.Fatal("hash identity changed",after,oid,err)}
  }
  check(c,b,true);hashStoredInputs(t,p,b,c,held);hashViewAuthority(t,p,b,c,held)
  if b.PG{return}
  dump:=fixture.ExtensionDump(t,p.Ctx,p.Native);other,target,r:=pgcryptoControlDestination(t,p,"hash_restore")
  for _,q:=range []string{"CREATE ROLE hash_owner SUPERUSER","CREATE ROLE hash_reader","CREATE TABLE hash_allocate(n integer)"}{if _,err:=other.ExecContext(p.Ctx,q);err!=nil{t.Fatal(err)}}
  restored:=fixture.Backend{DB:other,Recorder:r}
  if err:=stuntgres.Restore(p.Ctx,other,strings.NewReader(defaultDump));err!=nil{t.Fatal(err)};check(target,restored,false)
  if err:=stuntgres.Restore(p.Ctx,other,strings.NewReader(dump));err!=nil{t.Fatal(err)};check(target,restored,true);hashStoredInputs(t,p,restored,target,nil)
  for i,name:=range hashMoved{var oid int64;if err:=target.QueryRowContext(p.Ctx,"SELECT 'hash_moved."+name+"(bytea)'::regprocedure::bigint").Scan(&oid);err!=nil||oid<=0||oid==identities[i]{t.Fatal("hash symbolic destination remap",oid,identities[i],err)}}
  if fixture.ExtensionDump(t,p.Ctx,other)!=dump{t.Fatal("hash symbolic replay changed")}
  if p.Postgres!=nil{func(){
   pg,err:=p.Postgres.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer pg.Close()
   clean:=func(){for _,q:=range []string{"RESET SESSION AUTHORIZATION","DROP VIEW IF EXISTS hash_type_view,hash_stats_view,hash_items_view,hash_meta_view CASCADE","DROP TABLE IF EXISTS hash_images,hash_acl_inputs","DROP EXTENSION IF EXISTS pageinspect CASCADE","DROP SCHEMA IF EXISTS hash_part CASCADE","DROP SCHEMA IF EXISTS hash_moved CASCADE","DROP OWNED BY hash_owner,hash_reader CASCADE","DROP ROLE hash_owner,hash_reader"}{if _,err:=pg.ExecContext(p.Ctx,q);err!=nil{t.Error("hash replay cleanup",q,err)}}}
   defer clean()
   for _,q:=range []string{"CREATE ROLE hash_owner SUPERUSER","CREATE ROLE hash_reader"}{if _,err:=pg.ExecContext(p.Ctx,q);err!=nil{t.Fatal(err)}}
   if _,err:=pg.ExecContext(p.Ctx,dump);err!=nil{t.Fatal("actual hash SQL oracle replay",err)}
   for i:=range fixture.HashMembers{extensionVersionPlainCheck(t,p.Ctx,pg,fixture.HashMetadata(hashViewStep(i,fixture.HashRows(i)),true,true))}
  }()
  }
  // Native replay uses Restore's bounded catalog-repair admission. The actual
  // emitted SQL is executed unchanged on pinned PostgreSQL above.
  if err:=stuntgres.Reset(p.Ctx,other);err!=nil{t.Fatal(err)};if err:=stuntgres.Restore(p.Ctx,other,strings.NewReader(dump));err!=nil{t.Fatal("hash emitted script native restore",err)};check(target,restored,true)
  old:=hashRetain(t,p,c)
  hashStoredInputs(t,p,b,c,held)
  if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
  for i,name:=range hashMoved{rows,err:=c.QueryContext(p.Ctx,"SELECT * FROM hash_moved."+name+"($1)",fixture.HashOrdinary(i));if err==nil{rows.Close();t.Fatal("reset kept hash callable",name)}}
  old()
  if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump));err!=nil{t.Fatal(err)};check(c,b,true);hashStoredInputs(t,p,b,c,held);hashViewAuthority(t,p,b,c,held)
  before:=fixture.ExtensionDump(t,p.Ctx,p.Native)
  for _,bad:=range []string{"CREATE EXTENSION missing_pageinspect_provider;",hashBadOwner(t,dump),dump+"\nUPDATE hash_images SET bucket=NULL;\nSELECT 1/0;"}{
   if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(bad));err==nil{t.Fatal("invalid hash restore accepted")};if fixture.ExtensionDump(t,p.Ctx,p.Native)!=before{t.Fatal("failed hash restore changed state")};check(c,b,true)
  }
  ctx,cancel:=context.WithCancel(p.Ctx);err:=stuntgres.Restore(ctx,p.Native,&fixture.CatalogControlCancelReader{Cancel:cancel});cancel();if !errors.Is(err,context.Canceled)||fixture.ExtensionDump(t,p.Ctx,p.Native)!=before{t.Fatal("canceled restore changed hash state",err)};check(c,b,true)
 })
}
func hashRetain(t *testing.T,p *fixture.Pair,c *sql.Conn)func(){
 t.Helper();snapshots:=make([]*sql.Rows,4);descriptors:=make([][]*sql.ColumnType,4)
 for i:=range snapshots{r,err:=c.QueryContext(p.Ctx,hashViewStep(i,nil).Query);if err!=nil{t.Fatal(err)};snapshots[i]=r;t.Cleanup(func(){r.Close()});descriptors[i],err=r.ColumnTypes();if err!=nil{t.Fatal(err)}}
 return func(){t.Helper();for i,r:=range snapshots{
  fixture.HashCheckColumns(t,descriptors[i],fixture.HashNames[i],fixture.HashTypes[i],false,true);var rows [][]any
  for r.Next(){row:=make([]any,len(fixture.HashNames[i]));dest:=make([]any,len(row));for j:=range row{dest[j]=&row[j]};if err:=r.Scan(dest...);err!=nil{t.Fatal(err)};for j:=range row{row[j]=fixture.Normalize(row[j])};rows=append(rows,row)}
  if err:=r.Err();err!=nil{t.Fatal(err)};r.Close();if !reflect.DeepEqual(rows,fixture.HashRows(i)){t.Fatal("retained hash rows changed",i,rows)};fixture.HashCheckColumns(t,descriptors[i],fixture.HashNames[i],fixture.HashTypes[i],false,true)
 }}
}
func TestReferencePageinspectHashControls(t *testing.T){hashControls(t,false)}
func TestReferencePageinspectHashFeatureSmoke(t *testing.T){hashControls(t,true)}
