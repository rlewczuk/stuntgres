package reference_test

import (
 "bytes"
 "context"
 "database/sql"
 "encoding/base64"
 "encoding/json"
 "errors"
 "reflect"
 "strings"
 "testing"
 "time"

 "stuntgres"
 "stuntgres/reference/fixture"
)

func citextStringStoredSetup(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
 t.Helper();citextInstall(t,p,b,c);citextStringCorpus(t,p,b,c)
 for _,q:=range []string{
  "CREATE VIEW cs_view AS "+citextStringProjection+" WHERE replace(s,d,r)='XbX' AND split_part(s,d,n)='b' AND strpos(s,d)>0 AND translate(s,d,t)='XbX'",
  "CREATE TABLE cs_guard(id integer,s citext,CONSTRAINT cs_has_a CHECK(strpos(s,'a')>0),CONSTRAINT cs_replace CHECK(replace(s,'a','X')<>'z'),CONSTRAINT cs_split CHECK(split_part(s,'a',1)<>'z'),CONSTRAINT cs_translate CHECK(translate(s,'a','X')<>'z'),r text DEFAULT replace('AbA'::citext,'a','X'),f text DEFAULT split_part('aTb'::citext,'t',2),p integer DEFAULT strpos('ABC'::citext,'a'),t text DEFAULT translate('aAbB'::citext,'ab','BA'))",
 } {fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0))}
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO cs_guard(id,s) VALUES(1,'A')",1))
}
func citextStringCheckDiagnostic(table,constraint string,id int) fixture.Diagnostic {
 d:=fixture.ControlsReferenceDiagnostic("23514",`new row for relation "`+table+`" violates check constraint "`+constraint+`"`)
 d.Schema="public";d.Table=table;d.Constraint=constraint
 if table=="cs_guard" {d.Detail="Failing row contains (3, z, XbX, b, 1, ABBA)."} else {d.Detail="Failing row contains (z)."}
 return d
}
func citextStringStoredChecks(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor) {
 t.Helper();citextStringCorpusChecks(t,p,b,c)
 citextStringRoutes(t,p,b,c,fixture.Select("SELECT id,r,s,p,t FROM cs_view ORDER BY id",citextStringProjectionNames,citextStringProjectionTypes,[][]any{{int64(1),"XbX","b",int64(1),"XbX"}}))
 citextStringRoutes(t,p,b,c,fixture.Select("SELECT id,s::text AS s,r,f,p,t FROM cs_guard ORDER BY id",[]string{"id","s","r","f","p","t"},[]string{"INT4","TEXT","TEXT","TEXT","INT4","TEXT"},[][]any{{int64(1),"A","XbX","b",int64(1),"ABBA"}}))
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO cs_guard(id,s) VALUES(2,'a')",1))
 citextStringRoutes(t,p,b,c,fixture.Select("SELECT r,f,p,t FROM cs_guard WHERE id=2",[]string{"r","f","p","t"},[]string{"TEXT","TEXT","INT4","TEXT"},[][]any{{"XbX","b",int64(1),"ABBA"}}))
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("DELETE FROM cs_guard WHERE id=2",1))
 for _,prepared:=range []bool{false,true} {citextError(t,p,b,c,"INSERT INTO cs_guard(id,s) VALUES(2,'A'),(3,'z')",prepared,false,false,citextStringCheckDiagnostic("cs_guard","cs_has_a",3))}
 citextStringRoutes(t,p,b,c,fixture.Select("SELECT id FROM cs_guard ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)}}))
}
func citextStringCorruptSignature(t *testing.T,script string) string {
 t.Helper();const marker="-- stuntgres-extension-owners-v2 "
 start:=strings.Index(script,marker);if start<0 {t.Fatal("missing owner record")}
 offset:=strings.IndexByte(script[start:],'\n');if offset<0 {t.Fatal("unterminated owner record")};end:=start+offset
 data,err:=base64.StdEncoding.DecodeString(script[start+len(marker):end]);if err!=nil {t.Fatal(err)}
 var record map[string]json.RawMessage;if err:=json.Unmarshal(data,&record);err!=nil {t.Fatal(err)}
 var members []map[string]json.RawMessage;if err:=json.Unmarshal(record["members"],&members);err!=nil {t.Fatal(err)}
 found:=false
 for _,member:=range members {
  var name,kind,signature string
  if err:=json.Unmarshal(member["name"],&name);err!=nil {t.Fatal(err)}
  if err:=json.Unmarshal(member["kind"],&kind);err!=nil {t.Fatal(err)}
  if err:=json.Unmarshal(member["signature"],&signature);err!=nil {t.Fatal(err)}
  if name=="translate"&&kind=="procedure"&&signature=="citext,citext,25" {
   if found {t.Fatal("duplicate translate member")};found=true
   member["signature"]=json.RawMessage(`"citext,citext,citext"`)
  }
 }
 if !found {t.Fatal("missing genuine translate owner signature")}
 record["members"],err=json.Marshal(members);if err!=nil {t.Fatal(err)}
 data,err=json.Marshal(record);if err!=nil {t.Fatal(err)}
 return script[:start]+marker+base64.StdEncoding.EncodeToString(data)+script[end:]
}
func citextStringReplayConn(t *testing.T,p *fixture.Pair,moved bool,reader string) *sql.Conn {
 t.Helper()
 c,err:=p.Postgres.Conn(p.Ctx);if err!=nil {t.Fatal(err)}
 cleanup:=[]string{"ROLLBACK","RESET SESSION AUTHORIZATION","RESET ROLE","DROP EXTENSION IF EXISTS citext CASCADE"}
 if moved {cleanup=append(cleanup,"DROP SCHEMA IF EXISTS cs_moved CASCADE")}
 cleanup=append(cleanup,"DROP SCHEMA public CASCADE","CREATE SCHEMA public")
 if reader!="" {cleanup=append(cleanup,"DROP ROLE IF EXISTS "+citextIdentifier(reader))}
 cleanup=append(cleanup,"DISCARD ALL")
 t.Cleanup(func(){
  ctx,cancel:=context.WithTimeout(context.Background(),15*time.Second);defer cancel();defer c.Close()
  for _,q:=range cleanup {if _,err:=c.ExecContext(ctx,q);err!=nil {t.Errorf("citext string replay cleanup %s: %v",q,err)}}
 })
 // Every emitted script and its observations share one physical SQL session.
 // Native dump windows may change search_path or session authorization.
 for _,q:=range []string{"RESET SESSION AUTHORIZATION","RESET ROLE","SET search_path=public","DROP EXTENSION IF EXISTS citext CASCADE"} {
  if _,err:=c.ExecContext(p.Ctx,q);err!=nil {t.Fatal(err)}
 }
 if moved {if _,err:=c.ExecContext(p.Ctx,"DROP SCHEMA IF EXISTS cs_moved CASCADE");err!=nil {t.Fatal(err)}}
 if _,err:=c.ExecContext(p.Ctx,"DROP SCHEMA public CASCADE;CREATE SCHEMA public");err!=nil {t.Fatal(err)}
 if reader!="" {if _,err:=c.ExecContext(p.Ctx,"CREATE ROLE "+citextIdentifier(reader));err!=nil {t.Fatal(err)}}
 return c
}
func citextStringReplayReady(t *testing.T,p *fixture.Pair,c *sql.Conn) {
 t.Helper()
 for _,q:=range []string{"RESET SESSION AUTHORIZATION","RESET ROLE","SET search_path=public"} {
  if _,err:=c.ExecContext(p.Ctx,q);err!=nil {t.Fatal(err)}
 }
}
func citextStringReplay(t *testing.T,p *fixture.Pair,script string,acl bool) {
 t.Helper();if p.Postgres==nil {return}
 reader:="";if acl {reader="cs_control_reader"}
 c:=citextStringReplayConn(t,p,false,reader)
 if _,err:=c.ExecContext(p.Ctx,script);err!=nil {t.Fatal("actual emitted SQL replay",err)}
 citextStringReplayReady(t,p,c)
 pg:=fixture.Backend{PG:true,DB:p.Postgres,Recorder:p.PostgresRecord}
 citextStringStoredChecks(t,p,pg,c);citextStringCatalogChecks(t,p,pg,c)
 if acl {citextStringControlACLChecks(t,p,pg,c)}
}
func citextStringControlACLSetup(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
 t.Helper()
 for _,d:=range citextStringDeclarations {
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("REVOKE EXECUTE ON FUNCTION "+d.signature()+" FROM PUBLIC",0))
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("GRANT EXECUTE ON FUNCTION "+d.signature()+" TO cs_control_reader",0))
 }
}
func citextStringControlACLChecks(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
 t.Helper()
 run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
 run(fixture.Exec("BEGIN",0));defer c.ExecContext(p.Ctx,"ROLLBACK");defer c.ExecContext(p.Ctx,"RESET ROLE")
 run(fixture.Exec("GRANT USAGE ON SCHEMA public TO cs_control_reader",0))
 citextStringControlACLSetup(t,p,b,c)
 // Each expected denial has its own savepoint: PostgreSQL errors abort the
 // transaction, while the final rollback must restore the exact ACL baseline.
 deny:=func(executor fixture.Executor,failure fixture.Step) {
  run(fixture.Exec("SAVEPOINT cs_control_acl",0))
  fixture.RunStep(t,p.Ctx,executor,b.Recorder,failure)
  run(fixture.Exec("ROLLBACK TO SAVEPOINT cs_control_acl",0))
  run(fixture.Exec("RELEASE SAVEPOINT cs_control_acl",0))
 }
 for _,d:=range citextStringDeclarations {
  q:=d.query();stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close()
  held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
  good:=citextStringValue(q,d.result,d.expected,d.values...)
  run(fixture.Exec("SET ROLE cs_control_reader",0))
  citextStringRoutes(t,p,b,c,good);fixture.RunStep(t,p.Ctx,held,b.Recorder,good)
  run(fixture.Exec("RESET ROLE",0))
  run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+d.signature()+" FROM cs_control_reader",0))
  run(fixture.Exec("SET ROLE cs_control_reader",0))
  failure:=fixture.ErrorDiagnostic(q,fixture.ControlsReferenceDiagnostic("42501","permission denied for function "+d.name),d.values...);failure.SelectRows=true;failure.ErrorStage="execute"
  deny(c,failure);deny(c,fixture.Prepared(failure));deny(held,failure)
  run(fixture.Exec("RESET ROLE",0))
  run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+d.signature()+" TO cs_control_reader",0))
  run(fixture.Exec("SET ROLE cs_control_reader",0))
  fixture.RunStep(t,p.Ctx,held,b.Recorder,good);citextStringRoutes(t,p,b,c,good)
  run(fixture.Exec("RESET ROLE",0))
  run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+d.signature()+" FROM cs_control_reader",0))
  run(fixture.Exec("SET ROLE cs_control_reader",0));deny(held,failure)
  run(fixture.Exec("RESET ROLE",0))
  if err:=stmt.Close();err!=nil {t.Fatal(err)}
 }
 run(fixture.Exec("ROLLBACK",0))
}
func citextStringIdentities(t *testing.T,p *fixture.Pair,c *sql.Conn) []int64 {
 t.Helper();ids:=make([]int64,1+len(citextStringDeclarations))
 if err:=c.QueryRowContext(p.Ctx,"SELECT 'public.citext'::regtype::oid::bigint").Scan(&ids[0]);err!=nil {t.Fatal(err)}
 for i,d:=range citextStringDeclarations {
  if err:=c.QueryRowContext(p.Ctx,"SELECT $1::regprocedure::oid::bigint",d.signature()).Scan(&ids[i+1]);err!=nil {t.Fatal(err)}
 }
 return ids
}
func TestReferenceCitextStringControls(t *testing.T) {
 for _,phase:=range []string{"same_engine_and_emitted_replay","independent_driver","failed_restore","owner_corruption"} {t.Run(phase,func(t *testing.T){
 fixture.RoleReferenceCleanup(t,[]string{"cs_control_reader"})
 citextStringScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  citextStringStoredSetup(t,p,b,c);citextStringStoredChecks(t,p,b,c);citextStringCatalogChecks(t,p,b,c)
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE ROLE cs_control_reader",0))
  citextStringControlACLChecks(t,p,b,c)
  if phase=="failed_restore" {
   fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE cs_late(s citext,CONSTRAINT cs_late_has_a CHECK(strpos(s,'a')>0))",0))
   for _,prepared:=range []bool{false,true} {citextError(t,p,b,c,"INSERT INTO cs_late VALUES('A'),('z')",prepared,false,false,citextStringCheckDiagnostic("cs_late","cs_late_has_a",0))}
   citextStringRoutes(t,p,b,c,fixture.Select("SELECT s::text AS s FROM cs_late",[]string{"s"},[]string{"TEXT"},nil))
   fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("DROP TABLE cs_late",0))
  }
  if b.PG {
   for _,q:=range []string{"DROP VIEW cs_view","DROP TABLE cs_guard,cs_source","DROP EXTENSION citext"} {fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0))}
   citextStringStoredSetup(t,p,b,c);citextStringStoredChecks(t,p,b,c);citextStringCatalogChecks(t,p,b,c)
   citextStringControlACLChecks(t,p,b,c)
   return
  }
  identities:=citextStringIdentities(t,p,c)
  var dump bytes.Buffer;if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil {t.Fatal(err)}
  old,err:=c.QueryContext(p.Ctx,strings.Replace(citextStringProjection,"id,","",1)+" ORDER BY id");if err!=nil {t.Fatal(err)};defer old.Close()
  metadata:=citextStringMetadata(t,b,old)
  if phase=="independent_driver" {
   target:=fixture.NewFixture(t,false,true)
   conn,err:=target.Memory.Conn(target.Ctx);if err!=nil {t.Fatal(err)}
   defer conn.Close()
   sourceIDs:=map[string]int64{"citext":identities[0]}
   for i,d:=range citextStringDeclarations {sourceIDs[d.name]=identities[i+1]}
   retained:=cubeComparisonForceCollisions(t,target,conn,sourceIDs)
   if err:=stuntgres.Restore(target.Ctx,target.Native,bytes.NewReader(dump.Bytes()));err!=nil {t.Fatal(err)}
   dest:=fixture.Backend{DB:target.Memory,Recorder:target.MemoryRecord}
   citextStringStoredChecks(t,target,dest,target.Memory);citextStringCatalogChecks(t,target,dest,target.Memory)
   mapped:=citextStringIdentities(t,target,conn)
   for i,id:=range identities {if mapped[i]==id {t.Fatal("retained TEMP collision did not remap installed identity",i,id)}}
   if got:=cubeComparisonTemporaryIDs(t,target,conn);!reflect.DeepEqual(got,retained) {t.Fatal("Restore changed retained TEMP identities",got,retained)}
   fixture.RunStep(t,target.Ctx,conn,target.MemoryRecord,fixture.Select("SELECT v FROM pg_temp.cube_compare_retained_0",[]string{"v"},[]string{"INT4"},[][]any{{int64(73)}}))
   fixture.RunStep(t,target.Ctx,conn,target.MemoryRecord,fixture.Exec("CREATE ROLE cs_control_reader",0))
   citextStringControlACLChecks(t,target,dest,conn)
   var first,second bytes.Buffer
   if err:=stuntgres.Dump(target.Ctx,target.Native,&first);err!=nil {t.Fatal(err)}
   if err:=stuntgres.Dump(target.Ctx,target.Native,&second);err!=nil {t.Fatal(err)}
   if !bytes.Equal(first.Bytes(),second.Bytes()) {t.Fatal("nondeterministic target dump")}
  } else if phase=="same_engine_and_emitted_replay" {
   if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil {t.Fatal(err)}
   if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil {t.Fatal(err)}
  } else {
   bad:="CREATE EXTENSION citext;CREATE TABLE cs_late(s citext,CONSTRAINT cs_late_has_a CHECK(strpos(s,'a')>0));INSERT INTO cs_late VALUES('A'),('z');"
   if phase=="owner_corruption" {
    bad=citextStringCorruptSignature(t,dump.String())
    err=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(bad));var state interface{SQLState()string}
    if err==nil||!strings.Contains(err.Error(),"invalid extension owner metadata")||errors.As(err,&state) {t.Fatal("owner signature corruption",err)}
   } else {want:=citextStringCheckDiagnostic("cs_late","cs_late_has_a",0);fixture.AssertDiagnostic(t,stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(bad)),&want)}
   var after bytes.Buffer;if err:=stuntgres.Dump(p.Ctx,p.Native,&after);err!=nil {t.Fatal(err)}
   if !bytes.Equal(after.Bytes(),dump.Bytes()) {t.Fatal("failed Restore published catalog or rows")}
  }
  if phase=="failed_restore"||phase=="owner_corruption" {
   if !reflect.DeepEqual(identities,citextStringIdentities(t,p,c)) {t.Fatal("failed Restore changed installed identities")}
  }
  citextStringMetadata(t,b,old)
  var got [][]any
  for old.Next() {var r,s,pos,tr any;if err:=old.Scan(&r,&s,&pos,&tr);err!=nil {t.Fatal(err)};got=append(got,[]any{fixture.Normalize(r),fixture.Normalize(s),fixture.Normalize(pos),fixture.Normalize(tr)})}
  if err:=old.Err();err!=nil {t.Fatal(err)};old.Close()
  want:=[][]any{{"XbX","b",int64(1),"XbX"},{"x","x",int64(0),"x"},{nil,nil,nil,nil},{"X","",int64(1),""}}
  if !reflect.DeepEqual(got,want)||metadata[0].DatabaseTypeName()!="TEXT"||metadata[2].DatabaseTypeName()!="INT4" {t.Fatal("retained reader",got,metadata)}
  citextStringStoredChecks(t,p,b,c);citextStringCatalogChecks(t,p,b,c)
  citextStringControlACLChecks(t,p,b,c)
  if phase=="same_engine_and_emitted_replay" {citextStringReplay(t,p,dump.String(),true)}
 })})}
}
