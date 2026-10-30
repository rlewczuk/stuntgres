package reference_test

import (
 "context"
 "database/sql"
 "testing"

 "stuntgres/reference/fixture"
)

func TestReferenceRoutineTransformDefinitionCancellation(t *testing.T) {
 definitionScenarioRuntime(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *transformRuntime) {
  definitionAnnotate(t,p,b,c)
  hostExec(t,p,b,c,definitionCreate("td_a_from","td_b_to",false))
  python:="assert isinstance(v,dict)\nv['a']=str(int(v['a'])+1)\nv['c']='3'\nplpy.execute('SELECT pg_advisory_xact_lock(638127)')\nreturn v"
  definitionFunction(t,p,b,c,"tr_cancel","wait-v1",python)
  hostExec(t,p,b,c,"CREATE TABLE tr_atomic_data(v integer)")
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO tr_atomic_data VALUES (1)",1))
  hostExec(t,p,b,c,"CREATE FUNCTION tr_cancel_prefix() RETURNS hstore LANGUAGE plpgsql AS $$ BEGIN INSERT INTO tr_atomic_data VALUES (2); RETURN tr_cancel('a=>7,b=>NULL'::hstore); END $$")
  q:="SELECT tr_cancel_prefix()"
  held:=transformsHeld(t,p,c,q)
  for _,executor:=range []fixture.Executor{c,held} {
   if b.PG {
    // Reuse the actual server admission/cancel/join observer and complete
    // diagnostic expected for these exact named Python/PL wrapper sources.
    transformsPGCancel(t,p,b,c,executor,q)
   } else {
    hostCancelAdmitted(t,p,&r.hostNativeRuntime,func(ctx context.Context)error {
     rows,err:=executor.QueryContext(ctx,q); if rows!=nil { rows.Close() }; return err
    })
   }
   hostRows(t,p,b,c,"SELECT v FROM tr_atomic_data ORDER BY v",[]string{"v"},[]string{"INT4"},[][]any{{int64(1)}})
   oid:=definitionPair(t,p,b,c,"td_a_from","td_b_to"); definitionEdges(t,p,b,c,oid,"td_a_from","td_b_to")
  }
  // Recover through this SAME held wrapper after cancellation; do not replace
  // its statement handle, and still run real conversions in both directions.
  hostExec(t,p,b,c,"CREATE OR REPLACE FUNCTION tr_cancel(v hstore) RETURNS hstore LANGUAGE td_lang TRANSFORM FOR TYPE hstore AS "+transformsLiteral(transformsSource(b,"increment-v1",hostIncrementPython)))
  rows,err:=held.QueryContext(p.Ctx,q); if err!=nil { t.Fatal(err) }
  if !rows.Next() { rows.Close(); t.Fatal("missing canceled wrapper recovery row") }
  var value string; if err=rows.Scan(&value); err!=nil { rows.Close(); t.Fatal(err) }
  if rows.Next() { t.Error("extra recovery row") }; if err=rows.Err(); err!=nil { t.Error(err) }; rows.Close()
  hostBag(t,p,b,c,"$1::hstore",transformsMapRows,value)
  hostRows(t,p,b,c,"SELECT v FROM tr_atomic_data ORDER BY v",[]string{"v"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)}})
 })
}
