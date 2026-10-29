package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceModdatetimeShared(t *testing.T) {
 moddatetimeCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  mdExec(t,p,b,c,"CREATE EXTENSION moddatetime","CREATE TABLE mdt(id integer,v integer,stamp timestamp)","CREATE TRIGGER a BEFORE UPDATE ON mdt FOR EACH ROW WHEN (NEW.v>0) EXECUTE FUNCTION moddatetime('stamp')")
  mdRun(t,p,b,c,fixture.Exec("INSERT INTO mdt VALUES(1,0,NULL),(2,0,NULL)",2))
  mdRun(t,p,b,c,mdStamp("UPDATE mdt SET v=NULL WHERE id=1 RETURNING id,stamp",[]any{int64(1),nil}))
  mdRun(t,p,b,c,fixture.Prepared(mdStamp("UPDATE mdt SET v=0 WHERE id=1 RETURNING id,stamp",[]any{int64(1),nil})))
  mdExec(t,p,b,c,"ALTER TABLE mdt DISABLE TRIGGER a")
  mdRun(t,p,b,c,mdStamp("UPDATE mdt SET v=1 WHERE id=1 RETURNING id,stamp",[]any{int64(1),nil}))
  mdExec(t,p,b,c,"ALTER TABLE mdt ENABLE TRIGGER a","BEGIN")
  civil,_:=mdTimes(t,p,c)
  mdRun(t,p,b,c,mdStamp("UPDATE mdt SET v=v WHERE id=1 RETURNING id,stamp",[]any{int64(1),civil}))
  mdExec(t,p,b,c,"COMMIT")
  mdExec(t,p,b,c,"CREATE FUNCTION public.md_observe() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.stamp IS NULL THEN RAISE EXCEPTION 'stamp not visible'; END IF; NEW.v=NEW.v+1; RETURN NEW; END $$","CREATE TRIGGER z BEFORE UPDATE ON mdt FOR EACH ROW EXECUTE FUNCTION public.md_observe()")
  mdRun(t,p,b,c,fixture.Exec("UPDATE mdt SET v=1",2))
  mdRun(t,p,b,c,fixture.Select("SELECT id,v FROM mdt ORDER BY id",[]string{"id","v"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(2)},{int64(2),int64(2)}}))
  mdExec(t,p,b,c,"CREATE OR REPLACE FUNCTION public.md_observe() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id=2 THEN RAISE EXCEPTION 'late row'; END IF; RETURN NEW; END $$")
  // A later row error must expose neither a successful RETURNING prefix nor writes.
  var before1,before2 any
  if err:=c.QueryRowContext(p.Ctx,"SELECT stamp FROM mdt WHERE id=1").Scan(&before1);err!=nil {t.Fatal(err)}
  if err:=c.QueryRowContext(p.Ctx,"SELECT stamp FROM mdt WHERE id=2").Scan(&before2);err!=nil {t.Fatal(err)}
  before1,before2=fixture.Normalize(before1),fixture.Normalize(before2)
  for _,prepared:=range []bool{false,true} {
   q:="UPDATE mdt SET v=9 RETURNING id,stamp";var rows *sql.Rows;var err error
   if prepared {s,e:=c.PrepareContext(p.Ctx,q);if e!=nil {t.Fatal(e)};rows,err=s.QueryContext(p.Ctx);s.Close()} else {rows,err=c.QueryContext(p.Ctx,q)}
   if rows!=nil {if rows.Next(){rows.Close();t.Fatal("failed statement exposed prefix")};if err==nil {err=rows.Err()};rows.Close()}
   if err==nil {t.Fatal("late error missing")}
   expected:=fixture.ControlsReferenceDiagnostic("P0001","late row");expected.Context="PL/pgSQL function md_observe() line 1 at RAISE"
   fixture.AssertDiagnostic(t,err,&expected)
   mdRun(t,p,b,c,mdStamp("SELECT id,stamp FROM mdt ORDER BY id",[]any{int64(1),before1},[]any{int64(2),before2}))
  }
  mdExec(t,p,b,c,"DROP FUNCTION public.md_observe() CASCADE")
 })
}
