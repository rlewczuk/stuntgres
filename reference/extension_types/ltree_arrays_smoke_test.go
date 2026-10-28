package reference_test

import (
 "bytes"
 "database/sql"
 "strings"
 "testing"
 "stuntgres"
 "stuntgres/reference/fixture"
)

func TestReferenceLtreeArrayFeatureSmoke(t *testing.T) {
 ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  ltreeArraysControlSetup(t,p,b,c)
  projection,columns,families,match:=ltreeArraysProjection()
  names:=append([]string{"id"},columns...);types:=append([]string{"INT4"},families...)
  matching:=append([]any{int64(1)},match...)
  missed,wholeNull:=make([]any,len(names)),make([]any,len(names));missed[0],wholeNull[0]=int64(2),int64(3)
  for i,family:=range families{if family=="BOOL"{missed[i+1]=false}}
  query:="SELECT id,"+projection+" FROM la_control ORDER BY id"
  step:=ltreeArraySelect(b,query,names,types,[][]any{matching,missed,wholeNull})
  fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
  metadata := ltreeArraySelect(b,"SELECT "+projection+" FROM la_control WHERE id=1",columns,families,[][]any{match})
  metadata.CheckMetadata=func(t *testing.T,rows *sql.Rows){ltreeArrayMetadata(t,b,rows,families,nil)}
  fixture.RunStep(t,p.Ctx,c,b.Recorder,metadata)
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT id FROM la_control WHERE paths @> target ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)}}))
  heldSQL:="SELECT $1::lquery[] ? $2::ltree[] AS v"
  statement,err:=c.PrepareContext(p.Ctx,heldSQL);if err!=nil{t.Fatal(err)};defer statement.Close()
  held:=fixture.HeldStatement{Executor:c,Statement:statement,SQL:heldSQL}
  fixture.RunStep(t,p.Ctx,held,b.Recorder,ltreeArrayResult(b,heldSQL,true,"{Top.*}","{Top.Science}"))
  fixture.RunStep(t,p.Ctx,held,b.Recorder,ltreeArrayResult(b,heldSQL,nil,nil,"{Top.Science}"))
  ltreeArraysFirstWitnesses(t,p,b,c)
  if !b.PG {
   var dump bytes.Buffer;if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil{t.Fatal(err)}
   if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
   if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil{t.Fatal(err)}
   fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
   fixture.RunStep(t,p.Ctx,c,b.Recorder,ltreeArraySelect(b,"SELECT "+strings.Join(columns,",")+" FROM la_view",columns,families,[][]any{match}))
  }
 })
}
