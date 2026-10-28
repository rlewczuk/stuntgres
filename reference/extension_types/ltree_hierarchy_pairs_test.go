package reference_test

import (
 "database/sql"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceLtreeHierarchyPairs(t *testing.T) {
 ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  ltreeInstall(t,p,b,c)
  for _, direct:=range []bool{false,true} {
   q:="SELECT $1::ltree @> $2::ltree AS ancestor,$1::ltree <@ $2::ltree AS descendant"
   if direct {q="SELECT ltree_isparent($1::ltree,$2::ltree) AS ancestor,ltree_risparent($1::ltree,$2::ltree) AS descendant"}
   stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)}
   held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
   for _,tc:=range []struct{left,right any;a,d any}{
    {"Top.Science","Top.Science.Astronomy",true,false},{"Top.Science.Astronomy","Top.Science",false,true},
    {"Top.Science","Top.Science",true,true},{"Top.Science","Top.ScienceX",false,false},
    {"Top","Topical.Child",false,false},{"Top.Science","Top.Hobbies",false,false},{"Top","top.Child",false,false},
    {"a-b",[]byte("a-b.c_d"),true,false},{"a-b","a.b",false,false},
    {"","",true,true},{"","Top",true,false},{"Top","",false,true},
    {nil,"Top",nil,nil},{"",nil,nil,nil},{nil,nil,nil,nil},
   } {
    s:=ltreeHierarchyPair(q,tc.left,tc.right,tc.a,tc.d)
    fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
    fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(s))
    fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
   }
   stmt.Close()
  }
  ltreeHierarchySeed(t,p,b,c)
  for _,s:=range ltreeHierarchyChecks(){fixture.RunStep(t,p.Ctx,c,b.Recorder,s);fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(s))}
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE lh_selected(v boolean)",0))
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO lh_selected SELECT v @> 'Top.Science'::ltree FROM lh_paths WHERE id=3",1))
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT v FROM lh_selected",[]string{"v"},[]string{"BOOL"},[][]any{{true}}))
 })
}

func TestReferenceLtreeHierarchyBoundaries(t *testing.T) {
 for _,tc:=range []struct{name,left,right string;a,d bool}{
  {"long_equal",strings.Repeat("a",1000),strings.Repeat("a",1000),true,true},
  {"long_prefix",strings.Repeat("a",1000),strings.Repeat("a",1000)+".b",true,false},
  {"deep_equal",strings.Repeat("a.",65534)+"a",strings.Repeat("a.",65534)+"a",true,true},
  {"deep_prefix","a",strings.Repeat("a.",65534)+"a",true,false},
  {"deep_mismatch",strings.Repeat("a.",65534)+"a",strings.Repeat("a.",65534)+"b",false,false},
 } {t.Run(tc.name,func(t *testing.T){
  ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
   ltreeInstall(t,p,b,c)
   q:="SELECT $1::ltree @> $2::ltree AS ancestor,$1::ltree <@ $2::ltree AS descendant"
   fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(ltreeHierarchyPair(q,tc.left,tc.right,tc.a,tc.d)))
  })
 })}
}
