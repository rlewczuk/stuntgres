package core_test

import (
 "bytes"
 "testing"

 "stuntgres/internal/engine"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/parser"
)

func TestTransformDefinitionSQLProvenance(t *testing.T) {
 e,consumer:=transformDefinitionOwner(t,nil)
 original:=e.CatalogRead()
 bootstrap:=original.Get(consumer.Transforms.At(0).Pair).(*catalog.Transform)
 if bootstrap.SQLDefined || !catalog.TransformBootstrapEquivalent(original,bootstrap) {t.Fatal("genuine bootstrap selection lost authenticated reconstruction")}
 statement:="CREATE OR REPLACE TRANSFORM FOR hstore LANGUAGE owner_host (FROM SQL WITH FUNCTION owner_host_from(internal), TO SQL WITH FUNCTION owner_host_to(internal))"
 enginetest.Run(t,e,statement)
 view:=e.CatalogRead();defined:=view.Get(bootstrap.Address()).(*catalog.Transform)
 if !defined.SQLDefined || catalog.TransformBootstrapEquivalent(view,defined) {t.Fatal("explicit equivalent SQL definition collapsed into bootstrap")}
 if defined.From!=bootstrap.From || defined.To!=bootstrap.To || defined.Runtime!=bootstrap.Runtime || defined.SQLType!=bootstrap.SQLType {t.Fatal("equivalent SQL definition changed unrelated authority")}
 for _,flag:=range []bool{false,true} {
  bad:=*defined;bad.SQLDefined=flag;bad.From=defined.To
  state:=catalog.CloneState(view.State());state.SetDescriptor(bad.Address(),&bad)
  forged:=view.WithState(state)
  if err:=(catalog.TransformProvider{}).Validate(forged,&bad);err==nil || catalog.TransformBootstrapEquivalent(forged,&bad) {t.Fatal("provenance flag manufactured directional authority",flag)}
 }
 for _,name:=range []string{"owner_host_from","owner_host_to"} {enginetest.Run(t,e,"ALTER FUNCTION "+name+"(internal) VOLATILE")}
 var stream bytes.Buffer
 if err:=engine.Dump(t.Context(),e,&stream);err!=nil {t.Fatal(err)}
 transformReplayWindow(t,stream.String())
 target,_:=transformDefinitionOwner(t,nil)
 for range 2 {
  if err:=engine.Restore(t.Context(),target,bytes.NewReader(stream.Bytes()));err!=nil {t.Fatal(err)}
  current:=target.CatalogRead();p:=checkedDefaultOwnerProcedure(t,current,"owner_transform")
  pair:=current.Get(p.Transforms.At(0).Pair).(*catalog.Transform)
  if !pair.SQLDefined || catalog.TransformBootstrapEquivalent(current,pair) {t.Fatal("destination failed to reconstruct real emitted SQL provenance")}
  if pair.Runtime==defined.Runtime || pair.SQLType==defined.SQLType || p.Host.Layout==consumer.Host.Layout {t.Fatal("provenance replay borrowed source authority")}
 }
 if bootstrap.SQLDefined || original.Get(bootstrap.Address())!=bootstrap || !defined.SQLDefined {t.Fatal("provenance operation mutated retained snapshots")}
}

func transformReplayWindow(t *testing.T,sql string) {
 t.Helper()
 statements,err:=parser.Parse(sql);if err!=nil {t.Fatal(err)}
 stable,volatile:=make(map[string]int),make(map[string]int)
 pair,consumer:=-1,len(statements)
 consumers:=0
 for index,statement:=range statements {
  switch statement:=statement.(type) {
  case *parser.AlterRoutine:
   name:=statement.Routine.Name.Parts[len(statement.Routine.Name.Parts)-1].Text
   if name!="owner_host_from" && name!="owner_host_to" {continue}
   for _,attribute:=range statement.Attributes {
    if attribute.Kind=="stable" {stable[name]=index}
    if attribute.Kind=="volatile" {volatile[name]=index}
   }
  case *parser.CreateTransform:
   if statement.Language.Text=="owner_host" {if pair!=-1 {t.Fatal("duplicate logical pair replay")};pair=index}
  case *parser.CreateFunction:
   if statement.Language.Text=="owner_host" {consumer=min(consumer,index);consumers++}
  }
 }
 if pair<0 || consumers!=2 || len(stable)!=2 || len(volatile)!=2 {t.Fatal("missing actual pair/consumer/temporal SQL events",pair,consumers,stable,volatile)}
 start,end:=len(statements),-1
 for _,name:=range []string{"owner_host_from","owner_host_to"} {
  if stable[name]>=pair || volatile[name]<=pair || volatile[name]>=consumer {t.Fatal("temporary promise escaped its real pair/consumer bounds",name)}
  start=min(start,stable[name]);end=max(end,volatile[name])
 }
 for _,statement:=range statements[start:end+1] {
  switch statement:=statement.(type) {
  case *parser.AlterRoutine:
   name:=statement.Routine.Name.Parts[len(statement.Routine.Name.Parts)-1].Text
   if name!="owner_host_from" && name!="owner_host_to" {t.Fatal("unrelated callback mutation inside bounded replay window")}
   if len(statement.Attributes)!=1 || statement.Attributes[0].Kind!="stable" && statement.Attributes[0].Kind!="volatile" {t.Fatal("non-volatility operation inside bounded replay window")}
  case *parser.CreateTransform:
   if statement.Language.Text!="owner_host" {t.Fatal("unrelated pair inside bounded replay window")}
  case *parser.Set:
   if statement.Name.Text!="role" && statement.Name.Text!="search_path" {t.Fatal("unrelated setting inside bounded replay window")}
  default:t.Fatalf("callback-capable/nonmetadata statement inside bounded replay window: %T",statement)
  }
 }
}
