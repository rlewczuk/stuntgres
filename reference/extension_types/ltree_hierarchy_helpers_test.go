package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

var ltreeHierarchyCases = []ltreeArrayCase{
 {name:"ltree_isparent",op:"@>",leftType:"ltree",rightType:"ltree",left:"Top.Science",right:"Top.Science.Astro",want:true},
 {name:"ltree_risparent",op:"<@",leftType:"ltree",rightType:"ltree",left:"Top.Science.Astro",right:"Top.Science",want:true},
}
const ltreeHierarchyProjection = "v @> 'Top.Science'::ltree AS ancestor,v <@ 'Top.Science'::ltree AS descendant"
var ltreeHierarchyRows = [][]any{{int64(1),true,false},{int64(2),true,false},{int64(3),true,true},{int64(4),false,true},{int64(5),false,false},{int64(6),false,false},{int64(7),false,false},{int64(8),nil,nil}}
func ltreeHierarchySeed(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor) {
 t.Helper()
 for _, s := range []fixture.Step{
  fixture.Exec("CREATE TABLE lh_paths(id integer NOT NULL,v ltree)",0),
  fixture.Exec("INSERT INTO lh_paths VALUES (1,''),(2,'Top'),(3,'Top.Science'),(4,'Top.Science.Astronomy'),(5,'Top.ScienceX'),(6,'Top.Hobbies'),(7,'top.Science'),(8,NULL)",8),
 } { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
}
func ltreeHierarchyChecks() []fixture.Step {
 return []fixture.Step{
  fixture.Select("SELECT id,"+ltreeHierarchyProjection+" FROM lh_paths ORDER BY id",[]string{"id","ancestor","descendant"},[]string{"INT4","BOOL","BOOL"},ltreeHierarchyRows),
  ltreeComparisonIDs("SELECT id FROM lh_paths WHERE v <@ 'Top.Science'::ltree ORDER BY id",3,4),
  ltreeComparisonIDs("SELECT id FROM lh_paths WHERE v @> 'Top.Science.Astronomy'::ltree ORDER BY id",1,2,3,4),
  ltreeComparisonIDs("SELECT id FROM lh_paths WHERE v @> ''::ltree ORDER BY id",1),
  ltreeComparisonIDs("SELECT id FROM lh_paths WHERE v <@ ''::ltree ORDER BY id",1,2,3,4,5,6,7),
  ltreeComparisonIDs("SELECT id FROM lh_paths WHERE v @> NULL::ltree ORDER BY id"),
  ltreeComparisonIDs("SELECT id FROM lh_paths WHERE v <@ NULL::ltree ORDER BY id"),
 }
}
func ltreeHierarchyPair(q string,left,right any,a,d any) fixture.Step {
 s:=fixture.Select(q,[]string{"ancestor","descendant"},[]string{"BOOL","BOOL"},[][]any{{a,d}},left,right)
 s.CheckMetadata=func(t *testing.T,rows *sql.Rows){ltreeArrayMetadata(t,fixture.Backend{},rows,[]string{"BOOL","BOOL"},nil)}
 return s
}
