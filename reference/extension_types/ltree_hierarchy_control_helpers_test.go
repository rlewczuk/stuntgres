package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

const ltreeHierarchySchema="lh_provider"
func ltreeHierarchyRun(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor,s fixture.Step){t.Helper();fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
func ltreeHierarchyControlSetup(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 t.Helper()
 for _,q:=range []string{"CREATE SCHEMA "+ltreeHierarchySchema,"CREATE EXTENSION ltree SCHEMA "+ltreeHierarchySchema,"SET search_path=public,"+ltreeHierarchySchema}{ltreeHierarchyRun(t,p,b,c,fixture.Exec(q,0))}
 ltreeHierarchySeed(t,p,b,c)
 for _,q:=range []string{
  "CREATE TABLE lh_empty(v ltree)","CREATE TABLE lh_null(v ltree)",
  "CREATE VIEW lh_view AS SELECT id,"+ltreeHierarchyProjection+" FROM lh_paths",
  "CREATE VIEW lh_descendants AS SELECT id FROM lh_paths WHERE v <@ 'Top.Science'::ltree",
  "CREATE VIEW lh_empty_view AS SELECT v @> 'Top'::ltree AS ancestor,v <@ 'Top'::ltree AS descendant FROM lh_empty",
  "CREATE VIEW lh_null_view AS SELECT v @> 'Top'::ltree AS ancestor,v <@ 'Top'::ltree AS descendant FROM lh_null",
 }{ltreeHierarchyRun(t,p,b,c,fixture.Exec(q,0))}
 ltreeHierarchyRun(t,p,b,c,fixture.Exec("INSERT INTO lh_null VALUES (NULL)",1))
}
func ltreeHierarchyControlChecks(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor){
 t.Helper()
 ltreeHierarchyRun(t,p,b,c,fixture.Exec("SET search_path=public,"+ltreeHierarchySchema,0))
 steps:=append(ltreeHierarchyChecks(),ltreeHierarchyCatalogChecks(ltreeHierarchySchema)...)
 steps=append(steps,
  fixture.Select("SELECT id,ancestor,descendant FROM lh_view ORDER BY id",[]string{"id","ancestor","descendant"},[]string{"INT4","BOOL","BOOL"},ltreeHierarchyRows),
  ltreeComparisonIDs("SELECT id FROM lh_descendants ORDER BY id",3,4),
  fixture.Select("SELECT ancestor,descendant FROM lh_empty_view",[]string{"ancestor","descendant"},[]string{"BOOL","BOOL"},[][]any{}),
  fixture.Select("SELECT ancestor,descendant FROM lh_null_view",[]string{"ancestor","descendant"},[]string{"BOOL","BOOL"},[][]any{{nil,nil}}),
  fixture.Select("SELECT 'Top'::ltree OPERATOR(lh_provider.@>) 'Top.Child'::ltree AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}}),
  fixture.Select("SELECT 'Top.Child'::ltree OPERATOR(lh_provider.<@) 'Top'::ltree AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}}),
  fixture.Select("SELECT lh_provider.ltree_isparent('Top'::ltree,'Top.Child'::ltree) AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}}),
  fixture.Select("SELECT lh_provider.ltree_risparent('Top.Child'::ltree,'Top'::ltree) AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}}),
 )
 for _,s:=range steps{ltreeHierarchyRun(t,p,b,c,s);ltreeHierarchyRun(t,p,b,c,fixture.Prepared(s))}
 // Declared stored-view nullability is independent of direct computed metadata.
 for _,name:=range []string{"lh_empty_view","lh_null_view"}{
  rows,err:=c.QueryContext(p.Ctx,"SELECT ancestor,descendant FROM "+name);if err!=nil{t.Fatal(err)}
  columns:=ltreeArrayMetadata(t,b,rows,[]string{"BOOL","BOOL"},[]bool{true,true})
  for rows.Next(){var a,d any;if err:=rows.Scan(&a,&d);err!=nil{t.Fatal(err)};if a!=nil||d!=nil{t.Fatal("stored NULL changed")}}
  if err:=rows.Err();err!=nil{t.Fatal(err)};rows.Close()
  ltreeArrayColumnMetadata(t,b,columns,[]string{"BOOL","BOOL"},[]bool{true,true})
 }
}
func ltreeHierarchyHeldChecks(t *testing.T,p *fixture.Pair,b fixture.Backend,held fixture.HeldStatement){
 t.Helper()
 for _,tc:=range []struct{left,right any;a,d any}{{"Top","Top.Child",true,false},{"Top","Top",true,true},{"Top","Topical",false,false},{"","Top",true,false},{nil,"Top",nil,nil}}{
  ltreeHierarchyRun(t,p,b,held,ltreeHierarchyPair(held.SQL,tc.left,tc.right,tc.a,tc.d))
 }
}
func ltreeHierarchyDrop(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 t.Helper()
 for _,q:=range []string{"DROP VIEW lh_view","DROP VIEW lh_descendants","DROP VIEW lh_empty_view","DROP VIEW lh_null_view","DROP TABLE lh_paths","DROP TABLE lh_empty","DROP TABLE lh_null","DROP EXTENSION ltree","DROP SCHEMA "+ltreeHierarchySchema}{ltreeHierarchyRun(t,p,b,c,fixture.Exec(q,0))}
}
