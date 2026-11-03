package reference_test

import (
 "database/sql"
 "reflect"
 "testing"
 "stuntgres/reference/fixture"
)

var resetReplicationFunctions = []struct{name, argument string; oid int64; input string}{
 {"pg_stat_reset_replication_slot","text",6170,"25"},
 {"pg_stat_reset_subscription_stats","oid",6232,"26"},
}
func resetReplicationVoid(t *testing.T, p *fixture.Pair, b fixture.Backend, executor fixture.Executor, query, label string, args ...any) {
 t.Helper()
 step:=fixture.Select(query,[]string{label},[]string{"VOID"},[][]any{{""}},args...)
 step.CheckMetadata=func(t *testing.T, rows *sql.Rows) {
  columns,err:=rows.ColumnTypes(); if err!=nil || len(columns)!=1 { t.Fatal(columns,err) }
  scan:=reflect.TypeFor[string](); if b.PG { scan=reflect.TypeFor[any]() }
  if columns[0].ScanType()!=scan { t.Fatal("VOID ScanType",columns[0].ScanType(),scan) }
  if _,known:=columns[0].Nullable(); known { t.Fatal("computed VOID has known nullability") }
 }
 fixture.RunStep(t,p.Ctx,executor,b.Recorder,step)
}
func resetReplicationRuntimeError(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,query string,want fixture.Diagnostic,args ...any) {
 t.Helper()
 for _,prepared:=range []bool{false,true} {
  step:=fixture.ErrorDiagnostic(query,want,args...); step.SelectRows=true; step.Prepared=prepared
  if prepared { step.ErrorStage="execute" }
  fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
 }
}
func TestReferenceStatsResetReplicationSignatures(t *testing.T) {
 activityScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  for _,f:=range resetReplicationFunctions {
   for _,prepared:=range []bool{false,true} {
    for _,argument:=range []string{"NULL","NULL::"+f.argument} {
     query:="SELECT "+f.name+"("+argument+")"
     step:=fixture.Select(query,[]string{f.name},[]string{"VOID"},[][]any{{""}}); step.Prepared=prepared
     step.CheckMetadata=func(t *testing.T,rows *sql.Rows) {
      columns,err:=rows.ColumnTypes(); if err!=nil || len(columns)!=1 { t.Fatal(columns,err) }
      scan:=reflect.TypeFor[string](); if b.PG { scan=reflect.TypeFor[any]() }; if columns[0].ScanType()!=scan { t.Fatal(columns[0].ScanType(),scan) }
      if _,known:=columns[0].Nullable(); known { t.Fatal("known computed nullability") }
     }
     fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
    }
   }
   resetReplicationVoid(t,p,b,c,"SELECT pg_catalog.\""+f.name+"\"(NULL::"+f.argument+") AS reset_value","reset_value")
   resetReplicationVoid(t,p,b,c,"SELECT * FROM pg_catalog."+f.name+"(NULL::"+f.argument+")",f.name)
   query:="SELECT pg_catalog."+f.name+"($1::"+f.argument+")"
   stmt,err:=c.PrepareContext(p.Ctx,query); if err!=nil { t.Fatal(err) }
   held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:query}
   for _,arg:=range []any{nil,nil} { resetReplicationVoid(t,p,b,held,query,f.name,arg) }
   if f.argument=="oid" {
    resetReplicationVoid(t,p,b,held,query,f.name,int64(4294967294))
    step:=fixture.ErrorDiagnostic(query,activityDiagnostic("22023","invalid subscription OID 0",0),int64(0)); step.SelectRows=true
    fixture.RunStep(t,p.Ctx,held,b.Recorder,step)
    resetReplicationVoid(t,p,b,held,query,f.name,nil)
   } else {
    step:=fixture.ErrorDiagnostic(query,activityDiagnostic("22023",`replication slot "reset_missing_slot" does not exist`,0),"reset_missing_slot"); step.SelectRows=true
    fixture.RunStep(t,p.Ctx,held,b.Recorder,step)
    resetReplicationVoid(t,p,b,held,query,f.name,nil)
   }
   if err:=stmt.Close(); err!=nil { t.Fatal(err) }
  }
  for _,typ:=range []string{"smallint","integer","bigint"} { resetReplicationVoid(t,p,b,c,"SELECT pg_stat_reset_subscription_stats(123::"+typ+")","pg_stat_reset_subscription_stats") }
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT pg_stat_reset_subscription_stats(v) AS reset_value FROM (VALUES (123::oid),(124::oid),(NULL::oid)) AS targets(v)",[]string{"reset_value"},[]string{"VOID"},[][]any{{""},{""},{""}}))
  query:=`SELECT oid::bigint AS oid,proname::text AS name,pronargs::bigint AS nargs,prorettype::bigint AS result,proisstrict AS strict,provolatile::text AS volatility,proparallel::text AS parallel,proretset AS is_set,proargtypes::text AS inputs,proargnames::text AS names,proargmodes::text AS modes,proallargtypes::text AS alltypes,pronargdefaults::bigint AS defaults,provariadic::bigint AS variadic,prosecdef AS definer,prolang::bigint AS language,prosrc AS source FROM pg_proc WHERE oid IN (6170,6232) ORDER BY oid`
  var rows [][]any
  for _,f:=range resetReplicationFunctions { rows=append(rows,[]any{f.oid,f.name,int64(1),int64(2278),false,"v","s",false,f.input,nil,nil,nil,int64(0),int64(0),false,int64(12),f.name}) }
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select(query,[]string{"oid","name","nargs","result","strict","volatility","parallel","is_set","inputs","names","modes","alltypes","defaults","variadic","definer","language","source"},[]string{"INT8","TEXT","INT8","INT8","BOOL","TEXT","TEXT","BOOL","TEXT","TEXT","TEXT","TEXT","INT8","INT8","BOOL","INT8","TEXT"},rows))
  for _,f:=range resetReplicationFunctions {
   activityExec(t,p,c,"CREATE FUNCTION public."+f.name+"("+f.argument+") RETURNS text LANGUAGE SQL AS 'SELECT ''shadow''::text'")
   activityExec(t,p,c,"SET search_path=public,pg_catalog")
   var value string
   if err:=c.QueryRowContext(p.Ctx,"SELECT "+f.name+"(NULL::"+f.argument+")").Scan(&value); err!=nil || value!="shadow" { t.Fatal(value,err) }
   resetReplicationVoid(t,p,b,c,"SELECT pg_catalog."+f.name+"(NULL::"+f.argument+")",f.name)
   activityExec(t,p,c,"SET search_path=pg_catalog,public")
   resetReplicationVoid(t,p,b,c,"SELECT "+f.name+"(NULL::"+f.argument+")",f.name)
   activityExec(t,p,c,"DROP FUNCTION public."+f.name+"("+f.argument+")")
  }
 })
}
