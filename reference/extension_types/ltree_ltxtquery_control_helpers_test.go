package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

const ltreeLtxtquerySchema="lxq_provider"
func ltreeLtxtqueryControlSetup(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 t.Helper()
 for _,q:=range []string{"CREATE SCHEMA "+ltreeLtxtquerySchema,"CREATE EXTENSION ltree SCHEMA "+ltreeLtxtquerySchema,"SET search_path=public,"+ltreeLtxtquerySchema}{ltreeLtxtqueryRun(t,p,b,c,fixture.Exec(q,0))}
 ltreeLtxtquerySeed(t,p,b,c)
 for _,q:=range []string{
  "CREATE TABLE lxq_empty(v ltree)","CREATE TABLE lxq_null(v ltree)",
  "CREATE VIEW lxq_view AS SELECT id,"+ltreeLtxtqueryProjection+" FROM lxq_paths",
  "CREATE VIEW lxq_hits AS SELECT id FROM lxq_paths WHERE v @ 'Europe & Russia'::ltxtquery",
  "CREATE VIEW lxq_empty_view AS SELECT v @ 'Europe'::ltxtquery AS forward,'Europe'::ltxtquery @ v AS reverse FROM lxq_empty",
  "CREATE VIEW lxq_null_view AS SELECT v @ 'Europe'::ltxtquery AS forward,'Europe'::ltxtquery @ v AS reverse FROM lxq_null",
 }{ltreeLtxtqueryRun(t,p,b,c,fixture.Exec(q,0))}
 ltreeLtxtqueryRun(t,p,b,c,fixture.Exec("INSERT INTO lxq_null VALUES (NULL)",1))
}
func ltreeLtxtqueryControlChecks(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor){
 t.Helper()
 ltreeLtxtqueryRun(t,p,b,c,fixture.Exec("SET search_path=public,"+ltreeLtxtquerySchema,0))
 steps:=append(ltreeLtxtqueryChecks(),ltreeLtxtqueryCatalogChecks(ltreeLtxtquerySchema)...)
 steps=append(steps,
  fixture.Select("SELECT id,forward,reverse,exec,rexec FROM lxq_view ORDER BY id",[]string{"id","forward","reverse","exec","rexec"},[]string{"INT4","BOOL","BOOL","BOOL","BOOL"},ltreeLtxtqueryRows),
  ltreeComparisonIDs("SELECT id FROM lxq_hits ORDER BY id",1,2,8),
  fixture.Select("SELECT forward,reverse FROM lxq_empty_view",[]string{"forward","reverse"},[]string{"BOOL","BOOL"},[][]any{}),
  fixture.Select("SELECT forward,reverse FROM lxq_null_view",[]string{"forward","reverse"},[]string{"BOOL","BOOL"},[][]any{{nil,nil}}),
 )
 for _,s:=range steps{ltreeLtxtqueryRun(t,p,b,c,s);ltreeLtxtqueryRun(t,p,b,c,fixture.Prepared(s))}
 // Stored invocation and qualified calls retain authority with discovery disabled.
 ltreeLtxtqueryRun(t,p,b,c,fixture.Exec("SET search_path=public",0))
 for _,q:=range []string{
  "SELECT 'Europe.Russia'::lxq_provider.ltree OPERATOR(lxq_provider.@) 'Europe & Russia'::lxq_provider.ltxtquery AS v",
  "SELECT 'Europe & Russia'::lxq_provider.ltxtquery OPERATOR(lxq_provider.@) 'Europe.Russia'::lxq_provider.ltree AS v",
  "SELECT lxq_provider.ltxtq_exec('Europe.Russia'::lxq_provider.ltree,'Europe & Russia'::lxq_provider.ltxtquery) AS v",
  "SELECT lxq_provider.ltxtq_rexec('Europe & Russia'::lxq_provider.ltxtquery,'Europe.Russia'::lxq_provider.ltree) AS v",
 }{s:=ltreeArrayResult(b,q,true);ltreeLtxtqueryRun(t,p,b,c,s);ltreeLtxtqueryRun(t,p,b,c,fixture.Prepared(s))}
 ltreeLtxtqueryRun(t,p,b,c,fixture.Select("SELECT id,forward,reverse,exec,rexec FROM lxq_view ORDER BY id",[]string{"id","forward","reverse","exec","rexec"},[]string{"INT4","BOOL","BOOL","BOOL","BOOL"},ltreeLtxtqueryRows))
 for _,name:=range []string{"lxq_empty_view","lxq_null_view"}{
  rows,err:=c.QueryContext(p.Ctx,"SELECT forward,reverse FROM "+name);if err!=nil{t.Fatal(err)}
  columns:=ltreeArrayMetadata(t,b,rows,[]string{"BOOL","BOOL"},[]bool{true,true})
  count:=0
  for rows.Next(){var a,r any;if err:=rows.Scan(&a,&r);err!=nil{t.Fatal(err)};if a!=nil||r!=nil{t.Fatal("stored NULL changed")};count++}
  if err:=rows.Err();err!=nil{t.Fatal(err)};rows.Close();want:=0;if name=="lxq_null_view"{want=1};if count!=want{t.Fatal("stored row count",count,want)}
  ltreeArrayColumnMetadata(t,b,columns,[]string{"BOOL","BOOL"},[]bool{true,true})
 }
 // Direct computed metadata is unknown even for empty/all-NULL inputs.
 for _,tc:=range []struct{table string;rows [][]any}{{"lxq_empty",[][]any{}},{"lxq_null",[][]any{{nil,nil}}}}{
  s:=fixture.Select("SELECT v OPERATOR(lxq_provider.@) 'Europe'::lxq_provider.ltxtquery AS forward,'Europe'::lxq_provider.ltxtquery OPERATOR(lxq_provider.@) v AS reverse FROM "+tc.table,[]string{"forward","reverse"},[]string{"BOOL","BOOL"},tc.rows)
  s.CheckMetadata=func(t *testing.T,rows *sql.Rows){ltreeArrayMetadata(t,b,rows,[]string{"BOOL","BOOL"},nil)}
  ltreeLtxtqueryRun(t,p,b,c,s);ltreeLtxtqueryRun(t,p,b,c,fixture.Prepared(s))
 }
 ltreeLtxtqueryRun(t,p,b,c,fixture.Exec("SET search_path=public,"+ltreeLtxtquerySchema,0))
}
func ltreeLtxtqueryDrop(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 t.Helper()
 for _,q:=range []string{"DROP VIEW lxq_view","DROP VIEW lxq_hits","DROP VIEW lxq_empty_view","DROP VIEW lxq_null_view","DROP TABLE lxq_paths","DROP TABLE lxq_empty","DROP TABLE lxq_null","DROP EXTENSION ltree","DROP SCHEMA "+ltreeLtxtquerySchema}{ltreeLtxtqueryRun(t,p,b,c,fixture.Exec(q,0))}
}
