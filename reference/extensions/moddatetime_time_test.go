package reference_test

import (
 "database/sql"
 "fmt"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceModdatetimeTime(t *testing.T) {
 moddatetimeCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  mdExec(t,p,b,c,"CREATE EXTENSION moddatetime","SET TimeZone='UTC'","CREATE TABLE mdt(id integer,v integer,stamp timestamp,zoned timestamptz)","CREATE TRIGGER a BEFORE UPDATE ON mdt FOR EACH ROW EXECUTE FUNCTION moddatetime('stamp')","CREATE TRIGGER b BEFORE UPDATE ON mdt FOR EACH ROW EXECUTE FUNCTION moddatetime('zoned')")
  mdRun(t,p,b,c,fixture.Exec("INSERT INTO mdt VALUES(1,0,NULL,NULL),(2,0,'2000-01-01','2000-01-01+00'),(3,0,'infinity','-infinity')",3))
  q:="UPDATE mdt SET v=$1 WHERE id=$2 RETURNING id,stamp,zoned"
  held,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer held.Close()
  mdExec(t,p,b,c,"BEGIN")
  civil,zoned:=mdTimes(t,p,c)
  for _,id:=range []int64{1,2,3} {s:=mdRows(q,[]any{id,civil,zoned});s.Args=[]any{int64(1),id};fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:held,SQL:q},b.Recorder,s)}
  mdRun(t,p,b,c,mdRows("UPDATE mdt SET stamp=NULL,zoned='1999-01-01+00' WHERE id=1 RETURNING id,stamp,zoned",[]any{int64(1),civil,zoned}))
  mdExec(t,p,b,c,"SAVEPOINT saved")
  mdRun(t,p,b,c,fixture.Exec("DELETE FROM mdt WHERE id=2",1))
  mdExec(t,p,b,c,"ROLLBACK TO saved")
  mdRun(t,p,b,c,mdRows("SELECT id,stamp,zoned FROM mdt ORDER BY id",[]any{int64(1),civil,zoned},[]any{int64(2),civil,zoned},[]any{int64(3),civil,zoned}))
  mdExec(t,p,b,c,"COMMIT","BEGIN")
  civil,zoned=mdTimes(t,p,c)
  mdRun(t,p,b,c,fixture.Prepared(mdRows("UPDATE mdt SET v=v WHERE id=2 RETURNING id,stamp,zoned",[]any{int64(2),civil,zoned})))
  mdExec(t,p,b,c,"COMMIT")
 })
}

func TestReferenceModdatetimePrecision(t *testing.T) {
 for _,precision:=range []string{"","(0)","(3)"} {t.Run("precision"+precision,func(t *testing.T){
 moddatetimeCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  mdExec(t,p,b,c,"CREATE EXTENSION moddatetime",fmt.Sprintf("CREATE TABLE mdt(id integer,stamp timestamp%s,zoned timestamptz%s)",precision,precision),"CREATE TRIGGER a BEFORE UPDATE ON mdt FOR EACH ROW EXECUTE FUNCTION moddatetime('stamp')","CREATE TRIGGER b BEFORE UPDATE ON mdt FOR EACH ROW EXECUTE FUNCTION moddatetime('zoned')")
  mdRun(t,p,b,c,fixture.Exec("INSERT INTO mdt VALUES(1,NULL,NULL),(2,NULL,NULL)",2))
  mdExec(t,p,b,c,"BEGIN","SET LOCAL TimeZone='UTC'")
  civil,instant:=mdTimes(t,p,c)
  mdRun(t,p,b,c,mdRows("UPDATE mdt SET id=id WHERE id=1 RETURNING id,stamp,zoned",[]any{int64(1),civil,instant}))
  mdExec(t,p,b,c,"SET LOCAL TimeZone='Europe/Helsinki'")
  local,same:=mdTimes(t,p,c);if !instant.Equal(same) {t.Fatal("transaction instant changed")}
  mdRun(t,p,b,c,fixture.Prepared(mdRows("UPDATE mdt SET stamp=NULL,zoned=NULL WHERE id=2 RETURNING id,stamp,zoned",[]any{int64(2),local,instant})))
  mdRun(t,p,b,c,mdRows("SELECT id,stamp,zoned FROM mdt ORDER BY id",[]any{int64(1),civil,instant},[]any{int64(2),local,instant}))
  mdExec(t,p,b,c,"COMMIT")
 }) }) }
}
