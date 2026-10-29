package reference_test

import (
 "bytes"
 "database/sql"
 "testing"
 "stuntgres"
 "stuntgres/reference/fixture"
)

func TestReferenceModdatetimeNativePrecisionReplay(t *testing.T) {
 for _,precision:=range []string{"0","3"} {t.Run("precision"+precision,func(t *testing.T){
 moddatetimeCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  mdExec(t,p,b,c,"CREATE EXTENSION moddatetime","CREATE SCHEMA md_saved","SET TimeZone='Europe/Helsinki'","CREATE TABLE md_saved.mdt(id integer,stamp timestamp("+precision+"),zoned timestamptz("+precision+"))","CREATE TRIGGER a BEFORE UPDATE ON md_saved.mdt FOR EACH ROW EXECUTE FUNCTION moddatetime('stamp')","CREATE TRIGGER b BEFORE UPDATE ON md_saved.mdt FOR EACH ROW EXECUTE FUNCTION moddatetime('zoned')")
  mdRun(t,p,b,c,fixture.Exec("INSERT INTO md_saved.mdt VALUES(1,NULL,NULL),(2,NULL,NULL)",2))
  mdExec(t,p,b,c,"BEGIN");civil,zoned:=mdTimes(t,p,c)
  mdRun(t,p,b,c,mdRows("UPDATE md_saved.mdt SET id=id WHERE id=1 RETURNING id,stamp,zoned",[]any{int64(1),civil,zoned}))
  mdRun(t,p,b,c,fixture.Prepared(mdRows("UPDATE md_saved.mdt SET id=id WHERE id=2 RETURNING id,stamp,zoned",[]any{int64(2),civil,zoned})))
  mdExec(t,p,b,c,"COMMIT","SET DateStyle='ISO, MDY'")
  check:=func(conn *sql.Conn,backend fixture.Backend){fixture.RunStep(t,p.Ctx,conn,backend.Recorder,mdRows("SELECT id,stamp,zoned FROM md_saved.mdt ORDER BY id",[]any{int64(1),civil,zoned},[]any{int64(2),civil,zoned}))}
  check(c,b)
  if b.PG {return}
  mdRejectTemporalReplay(t,p,b,c,check)
  replay:=func(){
   mdExec(t,p,b,c,"SET DateStyle='SQL, DMY'")
   var dump bytes.Buffer;if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil {t.Fatal(err)}
   mdExec(t,p,b,c,"SET DateStyle='ISO, MDY'")
   if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil {t.Fatal(err)};check(c,b)
   if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil {t.Fatal(err)}
   if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil {t.Fatal(err)};check(c,b)
   fresh:=fixture.NewFixture(t,false,true);fc,err:=fresh.Memory.Conn(fresh.Ctx);if err!=nil {t.Fatal(err)};defer fc.Close()
   if err:=stuntgres.Restore(fresh.Ctx,fresh.Native,bytes.NewReader(dump.Bytes()));err!=nil {t.Fatal(err)};check(fc,fixture.Backend{DB:fresh.Memory,Recorder:fresh.MemoryRecord})
  }
  replay()
  mdExec(t,p,b,c,"DROP EXTENSION moddatetime CASCADE")
  replay() // Exact data is destination-owned temporal shape, not live helper authority.
 })
 })}
}
