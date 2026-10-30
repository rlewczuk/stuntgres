package reference_test

import (
 "database/sql"
 "testing"

 "stuntgres/reference/fixture"
)

func TestReferenceRoutineTransformDefinitionConsumers(t *testing.T) {
 definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  definitionAnnotate(t,p,b,c)
  hostExec(t,p,b,c,definitionCreate("td_a_from","td_a_to",false))
  definitionFunction(t,p,b,c,"td_consumer","selection-v1",transformsSelectionPython)
  // This program only computes its supplied value. The SQL owner explicitly
  // promises statement stability; registration and failure/cancel programs stay v.
  hostExec(t,p,b,c,"ALTER FUNCTION td_consumer(hstore) STABLE")
  python:="if v is None:\n    return (None,)\nv['a']=str(int(v['a'])+1)\nv['c']='3'\nreturn (v,)"
  hostExec(t,p,b,c,"CREATE PROCEDURE td_call(INOUT v hstore) LANGUAGE td_lang TRANSFORM FOR TYPE hstore AS "+transformsLiteral(transformsSource(b,"procedure-v1",python)))
  hostExec(t,p,b,c,"CREATE PROCEDURE td_out(v hstore, OUT answer hstore) LANGUAGE td_lang TRANSFORM FOR TYPE hstore AS "+transformsLiteral(transformsSource(b,"procedure-v1",python)))
  call:=transformsHeld(t,p,c,"CALL td_call($1::hstore)")
  out:=transformsHeld(t,p,c,"CALL td_out($1::hstore,NULL)")
  hostExec(t,p,b,c,"CREATE VIEW td_consuming_view AS SELECT td_consumer('a=>7,b=>NULL'::hstore) AS v")
  hostExec(t,p,b,c,"CREATE TABLE td_defaults(v integer DEFAULT (td_consumer('a=>7'::hstore)->'a')::integer CHECK (v <= (td_consumer('a=>7'::hstore)->'a')::integer))")
  hostExec(t,p,b,c,"CREATE FUNCTION td_step(s integer,v hstore) RETURNS integer LANGUAGE SQL RETURN s+(td_consumer(v)->'a')::integer")
  hostExec(t,p,b,c,"CREATE AGGREGATE td_sum(hstore) (SFUNC=td_step,STYPE=integer,INITCOND='0')")
  insert:=transformsHeld(t,p,c,"INSERT INTO td_defaults DEFAULT VALUES RETURNING v")
  aggregate:=transformsHeld(t,p,c,"SELECT td_sum(v) AS n FROM (VALUES ('a=>7'::hstore),('a=>2'::hstore)) AS input(v)")
  for _,replace:=range []bool{false,true} {
   want,n,sum:=transformsMapRows,int64(8),int64(11)
   if replace {
    from,to:="td_b_from","td_b_to"
    if !b.PG { from,to="td_scaled_from","td_projected_to"; want=[][]any{{"a","71"},{"b",nil},{"c","3"},{"z","out"}}; n,sum=71,92 }
    hostExec(t,p,b,c,definitionCreate(from,to,true))
   }
   for _,held:=range []fixture.HeldStatement{call,out} {
    rows,err:=held.QueryContext(p.Ctx,held.SQL,"a=>7,b=>NULL"); if err!=nil { t.Fatal(err) }
    cols,err:=rows.ColumnTypes(); if err!=nil { rows.Close(); t.Fatal(err) }
    name:="v"; if held.SQL==out.SQL { name="answer" }
    typ:="HSTORE"; if b.PG { typ="" }
    if len(cols)!=1 || cols[0].Name()!=name || cols[0].DatabaseTypeName()!=typ { t.Errorf("CALL metadata=%v",cols) }
    if !rows.Next() { rows.Close(); t.Fatal("missing CALL row") }
    var value string; if err=rows.Scan(&value); err!=nil { rows.Close(); t.Fatal(err) }
    if rows.Next() { t.Error("extra CALL output row") }; if err=rows.Err(); err!=nil { t.Error(err) }; rows.Close()
    hostBag(t,p,b,c,"$1::hstore",want,value)
   }
   hostBag(t,p,b,c,"(SELECT v FROM td_consuming_view)",want)
   hostRows(t,p,b,insert,insert.SQL,[]string{"v"},[]string{"INT4"},[][]any{{n}})
   hostRows(t,p,b,aggregate,aggregate.SQL,[]string{"n"},[]string{"INT4"},[][]any{{sum}})
  }
  // Real closure: CASCADE drops dependent routines/view/aggregate plus the
  // referencing default and CHECK, but does not delete their table or data.
  hostExec(t,p,b,c,"DROP TRANSFORM FOR hstore LANGUAGE td_lang CASCADE")
  hostRows(t,p,b,c,"SELECT count(*) AS n FROM pg_proc WHERE proname IN ('td_consumer','td_call','td_out','td_step','td_sum')",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}})
  hostRows(t,p,b,c,"SELECT count(*) AS n FROM pg_attrdef d JOIN pg_class c ON c.oid=d.adrelid WHERE c.relname='td_defaults'",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}})
  hostRows(t,p,b,c,"SELECT count(*) AS n FROM pg_constraint x JOIN pg_class c ON c.oid=x.conrelid WHERE c.relname='td_defaults'",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}})
  want:=[][]any{{int64(8)},{int64(8)}}; if !b.PG { want=[][]any{{int64(8)},{int64(71)}} }
  hostRows(t,p,b,c,"SELECT v FROM td_defaults ORDER BY v",[]string{"v"},[]string{"INT4"},want)
 })
}
