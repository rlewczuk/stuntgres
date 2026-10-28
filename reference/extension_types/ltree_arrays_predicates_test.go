package reference_test

import (
 "database/sql"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceLtreeArrayPredicates(t *testing.T) {
 for _, tc := range ltreeArrayCases {
  t.Run(tc.name,func(t *testing.T) {
   ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
    ltreeInstall(t,p,b,c)
    family := tc.family(); var miss any = false
    if family == "LTREE" { miss = nil }
    for _, direct := range []bool{false,true} {
     left,right := ltreeArrayLiteral(tc.left,tc.leftType),ltreeArrayLiteral(tc.right,tc.rightType)
     for _, witness := range []struct{ left,right string; want any }{
      {left,right,tc.want},
      {ltreeArrayLiteral(tc.missLeft,tc.leftType),ltreeArrayLiteral(tc.missRight,tc.rightType),miss},
      {"NULL::"+tc.leftType,right,nil},{left,"NULL::"+tc.rightType,nil},
     } {
      step:=ltreeArraySelect(b,"SELECT "+tc.expression(witness.left,witness.right,direct)+" AS v",[]string{"v"},[]string{family},[][]any{{witness.want}})
      fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
      fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(step))
     }
     for operand,typ:=range []string{tc.leftType,tc.rightType}{
      if !strings.HasSuffix(typ,"[]"){continue}
      a,r:=left,right;if operand==0{a=ltreeArrayLiteral("{}",typ)}else{r=ltreeArrayLiteral("{}",typ)}
      step:=ltreeArraySelect(b,"SELECT "+tc.expression(a,r,direct)+" AS v",[]string{"v"},[]string{family},[][]any{{miss}})
      fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
     }
    }
   })
  })
 }
 t.Run("boundaries",func(t *testing.T) {
  ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
   ltreeInstall(t,p,b,c)
   for _, tc := range []struct{ expression string; want bool }{
    {"'{ab}'::ltree[] @> 'a'::ltree",false},{"'{A}'::ltree[] @> 'a.b'::ltree",false},
    {"'{a}'::ltree[] @> 'a'::ltree",true},{"'{}'::ltree[] @> 'a'::ltree",false},
    {`'{""}'::ltree[] @> 'a'::ltree`,true},{"'{a}'::ltree[] <@ ''::ltree",true},
    {"'{a}'::ltree[] @> 'a.b'",true},
    {"'{NULL}'::ltree[] ? '{}'::lquery[]",false},{"'{{a}}'::ltree[] ? '{}'::lquery[]",false},
    {"'{}'::lquery[] ? '{{a,NULL}}'::ltree[]",false},
   } { fixture.RunStep(t,p.Ctx,c,b.Recorder,ltreeArrayResult(b,"SELECT "+tc.expression+" AS v",tc.want)) }
   for _, tc:=range []struct{ expression string; want any }{
    {`'{"",a}'::ltree[] ?@> 'a.b'::ltree`,""},
    {"'[7:8]={Top,Top.Science}'::ltree[] ?@> 'Top.Science.Astro'::ltree","Top"},
    {"'{}'::ltree[] ?<@ 'a'::ltree",nil},
   } { fixture.RunStep(t,p.Ctx,c,b.Recorder,ltreeArraySelect(b,"SELECT "+tc.expression+" AS v",[]string{"v"},[]string{"LTREE"},[][]any{{tc.want}})) }
  })
 })
 t.Run("stored_where",func(t *testing.T) {
  ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
   ltreeInstall(t,p,b,c)
   fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE la_pred(id integer,paths ltree[],queries lquery[],target ltree,q lquery,x ltxtquery)",0))
   fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO la_pred VALUES (1,'{Top,Top.Science,Top.Science.Astro}','{Top.*}','Top.Science','Top.*','Top & Science'),(2,'{Other}','{Nope}','Top.Science','Top.*','Top & Science'),(3,'{}','{}','Top.Science','Top.*','Top & Science'),(4,NULL,NULL,NULL,NULL,NULL)",4))
   query := "SELECT id,paths @> target AS a,paths ~ q AS m,paths ? queries AS q,paths @ x AS x,paths ?@> target AS first FROM la_pred ORDER BY id"
   step:=ltreeArraySelect(b,query,[]string{"id","a","m","q","x","first"},[]string{"INT4","BOOL","BOOL","BOOL","BOOL","LTREE"},[][]any{{int64(1),true,true,true,true,"Top"},{int64(2),false,false,false,false,nil},{int64(3),false,false,false,false,nil},{int64(4),nil,nil,nil,nil,nil}})
   fixture.RunStep(t,p.Ctx,c,b.Recorder,step); fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(step))
   fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT id FROM la_pred WHERE paths @> target ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)}}))
   fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE DOMAIN la_path AS ltree",0))
   fixture.RunStep(t,p.Ctx,c,b.Recorder,ltreeArrayResult(b,"SELECT '{a}'::la_path[] @> 'a.b'::la_path AS v",true))
  })
 })
}
