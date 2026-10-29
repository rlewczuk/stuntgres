package reference_test

import (
 "bytes"
 "context"
 "database/sql"
 "errors"
 "strings"
 "testing"
 "time"
 "stuntgres"
 "stuntgres/reference/fixture"
)

func TestReferenceModdatetimePersistence(t *testing.T) {
 moddatetimeCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  mdExec(t,p,b,c,"CREATE EXTENSION moddatetime","CREATE SCHEMA md_saved","CREATE TABLE md_saved.mdt(id integer,stamp timestamp)","CREATE TRIGGER a BEFORE UPDATE ON md_saved.mdt FOR EACH ROW WHEN (NEW.id>0) EXECUTE FUNCTION moddatetime('stamp')")
  mdExec(t,p,b,c,"CREATE TRIGGER b BEFORE UPDATE ON md_saved.mdt FOR EACH ROW EXECUTE FUNCTION moddatetime('missing')","ALTER TABLE md_saved.mdt DISABLE TRIGGER b")
  mdRun(t,p,b,c,fixture.Exec("INSERT INTO md_saved.mdt VALUES(1,NULL)",1))
  ordinaryBefore:="SELECT public.moddatetime() IS NULL AS fired"
  heldBefore,err:=c.PrepareContext(p.Ctx,ordinaryBefore);if err!=nil {t.Fatal(err)};defer heldBefore.Close()
  outside:=func(query string) fixture.Step {step:=fixture.ErrorDiagnostic(query,fixture.ControlsReferenceDiagnostic("XX000","moddatetime: not fired by trigger manager"));step.ErrorStage="execute";return step}
  fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:heldBefore,SQL:ordinaryBefore},b.Recorder,outside(ordinaryBefore))
  q:="UPDATE md_saved.mdt SET id=id RETURNING id,stamp";held,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer held.Close()
  mdExec(t,p,b,c,"ALTER FUNCTION public.moddatetime() RENAME TO stamp_actual","ALTER EXTENSION moddatetime SET SCHEMA md_saved")
  missing:=fixture.ControlsReferenceDiagnostic("42883","function public.moddatetime() does not exist")
  missing.Position="8";missing.Hint="No function matches the given name and argument types. You might need to add explicit type casts."
  invalidated:=fixture.ErrorDiagnostic(ordinaryBefore,missing);invalidated.ErrorStage="execute"
  fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:heldBefore,SQL:ordinaryBefore},b.Recorder,invalidated)
  ordinary:="SELECT md_saved.stamp_actual() IS NULL AS fired"
  heldOrdinary,err:=c.PrepareContext(p.Ctx,ordinary);if err!=nil {t.Fatal(err)};defer heldOrdinary.Close()
  mdExec(t,p,b,c,"CREATE FUNCTION public.moddatetime() RETURNS integer LANGUAGE SQL AS 'SELECT 99'","BEGIN")
  civil,_:=mdTimes(t,p,c)
  fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:held,SQL:q},b.Recorder,mdStamp(q,[]any{int64(1),civil}))
  mdExec(t,p,b,c,"COMMIT")
  check:=func(conn *sql.Conn,backend fixture.Backend){
   fixture.RunStep(t,p.Ctx,conn,backend.Recorder,mdStamp("SELECT id,stamp FROM md_saved.mdt ORDER BY id",[]any{int64(1),civil}))
   fixture.RunStep(t,p.Ctx,conn,backend.Recorder,fixture.Select("SELECT p.proname,t.tgnargs::integer,t.tgenabled::text FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid WHERE t.tgrelid='md_saved.mdt'::regclass ORDER BY t.tgname",[]string{"proname","tgnargs","tgenabled"},[]string{"NAME","INT4","TEXT"},[][]any{{"stamp_actual",int64(1),"O"},{"stamp_actual",int64(1),"D"}}))
   for _,prepared:=range []bool{false,true} {
    decoy:=fixture.Select("SELECT public.moddatetime() AS decoy",[]string{"decoy"},[]string{"INT4"},[][]any{{int64(99)}});decoy.Prepared=prepared
    fixture.RunStep(t,p.Ctx,conn,backend.Recorder,decoy)
   }
   mdError(t,p,backend,conn,ordinary,"XX000","moddatetime: not fired by trigger manager")
   if conn==c {fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:conn,Statement:heldOrdinary,SQL:ordinary},backend.Recorder,outside(ordinary))} else {
    fixture.RunStep(t,p.Ctx,conn,backend.Recorder,fixture.Prepared(outside(ordinary)))
   }
   fixture.RunStep(t,p.Ctx,conn,backend.Recorder,fixture.Exec("BEGIN",0))
   now,_:=mdTimes(t,p,conn);s:=mdStamp(q,[]any{int64(1),now})
   if conn==c {fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:conn,Statement:held,SQL:q},backend.Recorder,s)} else {fixture.RunStep(t,p.Ctx,conn,backend.Recorder,fixture.Prepared(s))}
   fixture.RunStep(t,p.Ctx,conn,backend.Recorder,mdStamp("UPDATE md_saved.mdt SET id=0,stamp=NULL RETURNING id,stamp",[]any{int64(0),nil}))
   fixture.RunStep(t,p.Ctx,conn,backend.Recorder,fixture.Exec("ROLLBACK",0))
  }
  check(c,b)
  if !b.PG {mdReplayControls(t,p,b,c,check)}
  mdExec(t,p,b,c,"ALTER TABLE md_saved.mdt RENAME COLUMN stamp TO renamed")
  mdError(t,p,b,c,"UPDATE md_saved.mdt SET id=id","09000",`"mdt" has no attribute "stamp"`)
  mdExec(t,p,b,c,"DROP TRIGGER a ON md_saved.mdt","CREATE TRIGGER a BEFORE UPDATE ON md_saved.mdt FOR EACH ROW EXECUTE FUNCTION md_saved.stamp_actual('renamed')")
  mdRun(t,p,b,c,fixture.Exec("UPDATE md_saved.mdt SET id=id",1))
 })
}

// These are native-control phases of the same dual-mode scenarios, not a
// substitute oracle implementation or a backend filter for SQL behavior.
func mdReplayControls(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,check func(*sql.Conn,fixture.Backend)) {
 t.Helper();var dump bytes.Buffer
 if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil {t.Fatal(err)}
 retained,err:=c.QueryContext(p.Ctx,"SELECT id,stamp FROM md_saved.mdt ORDER BY id");if err!=nil {t.Fatal(err)};defer retained.Close()
 meta,err:=retained.ColumnTypes();if err!=nil||len(meta)!=2||meta[1].DatabaseTypeName()!="TIMESTAMP" {t.Fatal(meta,err)}
 var saved time.Time
 if err:=c.QueryRowContext(p.Ctx,"SELECT stamp FROM md_saved.mdt WHERE id=1").Scan(&saved);err!=nil {t.Fatal(err)}
 if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil {t.Fatal(err)};check(c,b)
 if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil {t.Fatal(err)}
 if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil {t.Fatal(err)};check(c,b)
 fresh:=fixture.NewFixture(t,false,true);fc,err:=fresh.Memory.Conn(fresh.Ctx);if err!=nil {t.Fatal(err)};defer fc.Close()
 if err:=stuntgres.Restore(fresh.Ctx,fresh.Native,bytes.NewReader(dump.Bytes()));err!=nil {t.Fatal(err)};check(fc,fixture.Backend{DB:fresh.Memory,Recorder:fresh.MemoryRecord})
 mdExec(t,p,b,c,"CREATE TABLE md_saved.sentinel(n integer)");mdRun(t,p,b,c,fixture.Exec("INSERT INTO md_saved.sentinel VALUES(42)",1))
 for _,bad:=range []string{dump.String()+";CREATE TABLE md_saved.prefix(n integer);SELECT missing_native_restore;"} {if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(bad));err==nil {t.Fatal("malformed Restore accepted")};check(c,b)}
 ctx,cancel:=context.WithCancel(p.Ctx);cancel();if err:=stuntgres.Restore(ctx,p.Native,bytes.NewReader(dump.Bytes()));!errors.Is(err,context.Canceled){t.Fatal(err)}
 check(c,b);mdRun(t,p,b,c,fixture.Select("SELECT n FROM md_saved.sentinel",[]string{"n"},[]string{"INT4"},[][]any{{int64(42)}}))
 if !retained.Next(){t.Fatal(retained.Err())};var id int64;var stamp time.Time;if err:=retained.Scan(&id,&stamp);err!=nil||id!=1||!stamp.Equal(saved){t.Fatal(id,stamp,saved,err)};if retained.Next()||retained.Err()!=nil {t.Fatal("retained cardinality",retained.Err())}
}
