package reference_test

import (
 "database/sql"
 "reflect"
 "testing"

 "stuntgres/reference/fixture"
)

func TestReferenceRoutineTransformDefinitionCatalog(t *testing.T) {
 definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  definitionAnnotate(t,p,b,c)
  hostExec(t,p,b,c,definitionCreate("td_a_from","td_b_to",false))
  oid:=definitionPair(t,p,b,c,"td_a_from","td_b_to")
  definitionEdges(t,p,b,c,oid,"td_a_from","td_b_to")
  hostRows(t,p,b,c,"SELECT trftype='hstore'::regtype AS typ,trflang=(SELECT oid FROM pg_language WHERE lanname='td_lang') AS lang FROM pg_transform WHERE oid=$1::oid",[]string{"typ","lang"},[]string{"BOOL","BOOL"},[][]any{{true,true}},oid)
  // Transform has no independent owner/ACL, and aliases are not extension
  // members merely because their callbacks convert an extension's type.
  hostRows(t,p,b,c,"SELECT count(*) AS n FROM pg_depend WHERE classid='pg_transform'::regclass AND objid=$1::oid AND deptype='e'",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}},oid)
 })
}

func TestReferenceRoutineTransformDefinitionConversion(t *testing.T) {
 definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  definitionAnnotate(t,p,b,c)
  hostExec(t,p,b,c,definitionCreate("td_a_from","td_b_to",false))
  definitionFunction(t,p,b,c,"td_mutate","selection-v1",transformsSelectionPython)
  definitionFunction(t,p,b,c,"td_copy","copy-v1","assert isinstance(v,dict)\nreturn v")
  hostExec(t,p,b,c,"CREATE FUNCTION td_count(v hstore) RETURNS integer LANGUAGE td_lang TRANSFORM FOR TYPE hstore AS "+transformsLiteral(transformsSource(b,"count-v1","assert isinstance(v,dict)\nreturn len(v)")))
  hostExec(t,p,b,c,"CREATE FUNCTION td_emit() RETURNS hstore LANGUAGE td_lang TRANSFORM FOR TYPE hstore AS "+transformsLiteral(transformsSource(b,"emit-v1","return {'a':1,'b':'boo','c':None}")))
  for _,prepared:=range []bool{false,true} {
   step:=fixture.Select("SELECT td_count($1::hstore) AS n",[]string{"n"},[]string{"INT4"},[][]any{{int64(2)}},"aa=>bb,cc=>NULL")
   step.CheckMetadata=func(t *testing.T,rows *sql.Rows) {
    cols,err:=rows.ColumnTypes(); if err!=nil { t.Fatal(err) }
    scan:=reflect.TypeFor[int64](); if b.PG { scan=reflect.TypeFor[int32]() }
    if len(cols)!=1 || cols[0].ScanType()!=scan { t.Fatalf("count ScanType=%v want %v",cols,scan) }
    if nullable,known:=cols[0].Nullable(); nullable || known { t.Errorf("computed nullability=%v/%v",nullable,known) }
   }
   if prepared { step=fixture.Prepared(step) }
   fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
  }
  q:="SELECT key,value FROM each(td_mutate($1::hstore)) ORDER BY key"
  held:=transformsHeld(t,p,c,q)
  for _,executor:=range []fixture.Executor{c,held} {
   hostRows(t,p,b,executor,q,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows,"a=>7,b=>NULL")
  }
  hostBag(t,p,b,c,"td_emit()",[][]any{{"a","1"},{"b","boo"},{"c",nil}})
  hostBag(t,p,b,c,"td_copy(''::hstore)",[][]any{})
  hostBag(t,p,b,c,"td_copy('a=>NULL,b=>\"NULL\",c=>\"\"'::hstore)",[][]any{{"a",nil},{"b","NULL"},{"c",""}})
  hostRows(t,p,b,c,"SELECT td_copy(''::hstore) IS NULL AS empty,exist(td_copy('a=>NULL'::hstore),'a') AS present,exist(td_copy('a=>NULL'::hstore),'missing') AS missing",[]string{"empty","present","missing"},[]string{"BOOL","BOOL","BOOL"},[][]any{{false,true,false}})
  hostBag(t,p,b,c,"td_mutate(NULL::hstore)",[][]any{{"n","handled"}})
  hostExec(t,p,b,c,"CREATE TABLE td_input(v hstore)")
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO td_input VALUES ('a=>7,b=>NULL')",1))
  hostBag(t,p,b,c,"td_mutate((SELECT v FROM td_input))",transformsMapRows)
  hostBag(t,p,b,c,"(SELECT v FROM td_input)",[][]any{{"a","7"},{"b",nil}})
  hostMetadata(t,p,b,c,"SELECT td_mutate('a=>7'::hstore) AS v")
  hostMetadata(t,p,b,c,"SELECT td_mutate('a=>7'::hstore) AS v WHERE false")
 })
}

func TestReferenceRoutineTransformDefinitionDirections(t *testing.T) {
 for _,direction:=range []string{"from","to"} {
  t.Run(direction,func(t *testing.T) {
   definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
    definitionAnnotate(t,p,b,c)
    from,to:="td_a_from","td_b_to"
    python:=""
    if direction=="from" {
     to=""
     // The genuine default TO consumes entries natively, strings in Python.
     python="import json\nassert isinstance(v,dict)\nreturn ','.join(json.dumps(k)+'=>'+('NULL' if v[k] is None else json.dumps(v[k])) for k in sorted(v))"
    } else {
     from=""
     python="assert isinstance(v,str)\nrows=plpy.execute('SELECT key,value FROM each('+plpy.quote_literal(v)+'::hstore)')\nreturn {row['key']:row['value'] for row in rows}"
    }
    hostExec(t,p,b,c,definitionCreate(from,to,false))
    oid:=definitionPair(t,p,b,c,from,to); definitionEdges(t,p,b,c,oid,from,to)
    definitionFunction(t,p,b,c,"td_direction","definition-"+direction,python)
    q:="SELECT key,value FROM each(td_direction($1::hstore)) ORDER BY key"
    held:=transformsHeld(t,p,c,q)
    for _,executor:=range []fixture.Executor{c,held} {
     hostRows(t,p,b,executor,q,[]string{"key","value"},[]string{"TEXT","TEXT"},[][]any{{"a","7"},{"b",nil}},"a=>7,b=>NULL")
     hostRows(t,p,b,executor,q,[]string{"key","value"},[]string{"TEXT","TEXT"},[][]any{{"a","2"},{"extra","value"}},"a=>2,extra=>value")
    }
   })
  })
 }
}

func TestReferenceRoutineTransformDefinitionReplacement(t *testing.T) {
 definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  definitionAnnotate(t,p,b,c)
  hostExec(t,p,b,c,definitionCreate("td_a_from","td_a_to",false))
  oid:=definitionPair(t,p,b,c,"td_a_from","td_a_to")
  definitionFunction(t,p,b,c,"td_retained","selection-v1",transformsSelectionPython)
  // Common associations are language/type pairs, not type-only annotations.
  // Install and replace each actual SQL/PL pair independently of td_lang.
  for _,language:=range []string{"sql","plpgsql"} {
   hostExec(t,p,b,c,"CREATE TRANSFORM FOR hstore LANGUAGE "+language+" (FROM SQL WITH FUNCTION td_a_from(internal), TO SQL WITH FUNCTION td_a_to(internal))")
  }
  hostExec(t,p,b,c,"CREATE FUNCTION td_unused() RETURNS integer LANGUAGE SQL TRANSFORM FOR TYPE hstore, FOR TYPE hstore AS 'SELECT 1'")
  hostExec(t,p,b,c,"CREATE FUNCTION td_unused_pl() RETURNS integer LANGUAGE plpgsql TRANSFORM FOR TYPE hstore, FOR TYPE hstore AS $$ BEGIN RETURN 1; END $$")
  sqlHeld:=transformsHeld(t,p,c,"SELECT td_unused() AS n")
  plHeld:=transformsHeld(t,p,c,"SELECT td_unused_pl() AS n")
  q:="SELECT key,value FROM each(td_retained($1::hstore)) ORDER BY key"
  held:=transformsHeld(t,p,c,q)
  hostRows(t,p,b,held,q,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows,"a=>7,b=>NULL")
  hostExec(t,p,b,c,"CREATE VIEW td_view AS SELECT td_retained('a=>7,b=>NULL'::hstore) AS v")
  hostExec(t,p,b,c,definitionCreate("td_b_from","td_b_to",true))
  for _,language:=range []string{"sql","plpgsql"} {
   hostExec(t,p,b,c,"CREATE OR REPLACE TRANSFORM FOR hstore LANGUAGE "+language+" (FROM SQL WITH FUNCTION td_b_from(internal), TO SQL WITH FUNCTION td_b_to(internal))")
  }
  if next:=definitionPair(t,p,b,c,"td_b_from","td_b_to"); next!=oid { t.Errorf("replacement changed OID %d -> %d",oid,next) }
  definitionEdges(t,p,b,c,oid,"td_b_from","td_b_to")
  for _,executor:=range []fixture.Executor{c,held} { hostRows(t,p,b,executor,q,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows,"a=>7,b=>NULL") }
  hostBag(t,p,b,c,"(SELECT v FROM td_view)",transformsMapRows)
  hostRows(t,p,b,c,"SELECT td_unused() AS n",[]string{"n"},[]string{"INT4"},[][]any{{int64(1)}})
  transformsAssociation(t,p,b,c,"td_unused",2)
  transformsAssociation(t,p,b,c,"td_unused_pl",2)
  for _,consumer:=range []fixture.HeldStatement{sqlHeld,plHeld} {
   hostRows(t,p,b,consumer,consumer.SQL,[]string{"n"},[]string{"INT4"},[][]any{{int64(1)}})
  }
  hostRows(t,p,b,c,"SELECT count(*) AS n FROM pg_depend d JOIN pg_proc p ON p.oid=d.objid JOIN pg_transform t ON t.oid=d.refobjid WHERE d.classid='pg_proc'::regclass AND d.refclassid='pg_transform'::regclass AND d.deptype='n' AND p.proname IN ('td_unused','td_unused_pl') AND p.prolang=t.trflang",[]string{"n"},[]string{"INT8"},[][]any{{int64(2)}})
  definitionDiagnostic(t,p,c,definitionCreate("td_b_from","td_a_from",true),fixture.ControlsReferenceDiagnostic("42P17","return data type of TO SQL function must be the transform data type"),"execute")
  definitionEdges(t,p,b,c,oid,"td_b_from","td_b_to")
  hostRows(t,p,b,held,q,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows,"a=>7,b=>NULL")
  if !b.PG {
   // Authentic changed Go conversion behavior is separate from the real PG
   // C alias witness above: those C symbols do not multiply/project values.
   hostExec(t,p,b,c,definitionCreate("td_scaled_from","td_projected_to",true))
   scaled:=[][]any{{"a","71"},{"b",nil},{"c","3"},{"z","out"}}
   hostRows(t,p,b,held,q,[]string{"key","value"},[]string{"TEXT","TEXT"},scaled,"a=>7,b=>NULL")
   hostBag(t,p,b,c,"(SELECT v FROM td_view)",scaled)
   definitionEdges(t,p,b,c,oid,"td_scaled_from","td_projected_to")
  }
 })
}
