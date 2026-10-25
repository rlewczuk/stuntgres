package reference_test

import (
 "bytes"
 "database/sql"
 "strings"
 "testing"

 "stuntgres"
 "stuntgres/reference/fixture"
)

func citextStringRenamedChecks(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor) {
 t.Helper()
 for _,d:=range citextStringDeclarations {
  current:=strings.Replace(d.query(),"public."+d.name,"cs_moved.cs_"+d.name,1)
  citextStringRoutes(t,p,b,c,citextStringValue(current,d.result,d.expected,d.values...))
  citextStringRoutes(t,p,b,c,citextStringValue("SELECT v FROM cs_identity_"+d.name,d.result,d.expected))
 }
 citextStringRoutes(t,p,b,c,fixture.Select("SELECT r,f,p,t FROM cs_identity_defaults",[]string{"r","f","p","t"},[]string{"TEXT","TEXT","INT4","TEXT"},[][]any{{"XbX","b",int64(1),"ABBA"}}))
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO cs_identity_defaults(s) VALUES('a')",1))
 citextStringRoutes(t,p,b,c,fixture.Select("SELECT count(*) AS n FROM cs_identity_defaults WHERE r='XbX' AND f='b' AND p=1 AND t='ABBA'",[]string{"n"},[]string{"INT8"},[][]any{{int64(2)}}))
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("DELETE FROM cs_identity_defaults WHERE s::text='a'",1))
}
func TestReferenceCitextStringProvenance(t *testing.T) {
 fixture.RoleReferenceCleanup(t,[]string{"cs_identity_reader"})
 fixture.SchemaReferenceCleanup(t,"cs_moved")
 citextStringScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  citextInstall(t,p,b,c)
  run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
  defer c.ExecContext(p.Ctx,"RESET ROLE")
  defer c.ExecContext(p.Ctx,"ROLLBACK")
  run(fixture.Exec("CREATE SCHEMA cs_moved",0));run(fixture.Exec("CREATE ROLE cs_identity_reader",0))
  run(fixture.Exec("CREATE TABLE cs_identity_source(s citext,d citext,r citext,n integer,t text)",0))
  run(fixture.Exec("INSERT INTO cs_identity_source VALUES('AbA','a','X',2,'X')",1))
  for _,d:=range citextStringDeclarations {
   args:="s,d";if d.name=="replace" {args+=",r"};if d.name=="split_part" {args+=",n"};if d.name=="translate" {args+=",t"}
   run(fixture.Exec("CREATE VIEW cs_identity_"+d.name+" AS SELECT public."+d.name+"("+args+") AS v FROM cs_identity_source",0))
  }
  run(fixture.Exec("CREATE TABLE cs_identity_defaults(s citext,CONSTRAINT cs_identity_guard CHECK(strpos(s,'a')>0 AND replace(s,'a','X')<>'z' AND split_part(s,'a',1)<>'z' AND translate(s,'a','X')<>'z'),r text DEFAULT replace('AbA'::citext,'a','X'),f text DEFAULT split_part('aTb'::citext,'t',2),p integer DEFAULT strpos('ABC'::citext,'a'),t text DEFAULT translate('aAbB'::citext,'ab','BA'))",0))
  run(fixture.Exec("INSERT INTO cs_identity_defaults(s) VALUES('A')",1))
  for _,d:=range citextStringDeclarations {
   q:=d.query();stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)}
   defer stmt.Close()
   held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
   fixture.RunStep(t,p.Ctx,held,b.Recorder,citextStringValue(q,d.result,d.expected,d.values...))
   var before,after int64
   if err:=c.QueryRowContext(p.Ctx,"SELECT $1::regprocedure::oid::bigint",d.signature()).Scan(&before);err!=nil {t.Fatal(err)}
   run(fixture.Exec("ALTER FUNCTION "+d.signature()+" RENAME TO cs_"+d.name,0))
   missing:=fixture.ControlsReferenceDiagnostic("42883","function public."+d.name+"("+strings.Join(d.args,", ")+") does not exist")
   missing.Hint="No function matches the given name and argument types. You might need to add explicit type casts."
   missing.Position="8"
   failure:=fixture.ErrorDiagnostic(q,missing,d.values...);failure.SelectRows=true;failure.ErrorStage="execute"
   fixture.RunStep(t,p.Ctx,held,b.Recorder,failure)
   if err:=stmt.Close();err!=nil {t.Fatal(err)}
   renamedSignature:="public.cs_"+d.name+"("+strings.Join(d.args,",")+")"
   run(fixture.Exec("ALTER FUNCTION "+renamedSignature+" SET SCHEMA cs_moved",0))
   if err:=c.QueryRowContext(p.Ctx,"SELECT $1::regprocedure::oid::bigint",strings.Replace(renamedSignature,"public.","cs_moved.",1)).Scan(&after);err!=nil {t.Fatal(err)}
   if before<=0||before!=after {t.Fatal("routine identity changed during rename/move",before,after)}
   movedSignature:=strings.Replace(renamedSignature,"public.","cs_moved.",1)
   run(fixture.Exec("BEGIN",0))
   run(fixture.Exec("GRANT USAGE ON SCHEMA public,cs_moved TO cs_identity_reader",0))
   run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+movedSignature+" FROM PUBLIC",0))
   current:=strings.Replace(q,"public."+d.name,"cs_moved.cs_"+d.name,1)
   run(fixture.Exec("SET ROLE cs_identity_reader",0))
   denied:=fixture.ErrorDiagnostic(current,fixture.ControlsReferenceDiagnostic("42501","permission denied for function cs_"+d.name),d.values...);denied.SelectRows=true
   for _,prepared:=range []bool{false,true} {
    run(fixture.Exec("SAVEPOINT cs_acl_probe",0))
    failure:=denied;if prepared {failure=fixture.Prepared(failure)}
    run(failure)
    run(fixture.Exec("ROLLBACK TO SAVEPOINT cs_acl_probe",0))
    run(fixture.Exec("RELEASE SAVEPOINT cs_acl_probe",0))
   }
   run(fixture.Exec("RESET ROLE",0));run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+movedSignature+" TO PUBLIC",0))
   citextStringRoutes(t,p,b,c,citextStringValue(current,d.result,d.expected,d.values...))
   citextStringRoutes(t,p,b,c,citextStringValue("SELECT v FROM cs_identity_"+d.name,d.result,d.expected))
   run(fixture.Exec("ROLLBACK",0))
  }
  citextStringRenamedChecks(t,p,b,c)
  if b.PG {return}
  var dump bytes.Buffer;if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil {t.Fatal(err)}
  if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil {t.Fatal(err)}
  if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil {t.Fatal(err)}
  citextStringRenamedChecks(t,p,b,c)
  target:=fixture.NewFixture(t,false,true)
  fixture.RunStep(t,target.Ctx,target.Memory,target.MemoryRecord,fixture.Exec("CREATE TABLE cs_identity_spacer(v integer)",0))
  // Roles are cluster state on PostgreSQL, but engine-local on independent drivers.
  fixture.RunStep(t,target.Ctx,target.Memory,target.MemoryRecord,fixture.Exec("CREATE ROLE cs_identity_reader",0))
  if err:=stuntgres.Restore(target.Ctx,target.Native,bytes.NewReader(dump.Bytes()));err!=nil {t.Fatal(err)}
  dest:=fixture.Backend{DB:target.Memory,Recorder:target.MemoryRecord}
  citextStringRenamedChecks(t,target,dest,target.Memory)
  if p.Postgres!=nil {
   replay:=citextStringReplayConn(t,p,true,"cs_identity_reader")
   if _,err:=replay.ExecContext(p.Ctx,dump.String());err!=nil {t.Fatal("actual emitted renamed SQL replay",err)}
   citextStringReplayReady(t,p,replay)
   pg:=fixture.Backend{PG:true,DB:p.Postgres,Recorder:p.PostgresRecord};citextStringRenamedChecks(t,p,pg,replay)
  }
 })
}
