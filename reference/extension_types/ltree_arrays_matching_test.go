package reference_test

import (
 "database/sql"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceLtreeArrayMatching(t *testing.T) {
 t.Run("lquery",func(t *testing.T) {
  ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
   ltreeInstall(t,p,b,c)
   for _,tc:=range []struct{ path,query string; want bool }{
    {"","*",true},{"","a{0}",true},{"","a",false},{"a.b","a",false},
    {"a.a.b","a{1,2}.b",true},{"a.b","a{2}.b",false},{"a.a","a{0,2}.a",true},
    {"a.b","*.*.b.*{0}",true},{"a","a{0}.a",true},{"b","a|b",true},
    {"a.c","a.!b|d",true},{"a.b","a.!b|d",false},{"c.c","!a|b{2}",true},{"c.b","!a|b{2}",false},
    {"Top.sporting.swim.Russia","Top.*{0,2}.sport*@.!football|tennis{1,}.Russ*|Spain",true},
    {"Top.sporting.football.Russia","Top.*{0,2}.sport*@.!football|tennis{1,}.Russ*|Spain",false},
    {"Top.sporting.swim.France","Top.*{0,2}.sport*@.!football|tennis{1,}.Russ*|Spain",false},
    {"bar_foo","foo_bar%",true},{"foo","foo_foo%",true},{"a","_%",true},
    {"_bar__foo_","__foo__bar_%",true},{"foo-bar","foo_bar%",false},
    {"foo1_bar2_baz","foo_bar%*",true},{"foo1_br2_baz","foo_bar%*",false},
    {"FOO1_BAR2_baz","foo_bar%@*",true},{"Foo","foo",false},{"Foo","foo@",true},
    {"food","foo*",true},{"fo","foo*",false},{"food","foo",false},
    {strings.Repeat("a",1000),strings.Repeat("a",1000),true},
    {strings.Repeat("a.",65534)+"a","a{65535}",true},
    {strings.Repeat("a.",65534)+"a",strings.Repeat("b.",65534)+"b",false},
   } {
    path:="{"+tc.path+"}"; if tc.path=="" { path=`{""}` }
    left,right:=ltreeArrayLiteral(path,"ltree[]"),ltreeArrayLiteral(tc.query,"lquery")
    for _,expression:=range []string{left+" ~ "+right,right+" ~ "+left} {
     step:=ltreeArrayResult(b,"SELECT "+expression+" AS v",tc.want)
     fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
    }
   }
  })
 })
 t.Run("ltxtquery",func(t *testing.T) {
  ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
   ltreeInstall(t,p,b,c)
   for _,tc:=range []struct{ paths,query string; want bool }{
    {"{Top,Europe}","Top & Europe",false},{"{Europe.Top}","Top & Europe",true},
    {`{""}`,"!foo",true},{`{""}`,"foo",false},{"{}","!foo",false},
    {"{Top.Blocked}","Top | Europe & !Blocked",true},
    {"{Top.Blocked}","(Top | Europe) & !Blocked",false},
    {"{Top}","!!Top",true},{"{Other}","!!Top",false},
    {"{FOO1_BAR2_baz}","foo_bar%@*",true},{"{bar_foo}","foo_bar%",true},
    {"{a.b}","b & a & !c",true},
   } {
    left,right:=ltreeArrayLiteral(tc.paths,"ltree[]"),ltreeArrayLiteral(tc.query,"ltxtquery")
    for _,expression:=range []string{left+" @ "+right,right+" @ "+left} { fixture.RunStep(t,p.Ctx,c,b.Recorder,ltreeArrayResult(b,"SELECT "+expression+" AS v",tc.want)) }
   }
  })
 })
}
