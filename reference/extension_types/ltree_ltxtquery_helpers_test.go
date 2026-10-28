package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

var ltreeLtxtqueryCases=[]ltreeArrayCase{
 {name:"ltxtq_exec",op:"@",leftType:"ltree",rightType:"ltxtquery",left:"Europe.Russia",right:"Europe & Russia",want:true},
 {name:"ltxtq_rexec",op:"@",leftType:"ltxtquery",rightType:"ltree",left:"Europe & Russia",right:"Europe.Russia",want:true},
}
var ltreeLtxtquerySemanticCases=[]struct{path,query string;want bool}{
 {"Europe.Russia","Europe & Russia",true},{"Russia.x.Europe","Europe & Russia",true},{"x.Europe.y.Russia.z","Europe & Russia",true},{"Europe.x","Europe & Russia",false},
 {"Top.Blocked","Top | Europe & !Blocked",true},{"Top.Blocked","(Top | Europe) & !Blocked",false},{"Europe","!Top & Europe",true},{"Top","!Top & Europe",false},{"Top","!(Top & Europe)",true},{"Other","!!Top",false},{"Top","!!Top",true},{"Top.Other","!Top",false},
 {"","foo",false},{"","!foo",true},{"Foo","foo",false},{"Foo","foo@",true},{"food","foo",false},{"food","foo*",true},{"fo","foo*",false},
 {"bar_foo","foo_bar%",true},{"foo","foo_foo%",true},{"foo.bar","foo_bar%",false},{"foo-bar","foo_bar%",false},{"foo_barbaz","foo_bar%",false},{"foo1_bar2_baz","foo_bar%*",true},{"foo1_br2_baz","foo_bar%*",false},{"FOO1_BAR2_baz","foo_bar%@*",true},
}
const ltreeLtxtqueryProjection="v @ 'Europe & Russia'::ltxtquery AS forward,'Europe & Russia'::ltxtquery @ v AS reverse,ltxtq_exec(v,'Europe & Russia'::ltxtquery) AS exec,ltxtq_rexec('Europe & Russia'::ltxtquery,v) AS rexec"
var ltreeLtxtqueryRows=[][]any{{int64(1),true,true,true,true},{int64(2),true,true,true,true},{int64(3),false,false,false,false},{int64(4),false,false,false,false},{int64(5),false,false,false,false},{int64(6),false,false,false,false},{int64(7),nil,nil,nil,nil},{int64(8),true,true,true,true}}
func ltreeLtxtqueryRun(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor,s fixture.Step){t.Helper();fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
func ltreeLtxtquerySeed(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor){
 t.Helper()
 for _,s:=range []fixture.Step{fixture.Exec("CREATE TABLE lxq_paths(id integer NOT NULL,v ltree)",0),fixture.Exec("INSERT INTO lxq_paths VALUES (1,'Europe.Russia'),(2,'Russia.x.Europe'),(3,'Europe.x'),(4,'Top.Blocked'),(5,'FOO1_BAR2_baz'),(6,''),(7,NULL),(8,'Europe.Russia')",8)}{ltreeLtxtqueryRun(t,p,b,c,s)}
}
func ltreeLtxtqueryChecks()[]fixture.Step{
 bag:=fixture.Select("SELECT v @ 'Europe & Russia'::ltxtquery AS v FROM lxq_paths",[]string{"v"},[]string{"BOOL"},[][]any{{true},{true},{false},{false},{false},{false},{nil},{true}});bag.Bag=true
 return []fixture.Step{
  fixture.Select("SELECT id,"+ltreeLtxtqueryProjection+" FROM lxq_paths ORDER BY id",[]string{"id","forward","reverse","exec","rexec"},[]string{"INT4","BOOL","BOOL","BOOL","BOOL"},ltreeLtxtqueryRows),
  ltreeComparisonIDs("SELECT id FROM lxq_paths WHERE v @ 'Europe & Russia'::ltxtquery ORDER BY id",1,2,8),bag,
 }
}
func ltreeLtxtqueryPair(q string,path,query,a,r any)fixture.Step{
 s:=fixture.Select(q,[]string{"forward","reverse"},[]string{"BOOL","BOOL"},[][]any{{a,r}},path,query)
 s.CheckMetadata=func(t *testing.T,rows *sql.Rows){ltreeArrayMetadata(t,fixture.Backend{},rows,[]string{"BOOL","BOOL"},nil)}
 return s
}
func ltreeLtxtqueryHeldChecks(t *testing.T,p *fixture.Pair,b fixture.Backend,held fixture.HeldStatement){
 t.Helper()
 for _,tc:=range []struct{path,query,want any}{{"Europe.Russia","Europe & Russia",true},{"Europe.x","Europe & Russia",false},{nil,"Europe",nil},{"Europe",nil,nil},{nil,nil,nil},{"Europe.Russia","Europe & Russia",true}}{ltreeLtxtqueryRun(t,p,b,held,ltreeLtxtqueryPair(held.SQL,tc.path,tc.query,tc.want,tc.want))}
}
