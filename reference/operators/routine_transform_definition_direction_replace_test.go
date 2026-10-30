package reference_test

import (
 "database/sql"
 "testing"

 "stuntgres/reference/fixture"
)

func TestReferenceRoutineTransformDefinitionDirectionReplacement(t *testing.T) {
 definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  definitionAnnotate(t,p,b,c)
  hostExec(t,p,b,c,definitionCreate("td_a_from","td_b_to",false))
  oid:=definitionPair(t,p,b,c,"td_a_from","td_b_to")
  python:="value=args[0]\nif value is None:\n    return None\nif isinstance(value,str):\n    import json\n    query=plpy.prepare('SELECT hstore_to_json($1::hstore)::text AS j',['text'])\n    value=json.loads(plpy.execute(query,[value])[0]['j'])\n    value['mode']='entries'\nassert isinstance(value,dict)\nvalue['a']=str(int(value.get('a','0'))+1)\nvalue['c']='3'\nreturn value"
  definitionFunction(t,p,b,c,"td_mode","definition-mode",python)
  q:="SELECT key,value FROM each(td_mode($1::hstore)) ORDER BY key"
  held:=transformsHeld(t,p,c,q)
  hostRows(t,p,b,held,q,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows,"a=>7,b=>NULL")
  // Clear FROM on the SAME routine/held handle. The actual language default
  // is entries natively and a string on PG; neither is assumed to be a map.
  hostExec(t,p,b,c,definitionCreate("","td_b_to",true))
  if current:=definitionPair(t,p,b,c,"","td_b_to");current!=oid {t.Fatal("direction omission changed pair identity")}
  definitionEdges(t,p,b,c,oid,"","td_b_to")
  expected:=[][]any{{"a","8"},{"b",nil},{"c","3"},{"mode","entries"}}
  if b.PG {
   // Released PL/Python retains its cached converter until pg_proc changes.
   // Assert that real cache separately, then replace the IDENTICAL declaration.
   hostRows(t,p,b,held,q,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows,"a=>7,b=>NULL")
   hostExec(t,p,b,c,"CREATE OR REPLACE FUNCTION td_mode(v hstore) RETURNS hstore LANGUAGE td_lang TRANSFORM FOR TYPE hstore AS "+transformsLiteral(python))
  }
  hostRows(t,p,b,held,q,[]string{"key","value"},[]string{"TEXT","TEXT"},expected,"a=>7,b=>NULL")
  hostExec(t,p,b,c,definitionCreate("td_a_from","td_b_to",true))
  if current:=definitionPair(t,p,b,c,"td_a_from","td_b_to");current!=oid {t.Fatal("direction restoration changed pair identity")}
  definitionEdges(t,p,b,c,oid,"td_a_from","td_b_to")
  if b.PG {
   // Restoring FROM likewise leaves the cached default until the SAME source
   // declaration is replaced. Native must refresh without either replacement.
   hostRows(t,p,b,held,q,[]string{"key","value"},[]string{"TEXT","TEXT"},expected,"a=>7,b=>NULL")
   hostExec(t,p,b,c,"CREATE OR REPLACE FUNCTION td_mode(v hstore) RETURNS hstore LANGUAGE td_lang TRANSFORM FOR TYPE hstore AS "+transformsLiteral(python))
  }
  hostRows(t,p,b,held,q,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows,"a=>7,b=>NULL")
  if b.PG {return}
  // The native entries TO default is genuine and intentionally incompatible
  // with a map result. Use an actual map-to-entries program, not a fallback or
  // weakened assertion, when clearing TO on a second SAME held consumer.
  hostExec(t,p,b,c,"DROP FUNCTION td_mode(hstore)")
  hostExec(t,p,b,c,"ALTER FUNCTION go_host_default_to(internal) IMMUTABLE")
  hostExec(t,p,b,c,definitionCreate("td_a_from","go_host_default_to",true))
  definitionFunction(t,p,b,c,"td_mode_from","definition-from","")
  fromQ:="SELECT key,value FROM each(td_mode_from($1::hstore)) ORDER BY key"
  fromHeld:=transformsHeld(t,p,c,fromQ)
  original:=[][]any{{"a","7"},{"b",nil}}
  hostRows(t,p,b,fromHeld,fromQ,[]string{"key","value"},[]string{"TEXT","TEXT"},original,"a=>7,b=>NULL")
  hostExec(t,p,b,c,definitionCreate("td_a_from","",true))
  if current:=definitionPair(t,p,b,c,"td_a_from","");current!=oid {t.Fatal("TO omission changed pair identity")}
  definitionEdges(t,p,b,c,oid,"td_a_from","")
  hostRows(t,p,b,fromHeld,fromQ,[]string{"key","value"},[]string{"TEXT","TEXT"},original,"a=>7,b=>NULL")
 })
}
