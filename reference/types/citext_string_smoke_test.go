package reference_test

import (
 "bytes"
 "database/sql"
 "testing"

 "stuntgres"
 "stuntgres/reference/fixture"
)

func TestReferenceCitextStringFeatureSmoke(t *testing.T) {
 citextStringScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  citextStringStoredSetup(t,p,b,c);citextStringCorpusChecks(t,p,b,c)
  scalar:=fixture.Select("SELECT replace('ab'::citext,'','X') AS r,split_part('aTbT'::citext,'t',0) AS s,strpos('é🙂AbA'::citext,'a') AS p,translate('aAbB'::citext,'ab','BA') AS t",[]string{"r","s","p","t"},[]string{"TEXT","TEXT","INT4","TEXT"},[][]any{{"XaXbX",nil,int64(3),"ABBA"}})
  citextStringRoutes(t,p,b,c,scalar)
  citextStringRoutes(t,p,b,c,citextStringValue("SELECT replace('AbA'::citext,'a','X'::text) AS v","TEXT","AbA"))
  split:=citextStringDeclarations[2];q:=split.query()
  stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close()
  held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
  failure:=fixture.ErrorDiagnostic(q,citextStringParameterDiagnostic("22P02",`invalid input syntax for type integer: "bad"`,3),"aTbT","t","bad");failure.SelectRows=true;failure.ErrorStage="execute"
  fixture.RunStep(t,p.Ctx,held,b.Recorder,failure)
  fixture.RunStep(t,p.Ctx,held,b.Recorder,citextStringValue(q,"TEXT",nil,"aTbT","t",nil))
  fixture.RunStep(t,p.Ctx,held,b.Recorder,citextStringValue(q,"TEXT","b","aTbT","t",int64(2)))
  replace:=citextStringDeclarations[1];rq:=replace.query()
  rs,err:=c.PrepareContext(p.Ctx,rq);if err!=nil {t.Fatal(err)};defer rs.Close()
  rh:=fixture.HeldStatement{Executor:c,Statement:rs,SQL:rq}
  fixture.RunStep(t,p.Ctx,rh,b.Recorder,citextStringValue(rq,"TEXT","XbX","AbA","a","X"))
  fixture.RunStep(t,p.Ctx,rh,b.Recorder,citextStringValue(rq,"TEXT",nil,"AbA",nil,"X"))
  fixture.RunStep(t,p.Ctx,rh,b.Recorder,citextStringValue(rq,"TEXT","XaXbX","ab","","X"))
  citextStringStoredChecks(t,p,b,c)
  if !b.PG {
   var dump bytes.Buffer;if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil {t.Fatal(err)}
   if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil {t.Fatal(err)}
   if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil {t.Fatal(err)}
   citextStringStoredChecks(t,p,b,c);citextStringRoutes(t,p,b,c,scalar)
   citextStringReplay(t,p,dump.String(),false)
  }
 })
}
