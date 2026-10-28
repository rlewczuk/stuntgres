package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceLtreePathFeatureSmoke(t *testing.T) {
 t.Run("consumers_and_recovery",func(t *testing.T) {
  ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
   ltreeInstall(t,p,b,c);run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
   run(fixture.Exec("CREATE TABLE lp_smoke(id integer,p ltree)",0));run(fixture.Exec("INSERT INTO lp_smoke VALUES (1,'Top.Child1.Child2'),(2,'A.B.C'),(3,''),(4,NULL)",4))
   s:=ltreeArraySelect(b,"SELECT id,nlevel(p) AS depth,ltree2text(p) AS txt FROM lp_smoke ORDER BY id",[]string{"id","depth","txt"},[]string{"INT4","INT4","TEXT"},[][]any{{int64(1),int64(3),"Top.Child1.Child2"},{int64(2),int64(3),"A.B.C"},{int64(3),int64(0),""},{int64(4),nil,nil}})
   run(s);run(fixture.Prepared(s))
   q:="SELECT subpath($1::ltree,1) AS sliced,index($1::ltree,'Missing'::ltree) AS missing,lca($1::ltree,$1::ltree) AS common,$1::ltree || $2::text AS joined"
   stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close();held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
   for _,tc:=range []struct{path,text string;want []any}{
    {"Top.Child1.Child2","Leaf",[]any{"Child1.Child2",int64(-1),"Top.Child1","Top.Child1.Child2.Leaf"}},
    {"A.B.C","D",[]any{"B.C",int64(-1),"A.B","A.B.C.D"}},
   }{s:=ltreeArraySelect(b,q,[]string{"sliced","missing","common","joined"},[]string{"LTREE","INT4","LTREE","LTREE"},[][]any{tc.want},tc.path,tc.text);fixture.RunStep(t,p.Ctx,held,b.Recorder,s)}
   for _,tc:=range []struct{query string;want any}{
    {"SELECT lca('[7:8]={A.B.C,A.B.D}'::ltree[]) AS v","A.B"},
    {"SELECT text2ltree('A.B') AS v","A.B"},
    {"SELECT lca('A'::ltree,'A'::ltree) AS v",""},
    {"SELECT lca('{}'::ltree[]) AS v",nil},
   }{run(ltreePathResult(b,tc.query,"LTREE",tc.want))}
   q="SELECT subpath($1::ltree,$2::integer) AS v";recoverStmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer recoverStmt.Close();recovery:=fixture.HeldStatement{Executor:c,Statement:recoverStmt,SQL:q}
   failure:=fixture.ErrorDiagnostic(q,fixture.ControlsReferenceDiagnostic("22023","invalid positions"),"A.B.C",3);failure.SelectRows=true;failure.ErrorStage="execute";fixture.RunStep(t,p.Ctx,recovery,b.Recorder,failure)
   fixture.RunStep(t,p.Ctx,recovery,b.Recorder,ltreePathResult(b,q,"LTREE","B.C","A.B.C",1))
   failure=fixture.ErrorDiagnostic("SELECT $1::ltree || $2::text",fixture.ControlsReferenceDiagnostic("42601","ltree syntax error at character 1"),"A",".Leaf");failure.SelectRows=true;run(failure)
   run(fixture.ErrorDiagnostic("INSERT INTO lp_smoke VALUES (5,subpath($1::ltree,$2::integer))",fixture.ControlsReferenceDiagnostic("22023","invalid positions"),"A.B.C",3))
   run(ltreeSelect(b,"SELECT id,p FROM lp_smoke ORDER BY id",[]string{"id","p"},[][]any{{int64(1),"Top.Child1.Child2"},{int64(2),"A.B.C"},{int64(3),""},{int64(4),nil}}))
  })
 })
 t.Run("stored_controls",func(t *testing.T){ltreePathLifecycle(t,"same")})
}
