package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceRoutineTransformsReassociation(t *testing.T){
 transformsScenario(t,transformsReassociation)
}
func transformsReassociation(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,_ *transformRuntime){
 transformsFunction(t,p,b,c,"tr_replace","selection-v1",transformsSelectionPython,"","")
 var oid int64
 if err:=c.QueryRowContext(p.Ctx,"SELECT oid::bigint FROM pg_proc WHERE proname='tr_replace'").Scan(&oid);err!=nil{t.Fatal(err)}
 hostExec(t,p,b,c,"REVOKE EXECUTE ON FUNCTION tr_replace(hstore) FROM PUBLIC")
 q:="SELECT key,value FROM each(tr_replace($1::hstore)) ORDER BY key"
 held:=transformsHeld(t,p,c,q)
 for _,list:=range []string{"","FOR TYPE hstore","","FOR TYPE hstore"}{
  transformsFunction(t,p,b,c,"tr_replace","selection-v1",transformsSelectionPython,list,"")
  n:=int64(0);want:=transformsDefaultRows
  if list!=""{n=1;want=transformsMapRows}
  transformsAssociation(t,p,b,c,"tr_replace",n)
  transformsPairEdges(t,p,b,c,"tr_replace",n)
  hostRows(t,p,b,c,"SELECT oid::bigint AS id,proacl IS NOT NULL AS acl FROM pg_proc WHERE proname='tr_replace'",[]string{"id","acl"},[]string{"INT8","BOOL"},[][]any{{oid,true}})
  hostRows(t,p,b,held,q,[]string{"key","value"},[]string{"TEXT","TEXT"},want,"a=>7,b=>NULL")
  hostBag(t,p,b,c,"tr_replace('a=>7,b=>NULL'::hstore)",want)
 }
 bad:="CREATE OR REPLACE FUNCTION tr_replace(v hstore) RETURNS hstore LANGUAGE "+hostRuntimeLanguage(b)+" TRANSFORM FOR TYPE integer AS "+transformsLiteral(transformsSource(b,"selection-v1",transformsSelectionPython))
 transformsDiagnostic(t,p,c,bad,"42704","transform for type integer language \""+hostRuntimeLanguage(b)+"\" does not exist")
 transformsAssociation(t,p,b,c,"tr_replace",1)
 transformsPairEdges(t,p,b,c,"tr_replace",1)
 hostRows(t,p,b,held,q,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows,"a=>7,b=>NULL")
 hostRows(t,p,b,c,"SELECT oid::bigint AS id,proacl IS NOT NULL AS acl FROM pg_proc WHERE proname='tr_replace'",[]string{"id","acl"},[]string{"INT8","BOOL"},[][]any{{oid,true}})
}

func TestReferenceRoutineTransformsDependencies(t *testing.T){
 transformsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,_ *transformRuntime){
  transformsFunction(t,p,b,c,"tr_dependency","increment-v1",hostIncrementPython,"FOR TYPE hstore, FOR TYPE hstore","")
  transformsPairEdges(t,p,b,c,"tr_dependency",1)
  transformsAssociation(t,p,b,c,"tr_dependency",2)
  hostExec(t,p,b,c,"CREATE FUNCTION tr_unused_dependency() RETURNS integer LANGUAGE "+hostRuntimeLanguage(b)+" TRANSFORM FOR TYPE hstore AS "+transformsLiteral(transformsSource(b,"one-v1","return 1")))
  transformsPairEdges(t,p,b,c,"tr_unused_dependency",1)
  lang:=hostRuntimeLanguage(b)
  // Pair links are relational identities, not allocator-specific OID goldens.
  hostRows(t,p,b,c,"SELECT count(*) AS v FROM pg_depend d JOIN pg_transform t ON d.objid=t.oid JOIN pg_language l ON l.oid=t.trflang WHERE d.classid='pg_transform'::regclass AND d.refclassid='pg_language'::regclass AND d.refobjid=l.oid AND d.deptype='n' AND l.lanname=$1",[]string{"v"},[]string{"INT8"},[][]any{{int64(1)}},lang)
  q:="SELECT key,value FROM each(tr_dependency($1::hstore)) ORDER BY key"
  held:=transformsHeld(t,p,c,q)
  // Isolate a non-extension-owned alias for RESTRICT/CASCADE diagnostics.
  alias:="tr_dependency_language";handler:="go_host_from_call_handler";if b.PG{handler="plpython3_call_handler"}
  defer routineLanguagesCleanup(t,c,"DROP LANGUAGE IF EXISTS "+alias+" CASCADE")()
  hostExec(t,p,b,c,"CREATE LANGUAGE "+alias+" HANDLER "+handler)
  if b.PG{hostExec(t,p,b,c,"CREATE TRANSFORM FOR hstore LANGUAGE "+alias+" (FROM SQL WITH FUNCTION hstore_to_plpython3(internal))")}
  hostExec(t,p,b,c,"CREATE FUNCTION tr_alias_dependency(v hstore) RETURNS hstore LANGUAGE "+alias+" TRANSFORM FOR TYPE hstore AS "+transformsLiteral(transformsSource(b,"direction-v1","return '\"a\"=>\"9\",\"b\"=>NULL'")))
  _,err:=c.ExecContext(p.Ctx,"DROP LANGUAGE "+alias+" RESTRICT")
  if err==nil{t.Fatal("RESTRICT dropped live language")}
  d:=fixture.ExtractDiagnostic(t,err)
  want:=fixture.ControlsReferenceDiagnostic("2BP01","cannot drop language "+alias+" because other objects depend on it")
  want.Detail="transform for hstore language tr_dependency_language depends on language tr_dependency_language\nfunction tr_alias_dependency(hstore) depends on language tr_dependency_language"
  want.Hint="Use DROP ... CASCADE to drop the dependent objects too."
  if d!=want {t.Errorf("RESTRICT diagnostic=%#v; want complete=%#v",d,want)}
  hostExec(t,p,b,c,"DROP LANGUAGE "+alias+" CASCADE")
  hostRows(t,p,b,c,"SELECT count(*) AS v FROM pg_proc WHERE proname='tr_alias_dependency'",[]string{"v"},[]string{"INT8"},[][]any{{int64(0)}})
  hostRows(t,p,b,held,q,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows,"a=>7,b=>NULL")
 })
}

func TestReferenceRoutineTransformsAtomicity(t *testing.T){
 transformsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *transformRuntime){
  transformsFunction(t,p,b,c,"tr_atomic","increment-v1",hostIncrementPython,"FOR TYPE hstore","")
  hostExec(t,p,b,c,"CREATE TABLE tr_atomic_data(v integer)")
  hostExec(t,p,b,c,"CREATE FUNCTION tr_atomic_work() RETURNS hstore LANGUAGE plpgsql AS $$ BEGIN INSERT INTO tr_atomic_data VALUES (1); RETURN tr_atomic('a=>bad'::hstore); END $$")
  q:="SELECT tr_atomic_work()"
  held:=transformsHeld(t,p,c,q)
  for _,exec:=range []fixture.Executor{c,held}{
   rows,err:=exec.QueryContext(p.Ctx,q);if rows!=nil{rows.Close()}
   if err==nil{t.Fatal("expected program conversion failure")}
   if b.PG{
    want:=fixture.ControlsReferenceDiagnostic("38000","ValueError: invalid literal for int() with base 10: 'bad'")
    want.Context="Traceback (most recent call last):\n  PL/Python function \"tr_atomic\", line 2, in <module>\n    v['a'] = str(int(v.get('a', '0')) + 1)\nPL/Python function \"tr_atomic\"\nPL/pgSQL function tr_atomic_work() line 1 at RETURN"
    got:=fixture.ExtractDiagnostic(t,err)
    if got!=want {t.Errorf("program failure=%#v; want complete=%#v",got,want)}
   }
   hostRows(t,p,b,c,"SELECT count(*) AS v FROM tr_atomic_data",[]string{"v"},[]string{"INT8"},[][]any{{int64(0)}})
  }
  transformsFunction(t,p,b,c,"tr_atomic","copy-v1","return v","FOR TYPE hstore","")
  // The SAME prepared modifying caller recovers after replacement.
  rows,err:=held.QueryContext(p.Ctx,q);if err!=nil{t.Fatal(err)};if !rows.Next(){t.Fatal("missing recovered result")};var raw string;if err=rows.Scan(&raw);err!=nil{t.Fatal(err)};if rows.Next(){t.Fatal("extra result")};if err=rows.Err();err!=nil{t.Fatal(err)};rows.Close()
  hostBag(t,p,b,c,"$1::hstore",[][]any{{"a","bad"}},raw)
  hostRows(t,p,b,c,"SELECT count(*) AS v FROM tr_atomic_data",[]string{"v"},[]string{"INT8"},[][]any{{int64(1)}})
  transformsCancellation(t,p,b,c,r)
  if !b.PG {
   // Real native FROM/program/TO failures remain ordinary ABI errors, but
   // still unwind earlier SQL effects under the common statement owner.
   for _,item:=range []struct{input,program,message string}{
    {"a=>7,from_error=>1","copy-v1","host: FROM SQL failed"},
    {"a=>7","fail-v1","host: program failed"},
    {"a=>7,to_error=>1","copy-v1","host: TO SQL failed"},
   }{
    transformsFunction(t,p,b,c,"tr_atomic",item.program,"return v","FOR TYPE hstore","")
    hostExec(t,p,b,c,"CREATE OR REPLACE FUNCTION tr_atomic_work() RETURNS hstore LANGUAGE plpgsql AS $$ BEGIN INSERT INTO tr_atomic_data VALUES (3); RETURN tr_atomic("+transformsLiteral(item.input)+"::hstore); END $$")
    hostNativeError(t,p.Ctx,c,q,item.message)
    hostRows(t,p,b,c,"SELECT v FROM tr_atomic_data ORDER BY v",[]string{"v"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)}})
   }
  }
 })
}

func TestReferenceRoutineTransformsRetained(t *testing.T){
 transformsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *transformRuntime){
  transformsFunction(t,p,b,c,"tr_retained","retain-v1",hostIncrementPython,"FOR TYPE hstore","")
  q:="SELECT key,value FROM each(tr_retained($1::hstore)) ORDER BY key"
  held:=transformsHeld(t,p,c,q)
  rows,err:=c.QueryContext(p.Ctx,q,"a=>7,b=>NULL");if err!=nil{t.Fatal(err)}
  cols,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)}
  if !rows.Next(){t.Fatal("no retained row")}
  var key,value string;if err=rows.Scan(&key,&value);err!=nil{t.Fatal(err)}
  if key!="a"||value!="8"{t.Fatalf("retained=%s/%s",key,value)}
  // Close the cursor before DDL so the single fixture connection remains usable;
  // keep actual driver-returned buffers and ColumnType objects alive.
  rows.Close()
  if !b.PG {
   r.mu.Lock()
   retained:=r.retained
   if retained!=nil {delete(retained,"a");delete(retained,"b")}
   r.mu.Unlock()
   if retained==nil{t.Fatal("compiled program did not retain a real map")}
  }
  transformsFunction(t,p,b,c,"tr_retained","double-v1",hostDoublePython,"FOR TYPE hstore","")
  hostRows(t,p,b,held,q,[]string{"key","value"},[]string{"TEXT","TEXT"},[][]any{{"a","14"},{"b",nil},{"d","second"}},"a=>7,b=>NULL")
  if key!="a"||value!="8"||len(cols)!=2||cols[0].Name()!="key"{t.Fatal("retained output mutated")}
  hostExec(t,p,b,c,"DROP FUNCTION tr_retained(hstore)")
  rows,err=held.QueryContext(p.Ctx,q,"a=>7");if rows!=nil{rows.Close()};if err==nil{t.Fatal("retired prepared routine succeeded")}
  got:=fixture.ExtractDiagnostic(t,err)
  want:=fixture.ControlsReferenceDiagnostic("42883","function tr_retained(hstore) does not exist")
  want.Hint="No function matches the given name and argument types. You might need to add explicit type casts."
  want.Position="28"
  if got!=want {t.Errorf("retired routine=%#v; want complete=%#v",got,want)}
 })
}
