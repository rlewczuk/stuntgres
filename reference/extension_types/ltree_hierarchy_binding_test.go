package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceLtreeHierarchyBinding(t *testing.T) {
 fixture.SchemaReferenceCleanup(t,"lh_provider")
 ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
  for _,q:=range []string{"CREATE SCHEMA lh_provider","CREATE EXTENSION ltree SCHEMA lh_provider","SET search_path=public,lh_provider","CREATE DOMAIN lh_domain AS ltree"}{run(fixture.Exec(q,0))}
  for _,q:=range []string{
   "SELECT 'Top'::ltree @> 'Top.Child' AS v",
   "SELECT 'Top' @> 'Top.Child'::ltree AS v",
   "SELECT 'Top.Child'::ltree <@ 'Top' AS v",
   "SELECT 'Top.Child' <@ 'Top'::ltree AS v",
   "SELECT 'Top'::lh_domain @> 'Top.Child'::ltree AS v",
   "SELECT 'Top'::ltree OPERATOR(lh_provider.@>) 'Top.Child'::ltree AS v",
   "SELECT 'Top.Child'::ltree OPERATOR(lh_provider.<@) 'Top'::ltree AS v",
   "SELECT lh_provider.ltree_isparent('Top'::ltree,'Top.Child'::ltree) AS v",
   "SELECT lh_provider.ltree_risparent('Top.Child'::ltree,'Top'::ltree) AS v",
   "SELECT ARRAY['Top']::ltree[] @> 'Top.Child'::ltree AS v",
  } {s:=ltreeArrayResult(b,q,true);run(s);run(fixture.Prepared(s))}
  q:="SELECT $1 @> 'Top.Child'::ltree AS v"
  stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
  held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
  for _,tc:=range []struct{arg any;want any}{{"Top",true},{"Topical",false},{nil,nil}}{fixture.RunStep(t,p.Ctx,held,b.Recorder,ltreeArrayResult(b,q,tc.want,tc.arg))}
  q="SELECT $1::ltree <@ $2::ltree AS v"
  stmt2,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt2.Close()
  held2:=fixture.HeldStatement{Executor:c,Statement:stmt2,SQL:q}
  fixture.RunStep(t,p.Ctx,held2,b.Recorder,ltreeArrayResult(b,q,true,"Top.Child","Top"))
  fixture.RunStep(t,p.Ctx,held2,b.Recorder,ltreeArrayResult(b,q,nil,nil,"Top"))
  run(fixture.Exec("CREATE TABLE lh_empty(v ltree)",0))
  run(fixture.Exec("CREATE TABLE lh_null(v ltree)",0));run(fixture.Exec("INSERT INTO lh_null VALUES (NULL)",1))
  for _,tc:=range []struct{table string;rows [][]any}{{"lh_empty",[][]any{}},{"lh_null",[][]any{{nil,nil}}}}{
   s:=fixture.Select("SELECT v @> 'Top'::ltree AS ancestor,v <@ 'Top'::ltree AS descendant FROM "+tc.table,[]string{"ancestor","descendant"},[]string{"BOOL","BOOL"},tc.rows)
   s.CheckMetadata=func(t *testing.T,rows *sql.Rows){ltreeArrayMetadata(t,b,rows,[]string{"BOOL","BOOL"},nil)}
   run(s);run(fixture.Prepared(s))
  }
 })
}
