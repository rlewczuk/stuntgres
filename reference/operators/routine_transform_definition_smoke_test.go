package reference_test

import (
 "bytes"
 "context"
 "database/sql"
 "errors"
 "testing"
 "reflect"

 "stuntgres"
 "stuntgres/reference/fixture"
)

func definitionSmokeConsumers(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
 t.Helper()
 definitionFunction(t,p,b,c,"td_smoke","selection-v1",transformsSelectionPython)
 hostExec(t,p,b,c,"ALTER FUNCTION td_smoke(hstore) STABLE")
 python:="if v is None:\n    return (None,)\nv['a']=str(int(v['a'])+1)\nv['c']='3'\nreturn (v,)"
 hostExec(t,p,b,c,"CREATE PROCEDURE td_smoke_call(INOUT v hstore) LANGUAGE td_lang TRANSFORM FOR TYPE hstore AS "+transformsLiteral(transformsSource(b,"selection-v1",python)))
 for _,q:=range []string{
  "CREATE FUNCTION td_smoke_zero() RETURNS hstore LANGUAGE SQL STABLE RETURN td_smoke('a=>7,b=>NULL'::hstore)",
  "CREATE VIEW td_smoke_view AS SELECT td_smoke_zero() AS v",
  "CREATE TABLE td_smoke_defaults(v hstore DEFAULT td_smoke_zero())",
  "CREATE TABLE td_smoke_checks(v hstore CHECK(td_smoke(v) IS NOT NULL))",
  "CREATE FUNCTION td_smoke_state(s hstore,v hstore) RETURNS hstore LANGUAGE SQL RETURN td_smoke(v)",
  "CREATE AGGREGATE td_smoke_aggregate(hstore)(SFUNC=td_smoke_state,STYPE=hstore)",
 } { hostExec(t,p,b,c,q) }
}

func TestReferenceRoutineTransformDefinitionFeatureSmoke(t *testing.T) {
 definitionScenarioRuntime(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *transformRuntime) {
  definitionAnnotate(t,p,b,c)
  hostExec(t,p,b,c,definitionCreate("td_a_from","td_a_to",false))
  oid:=definitionPair(t,p,b,c,"td_a_from","td_a_to")
  definitionEdges(t,p,b,c,oid,"td_a_from","td_a_to")
  definitionSmokeConsumers(t,p,b,c)
  q:="SELECT key,value FROM each(td_smoke($1::hstore)) ORDER BY key"
  held:=transformsHeld(t,p,c,q)
  hostRows(t,p,b,held,q,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows,"a=>7,b=>NULL")
  hostBag(t,p,b,c,"(SELECT v FROM td_smoke_view)",transformsMapRows)
  call:=func(want [][]any){
   rows,err:=c.QueryContext(p.Ctx,"CALL td_smoke_call('a=>7,b=>NULL'::hstore)");if err!=nil {t.Fatal(err)}
   columns,err:=rows.ColumnTypes();if err!=nil {rows.Close();t.Fatal(err)}
   kind,scan:="HSTORE",reflect.TypeFor[string]();if b.PG {kind,scan="",reflect.TypeFor[any]()}
   if len(columns)!=1 || columns[0].Name()!="v" || columns[0].DatabaseTypeName()!=kind || columns[0].ScanType()!=scan {rows.Close();t.Fatal("INOUT metadata",columns)}
   if nullable,known:=columns[0].Nullable();nullable || known {t.Errorf("computed INOUT nullability=%v/%v",nullable,known)}
   if !rows.Next(){rows.Close();t.Fatal("missing INOUT row")};var text string;if err=rows.Scan(&text);err!=nil{rows.Close();t.Fatal(err)}
   if rows.Next(){t.Error("extra INOUT row")};if err=rows.Err();err!=nil{t.Error(err)};rows.Close()
   hostBag(t,p,b,c,"$1::hstore",want,text)
  }
  call(transformsMapRows)
  hostExec(t,p,b,c,definitionCreate("td_a_from","td_b_to",true))
  if current:=definitionPair(t,p,b,c,"td_a_from","td_b_to");current!=oid {t.Fatal("mixed-ID replacement changed pair identity")}
  definitionEdges(t,p,b,c,oid,"td_a_from","td_b_to")
  hostRows(t,p,b,held,q,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows,"a=>7,b=>NULL")
  want:=transformsMapRows
  if !b.PG {
   hostExec(t,p,b,c,definitionCreate("td_scaled_from","td_projected_to",true))
   definitionPair(t,p,b,c,"td_scaled_from","td_projected_to")
   definitionEdges(t,p,b,c,oid,"td_scaled_from","td_projected_to")
   want=[][]any{{"a","71"},{"b",nil},{"c","3"},{"z","out"}}
   hostRows(t,p,b,held,q,[]string{"key","value"},[]string{"TEXT","TEXT"},want,"a=>7,b=>NULL")
  }
  call(want)
  // Actual wrappers/default/CHECK/aggregate captures share the refreshed owner.
  hostBag(t,p,b,c,"(SELECT v FROM td_smoke_view)",want)
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO td_smoke_defaults DEFAULT VALUES",1))
  hostBag(t,p,b,c,"(SELECT v FROM td_smoke_defaults)",want)
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO td_smoke_checks VALUES ('a=>7,b=>NULL'::hstore)",1))
  hostBag(t,p,b,c,"(SELECT td_smoke_aggregate(v) FROM td_smoke_checks)",want)
  hostExec(t,p,b,c,"CREATE TABLE td_smoke_effects(v integer)")
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO td_smoke_effects VALUES(11)",1))
  hostExec(t,p,b,c,"CREATE FUNCTION td_smoke_prefix(v hstore) RETURNS hstore LANGUAGE plpgsql AS $$ BEGIN INSERT INTO td_smoke_effects VALUES(22); RETURN td_smoke(v); END $$")
  wrapper:=transformsHeld(t,p,c,"SELECT td_smoke_prefix($1::hstore)")
  result,err:=wrapper.QueryContext(p.Ctx,wrapper.SQL,"a=>bad");if result!=nil{result.Close()}
  if b.PG {
   d:=fixture.ControlsReferenceDiagnostic("38000","ValueError: invalid literal for int() with base 10: 'bad'")
   d.Context="Traceback (most recent call last):\n  PL/Python function \"td_smoke\", line 4, in <module>\n    v['a'] = str(int(v.get('a','0'))+1)\nPL/Python function \"td_smoke\"\nPL/pgSQL function td_smoke_prefix(hstore) line 1 at RETURN"
   fixture.AssertDiagnostic(t,err,&d)
  }else {definitionOrdinaryError(t,err,"invalid syntax")}
  hostRows(t,p,b,c,"SELECT v FROM td_smoke_effects",[]string{"v"},[]string{"INT4"},[][]any{{int64(11)}})
  // The exact dependency list is already independently compared in Dependencies;
  // this larger live closure must reject and preserve all consumer behavior.
  _,err=c.ExecContext(p.Ctx,"DROP TRANSFORM FOR hstore LANGUAGE td_lang RESTRICT")
  if diagnostic:=fixture.ExtractDiagnostic(t,err);diagnostic.Code!="2BP01" {t.Fatalf("RESTRICT=%v",err)}
  hostBag(t,p,b,c,"td_smoke_zero()",want)
  if !b.PG {
   stream:=transformsDump(t,p.Ctx,p.Native)
   for range 2 {
    fresh:=new(transformRuntime);db,target:=definitionOpen(t,fresh,"")
    if err=stuntgres.Restore(p.Ctx,db,bytes.NewReader(stream));err!=nil{t.Fatal(err)}
    hostBag(t,p,target,target.DB,"td_smoke_zero()",want)
    hostBag(t,p,target,target.DB,"(SELECT v FROM td_smoke_defaults)",want)
    if fresh.body.Load()==0 {t.Fatal("smoke replay did not execute destination capability")}
   }
   before:=transformsDump(t,p.Ctx,p.Native)
   canceled,stop:=context.WithCancel(p.Ctx);stop()
   if err=stuntgres.Restore(canceled,p.Native,bytes.NewReader(stream));!errors.Is(err,context.Canceled){t.Fatal(err)}
   if after:=transformsDump(t,p.Ctx,p.Native);!bytes.Equal(before,after){t.Fatal("canceled smoke restore changed catalog")}
   hostRows(t,p,b,held,q,[]string{"key","value"},[]string{"TEXT","TEXT"},want,"a=>7,b=>NULL")
   if r.body.Load()==0 {t.Fatal("smoke did not execute actual source capability")}
  }
  hostExec(t,p,b,c,"DROP TRANSFORM FOR hstore LANGUAGE td_lang CASCADE")
  definitionAbsent(t,p,b,c)
  hostRows(t,p,b,c,"SELECT count(*) AS n FROM pg_proc WHERE proname IN ('td_smoke','td_smoke_call','td_smoke_zero','td_smoke_state','td_smoke_aggregate')",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}})
  hostRows(t,p,b,c,"SELECT count(*) AS n FROM pg_class WHERE relname='td_smoke_view'",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}})
  // Only actual parsed dependency captures cascade. This quoted PL body is
  // deferred and lawfully survives; the two SQL RETURN wrappers above do not.
  hostRows(t,p,b,c,"SELECT proname FROM pg_proc WHERE proname='td_smoke_prefix'",[]string{"proname"},[]string{"NAME"},[][]any{{"td_smoke_prefix"}})
  hostRows(t,p,b,c,"SELECT count(*) AS n FROM pg_attrdef d JOIN pg_class c ON c.oid=d.adrelid WHERE c.relname='td_smoke_defaults'",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}})
  hostRows(t,p,b,c,"SELECT count(*) AS n FROM pg_constraint x JOIN pg_class c ON c.oid=x.conrelid WHERE c.relname='td_smoke_checks'",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}})
  hostRows(t,p,b,c,"SELECT v FROM td_smoke_effects",[]string{"v"},[]string{"INT4"},[][]any{{int64(11)}})
  var notices []fixture.Diagnostic
  _,noticeConn,record:=fixture.NoticeReferencePool(t,p,b.PG,&notices)
  fixture.RunStep(t,p.Ctx,noticeConn,record,fixture.Exec("DROP TRANSFORM IF EXISTS FOR hstore LANGUAGE td_lang",0))
  expected:=fixture.Diagnostic{Severity:"NOTICE",SeverityUnlocalized:"NOTICE",Code:"00000",Message:"transform for type hstore language \"td_lang\" does not exist, skipping"}
  if len(notices)!=1 || notices[0]!=expected {t.Fatalf("smoke notice=%#v want=%#v",notices,expected)}
  handler:="go_host_call_handler";if b.PG {handler="plpython3_call_handler"}
  hostExec(t,p,b,c,"CREATE LANGUAGE td_smoke_trigger HANDLER "+handler)
  defer routineLanguagesCleanup(t,c,"DROP LANGUAGE td_smoke_trigger CASCADE")()
  definitionAbsent(t,p,b,c)
 })
}
