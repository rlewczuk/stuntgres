package reference_test

import (
 "bytes"
 "database/sql"
 "strings"
 "testing"

 "stuntgres"
 "stuntgres/reference/fixture"
)

func TestReferenceRoutineTransformDefinitionBootstrapReplay(t *testing.T) {
 definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  definitionAnnotate(t,p,b,c)
  from,to:="go_host_from_hstore","go_host_to_hstore"
  if b.PG {from,to="hstore_to_plpython3","plpython3_to_hstore"}
  for _,name:=range []string{from,to} {hostExec(t,p,b,c,"ALTER FUNCTION "+name+"(internal) STABLE")}
  hostExec(t,p,b,c,definitionCreate(from,to,false))
  oid:=definitionPair(t,p,b,c,from,to);definitionEdges(t,p,b,c,oid,from,to)
  // Same callback selection as a real native bootstrap is nevertheless an
  // actual SQL declaration. Explicit replacement must retain that provenance.
  hostExec(t,p,b,c,definitionCreate(from,to,true))
  if current:=definitionPair(t,p,b,c,from,to);current!=oid {t.Fatal("equivalent replacement changed identity")}
  definitionFunction(t,p,b,c,"td_boot","selection-v1",transformsSelectionPython)
  hostExec(t,p,b,c,"CREATE FUNCTION td_boot_zero() RETURNS hstore LANGUAGE SQL AS $$ SELECT td_boot('a=>7,b=>NULL'::hstore) $$")
  for _,name:=range []string{from,to} {hostExec(t,p,b,c,"ALTER FUNCTION "+name+"(internal) VOLATILE")}
  definitionDiagnostic(t,p,c,definitionCreate(from,to,true),fixture.ControlsReferenceDiagnostic("42P17","transform function must not be volatile"),"execute")
  hostBag(t,p,b,c,"td_boot_zero()",transformsMapRows)
  if b.PG {return}
  hostExec(t,p,b,c,"DROP TRANSFORM FOR hstore LANGUAGE go_host")
  zero:=transformsHeld(t,p,c,"SELECT key,value FROM each(td_boot_zero()) ORDER BY key")
  typed:=transformsHeld(t,p,c,"SELECT key,value FROM each(td_boot($1::hstore)) ORDER BY key")
  hostRows(t,p,b,typed,typed.SQL,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows,"a=>7,b=>NULL")
  expected:="CREATE OR REPLACE TRANSFORM FOR public.hstore LANGUAGE \"td_lang\" (FROM SQL WITH FUNCTION \"public\".\"go_host_from_hstore\"(pg_catalog.internal), TO SQL WITH FUNCTION \"public\".\"go_host_to_hstore\"(pg_catalog.internal));\n"
  stream:=transformsDump(t,p.Ctx,p.Native)
  if strings.Count(string(stream),expected)!=1 {t.Fatalf("explicit bootstrap-equivalent SQL declaration omitted/duplicated:\n%s",stream)}
  assert:=func(target fixture.Backend,e fixture.Executor){
   definitionPair(t,p,target,e,from,to)
   hostBag(t,p,target,e,"td_boot_zero()",transformsMapRows)
   for _,name:=range []string{from,to} {hostRows(t,p,target,e,"SELECT provolatile::text AS v FROM pg_proc WHERE proname=$1",[]string{"v"},[]string{"TEXT"},[][]any{{"v"}},name)}
   hostRows(t,p,target,e,"SELECT count(*) AS n FROM pg_transform t JOIN pg_language l ON l.oid=t.trflang WHERE l.lanname='go_host' AND t.trftype='hstore'::regtype",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}})
  }
  for range 2 {
   runtime:=new(transformRuntime);db,target:=definitionOpen(t,runtime,"")
   if err:=stuntgres.Restore(p.Ctx,db,bytes.NewReader(stream));err!=nil {t.Fatal(err)}
   assert(target,target.DB)
   if runtime.body.Load()==0 {t.Fatal("equivalent replay used no destination program")}
   replay:=transformsDump(t,p.Ctx,db)
   if strings.Count(string(replay),expected)!=1 {t.Fatal("replay discarded actual SQL provenance")}
  }
  if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil {t.Fatal(err)}
  if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(stream));err!=nil {t.Fatal(err)}
  assert(b,c)
  hostRows(t,p,b,zero,zero.SQL,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows)
  rows,err:=typed.QueryContext(p.Ctx,typed.SQL,"a=>7,b=>NULL");if rows!=nil {for rows.Next(){};if err==nil {err=rows.Err()};rows.Close()}
  if diagnostic:=fixture.ExtractDiagnostic(t,err);diagnostic.Code!="0A000" {t.Fatalf("old typed generation was not fenced: %v",err)}
  if strings.Count(string(transformsDump(t,p.Ctx,p.Native)),expected)!=1 {t.Fatal("Reset/Restore discarded explicit SQL provenance")}
 })
}
