package reference_test

import (
 "bytes"
 "testing"
 "stuntgres"
 "stuntgres/reference/fixture"
)

// Use the existing DROP FUNCTION dependency owner, not downstream DROP
// TRANSFORM syntax. Replaying into a fresh matching runtime must not resurrect
// the retired bootstrap association merely because its provider is registered.
func transformsPairAbsence(t *testing.T,p *fixture.Pair){
 t.Helper()
 source,b:=transformsOpen(t,new(transformRuntime),nil)
 c,err:=b.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer c.Close()
 hostExec(t,p,b,c,"CREATE EXTENSION hstore")
 transformsFunction(t,p,b,c,"tr_surviving_default","selection-v1",transformsSelectionPython,"","")
 hostExec(t,p,b,c,"DROP FUNCTION go_host_from_hstore(internal) CASCADE")
 hostRows(t,p,b,c,"SELECT count(*) AS v FROM pg_transform t JOIN pg_language l ON l.oid=t.trflang WHERE l.lanname='go_host'",[]string{"v"},[]string{"INT8"},[][]any{{int64(0)}})
 hostBag(t,p,b,c,"tr_surviving_default('a=>7,b=>NULL'::hstore)",transformsDefaultRows)
 stream:=transformsDump(t,p.Ctx,source)
 target,destination:=transformsOpen(t,new(transformRuntime),nil)
 if err=stuntgres.Restore(p.Ctx,target,bytes.NewReader(stream));err!=nil{t.Fatal(err)}
 restored,err:=destination.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer restored.Close()
 hostRows(t,p,destination,restored,"SELECT count(*) AS v FROM pg_transform t JOIN pg_language l ON l.oid=t.trflang WHERE l.lanname='go_host'",[]string{"v"},[]string{"INT8"},[][]any{{int64(0)}})
 transformsAssociation(t,p,destination,restored,"tr_surviving_default",0)
 hostBag(t,p,destination,restored,"tr_surviving_default('a=>7,b=>NULL'::hstore)",transformsDefaultRows)
 transformsDiagnostic(t,p,restored,"CREATE FUNCTION tr_retired_pair(v hstore) RETURNS hstore LANGUAGE go_host TRANSFORM FOR TYPE hstore AS 'selection-v1'","42704","transform for type hstore language \"go_host\" does not exist")
 hostRows(t,p,destination,restored,"SELECT count(*) AS v FROM pg_proc WHERE proname='go_host_from_hstore'",[]string{"v"},[]string{"INT8"},[][]any{{int64(0)}})
}
