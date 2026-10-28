package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceLtreeLtxtqueryMatching(t *testing.T){
 ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  ltreeInstall(t,p,b,c)
  run:=func(s fixture.Step){ltreeLtxtqueryRun(t,p,b,c,s)}
  forms:=[]string{"SELECT $1::ltree @ $2::ltxtquery AS v","SELECT $2::ltxtquery @ $1::ltree AS v","SELECT ltxtq_exec($1::ltree,$2::ltxtquery) AS v","SELECT ltxtq_rexec($2::ltxtquery,$1::ltree) AS v"}
  for _,q:=range forms{
   statement,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)}
   held:=fixture.HeldStatement{Executor:c,Statement:statement,SQL:q}
   for _,tc:=range ltreeLtxtquerySemanticCases{
    s:=ltreeArrayResult(b,q,tc.want,tc.path,tc.query);run(s);run(fixture.Prepared(s));ltreeLtxtqueryRun(t,p,b,held,s)
   }
   for _,args:=range [][2]any{{nil,"Europe"},{"Europe",nil},{nil,nil}}{
    s:=ltreeArrayResult(b,q,nil,args[0],args[1]);run(s);run(fixture.Prepared(s));ltreeLtxtqueryRun(t,p,b,held,s)
   }
   statement.Close()
  }
  // Selected overload coercion accepts unknown literals, not explicit TEXT.
  for _,q:=range []string{"SELECT 'Europe.Russia'::ltree @ 'Europe & Russia' AS v","SELECT 'Europe & Russia' @ 'Europe.Russia'::ltree AS v","SELECT ltxtq_exec('Europe.Russia','Europe & Russia') AS v","SELECT ltxtq_rexec('Europe & Russia','Europe.Russia') AS v"}{s:=ltreeArrayResult(b,q,true);run(s);run(fixture.Prepared(s))}
  ltreeLtxtquerySeed(t,p,b,c)
  for _,s:=range ltreeLtxtqueryChecks(){run(s);run(fixture.Prepared(s))}
  for _,q:=range []string{"CREATE VIEW lxq_match AS SELECT id,"+ltreeLtxtqueryProjection+" FROM lxq_paths","CREATE VIEW lxq_filter AS SELECT id FROM lxq_paths WHERE v @ 'Europe & Russia'::ltxtquery"}{run(fixture.Exec(q,0))}
  run(fixture.Select("SELECT id,forward,reverse,exec,rexec FROM lxq_match ORDER BY id",[]string{"id","forward","reverse","exec","rexec"},[]string{"INT4","BOOL","BOOL","BOOL","BOOL"},ltreeLtxtqueryRows))
  run(ltreeComparisonIDs("SELECT id FROM lxq_filter ORDER BY id",1,2,8))
  q:="SELECT $1::ltree @ $2::ltxtquery AS forward,$2::ltxtquery @ $1::ltree AS reverse"
  statement,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer statement.Close()
  ltreeLtxtqueryHeldChecks(t,p,b,fixture.HeldStatement{Executor:c,Statement:statement,SQL:q})
 })
}
