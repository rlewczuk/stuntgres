package reference_test

import (
 "database/sql"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceRoutineTransformsConversion(t *testing.T) {
 transformsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,_ *transformRuntime){
  for _,selected:=range []bool{false,true}{
   name,list,want:="tr_default","",transformsDefaultRows
   if selected{name,list,want="tr_selected","FOR TYPE hstore",transformsMapRows}
   transformsFunction(t,p,b,c,name,"selection-v1",transformsSelectionPython,list,"")
   hostBag(t,p,b,c,name+"($1::hstore)",want,"a=>7,b=>NULL")
   q:="SELECT key,value FROM each("+name+"($1::hstore)) ORDER BY key"
   held:=transformsHeld(t,p,c,q)
   hostRows(t,p,b,held,q,[]string{"key","value"},[]string{"TEXT","TEXT"},want,"a=>7,b=>NULL")
   hostMetadata(t,p,b,c,"SELECT "+name+"('a=>7'::hstore) AS v")
   hostMetadata(t,p,b,c,"SELECT "+name+"('a=>7'::hstore) AS v WHERE false")
  }
  hostExec(t,p,b,c,"CREATE TABLE tr_input(v hstore)")
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO tr_input VALUES ('a=>7,b=>NULL')",1))
  hostBag(t,p,b,c,"tr_selected((SELECT v FROM tr_input))",transformsMapRows)
  hostBag(t,p,b,c,"(SELECT v FROM tr_input)",[][]any{{"a","7"},{"b",nil}})
  hostExec(t,p,b,c,"CREATE FUNCTION tr_emit() RETURNS hstore LANGUAGE "+hostRuntimeLanguage(b)+" TRANSFORM FOR TYPE hstore AS "+transformsLiteral(transformsSource(b,"emit-v1","return {'a':1,'b':'boo','c':None}")))
  hostBag(t,p,b,c,"tr_emit()",[][]any{{"a","1"},{"b","boo"},{"c",nil}})
  hostExec(t,p,b,c,"CREATE FUNCTION tr_count(v hstore) RETURNS integer LANGUAGE "+hostRuntimeLanguage(b)+" TRANSFORM FOR TYPE hstore AS "+transformsLiteral(transformsSource(b,"count-v1","assert isinstance(v,dict)\nreturn len(v)")))
  countSQL:="SELECT tr_count($1::hstore) AS n"
  countHeld:=transformsHeld(t,p,c,countSQL)
  for _,exec:=range []fixture.Executor{c,countHeld}{
   hostRows(t,p,b,exec,countSQL,[]string{"n"},[]string{"INT4"},[][]any{{int64(2)}},"aa=>bb,cc=>NULL")
  }
 })
}

func transformsProcedure(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,name,formals,python,list string){
 t.Helper()
 hostExec(t,p,b,c,"CREATE OR REPLACE PROCEDURE "+name+"("+formals+") LANGUAGE "+hostRuntimeLanguage(b)+" TRANSFORM "+list+" AS "+transformsLiteral(transformsSource(b,"procedure-v1",python)))
}
func transformsCall(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor,q,name string,want [][]any,args ...any){
 t.Helper()
 types:=[]string{"HSTORE"};if b.PG{types=[]string{""}}
 hostRows(t,p,b,c,q,[]string{name},types,want,args...)
}
func TestReferenceRoutineTransformsProcedure(t *testing.T){
 transformsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,_ *transformRuntime){
  python:="if v is None:\n    return (None,)\nassert isinstance(v,dict)\nv['a']=str(int(v['a'])+1)\nv['c']='3'\nreturn (v,)"
  // Hstore's SQL output order is not portable. Test the sole CALL member by
  // parsing it again through each(), while independently checking row metadata.
  transformsProcedure(t,p,b,c,"tr_call","INOUT v hstore",python,"FOR TYPE hstore")
  q:="CALL tr_call($1::hstore)"
  held:=transformsHeld(t,p,c,q)
  for _,exec:=range []fixture.Executor{c,held}{
   rows,err:=exec.QueryContext(p.Ctx,q,"a=>7,b=>NULL")
   if err!=nil{t.Fatal(err)}
   cols,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)}
   typ:="HSTORE";if b.PG{typ=""}
   if len(cols)!=1||cols[0].Name()!="v"||cols[0].DatabaseTypeName()!=typ{t.Fatalf("CALL columns=%v",cols)}
   if !rows.Next(){t.Fatal("missing CALL output")}
   var raw string;if err=rows.Scan(&raw);err!=nil{t.Fatal(err)}
   if rows.Next(){t.Fatal("extra CALL output")};if err=rows.Err();err!=nil{t.Fatal(err)};rows.Close()
   hostBag(t,p,b,c,"$1::hstore",transformsMapRows,raw)
  }
  transformsCall(t,p,b,held,q,"v",[][]any{{nil}},nil)
  transformsProcedure(t,p,b,c,"tr_out","v hstore, OUT answer hstore",python,"FOR TYPE hstore")
  out:=transformsHeld(t,p,c,"CALL tr_out($1::hstore,NULL)")
  transformsCall(t,p,b,out,"CALL tr_out($1::hstore,NULL)","answer",[][]any{{nil}},nil)
  rows,err:=c.QueryContext(p.Ctx,"CALL tr_out('a=>7,b=>NULL'::hstore,NULL)");if err!=nil{t.Fatal(err)}
  if !rows.Next(){t.Fatal("missing OUT row")};var raw string;if err=rows.Scan(&raw);err!=nil{t.Fatal(err)};if rows.Next(){t.Fatal("extra OUT row")};if err=rows.Err();err!=nil{t.Fatal(err)};rows.Close()
  hostBag(t,p,b,c,"$1::hstore",transformsMapRows,raw)
  transformsAssociation(t,p,b,c,"tr_call",1)
  var oid int64
  if err:=c.QueryRowContext(p.Ctx,"SELECT oid::bigint FROM pg_proc WHERE proname='tr_call'").Scan(&oid);err!=nil{t.Fatal(err)}
  // Python's outer output record is separate from the inner hstore map.
  selectionPython:="def compute(v):\n    "+strings.ReplaceAll(transformsSelectionPython,"\n","\n    ")+"\nreturn (compute(v),)"
  for _,selected:=range []bool{false,true}{
   clause:="";want:=transformsDefaultRows;n:=int64(0)
   if selected{clause=" TRANSFORM FOR TYPE hstore";want=transformsMapRows;n=1}
   hostExec(t,p,b,c,"CREATE OR REPLACE PROCEDURE tr_call(INOUT v hstore) LANGUAGE "+hostRuntimeLanguage(b)+clause+" AS "+transformsLiteral(transformsSource(b,"selection-v1",selectionPython)))
   rows,err:=held.QueryContext(p.Ctx,q,"a=>7,b=>NULL");if err!=nil{t.Fatal(err)}
   if !rows.Next(){t.Fatal("missing replaced CALL output")};var raw string;if err=rows.Scan(&raw);err!=nil{t.Fatal(err)};if rows.Next(){t.Fatal("extra replaced CALL output")};if err=rows.Err();err!=nil{t.Fatal(err)};rows.Close()
   hostBag(t,p,b,c,"$1::hstore",want,raw)
   transformsAssociation(t,p,b,c,"tr_call",n)
   hostRows(t,p,b,c,"SELECT oid::bigint AS id FROM pg_proc WHERE proname='tr_call'",[]string{"id"},[]string{"INT8"},[][]any{{oid}})
  }
 })
}

func TestReferenceRoutineTransformsDirections(t *testing.T){
 transformsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,_ *transformRuntime){
  for _,direction:=range []string{"from","to"}{
   lang:="tr_"+direction
   defer routineLanguagesCleanup(t,c,"DROP LANGUAGE IF EXISTS "+lang+" CASCADE")()
   handler:="go_host_"+direction+"_call_handler";if b.PG{handler="plpython3_call_handler"}
   hostExec(t,p,b,c,"CREATE LANGUAGE "+lang+" HANDLER "+handler)
   python:="assert isinstance(v,dict)\nreturn '\"a\"=>\"9\",\"b\"=>NULL'"
   if direction=="to"{python="assert isinstance(v,str)\nreturn {'a':'9','b':None}"}
   if b.PG{
    clause:="FROM SQL WITH FUNCTION hstore_to_plpython3(internal)"
    if direction=="to"{clause="TO SQL WITH FUNCTION plpython3_to_hstore(internal)"}
    hostExec(t,p,b,c,"CREATE TRANSFORM FOR hstore LANGUAGE "+lang+" ("+clause+")")
   }
   name:="tr_direction_"+direction
   hostExec(t,p,b,c,"CREATE FUNCTION "+name+"(v hstore) RETURNS hstore LANGUAGE "+lang+" TRANSFORM FOR TYPE hstore AS "+transformsLiteral(transformsSource(b,"direction-v1",python)))
   hostBag(t,p,b,c,name+"($1::hstore)",[][]any{{"a","9"},{"b",nil}},"a=>7,b=>NULL")
   q:="SELECT key,value FROM each("+name+"($1::hstore)) ORDER BY key"
   held:=transformsHeld(t,p,c,q)
   hostRows(t,p,b,held,q,[]string{"key","value"},[]string{"TEXT","TEXT"},[][]any{{"a","9"},{"b",nil}},"a=>7,b=>NULL")
   hostRows(t,p,b,c,"SELECT (trffromsql=0) AS f,(trftosql=0) AS t FROM pg_transform JOIN pg_language ON trflang=pg_language.oid WHERE lanname=$1",[]string{"f","t"},[]string{"BOOL","BOOL"},[][]any{{direction=="to",direction=="from"}},lang)
  }
  if !b.PG{hostNativeError(t,p.Ctx,c,"CREATE FUNCTION tr_no_default(v hstore) RETURNS hstore LANGUAGE go_host_unavailable TRANSFORM FOR TYPE hstore AS 'direction-v1'","unavailable host TO SQL direction")}
 })
}

func TestReferenceRoutineTransformsNulls(t *testing.T){
 transformsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *transformRuntime){
  transformsFunction(t,p,b,c,"tr_strict","selection-v1",transformsSelectionPython,"FOR TYPE hstore","STRICT")
  beforeFrom,beforeBody,beforeTo:=r.from.Load(),r.body.Load(),r.to.Load()
  hostRows(t,p,b,c,"SELECT tr_strict(NULL::hstore) IS NULL AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}})
  if !b.PG&&(r.from.Load()!=beforeFrom||r.body.Load()!=beforeBody||r.to.Load()!=beforeTo){t.Fatal("STRICT NULL invoked host")}
  transformsFunction(t,p,b,c,"tr_null","selection-v1",transformsSelectionPython,"FOR TYPE hstore","CALLED ON NULL INPUT")
  hostBag(t,p,b,c,"tr_null(NULL::hstore)",[][]any{{"n","handled"}})
  transformsFunction(t,p,b,c,"tr_copy","copy-v1","assert isinstance(v,dict)\nreturn v","FOR TYPE hstore","")
  hostBag(t,p,b,c,"tr_copy(''::hstore)",[][]any{})
  hostBag(t,p,b,c,"tr_copy('a=>NULL,b=>\"NULL\",c=>\"\"'::hstore)",[][]any{{"a",nil},{"b","NULL"},{"c",""}})
  hostRows(t,p,b,c,"SELECT tr_copy(''::hstore) IS NULL AS n,exist(tr_copy('a=>NULL'::hstore),'a') AS present,exist(tr_copy('a=>NULL'::hstore),'missing') AS absent",[]string{"n","present","absent"},[]string{"BOOL","BOOL","BOOL"},[][]any{{false,true,false}})
 })
}
