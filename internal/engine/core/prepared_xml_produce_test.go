package core_test

import (
	"context"
	"errors"
	"testing"

	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqltype"
)

func TestXMLProduceUnknownParameterAdmission(t *testing.T){
 e:=rootengine.New();s:=enginetest.StartSession(t,e,nil)
 ast,accepted:=enginetest.PreparedDescription(t,e,s,`SELECT xmltext('accepted') AS v`)
 enginetest.PreparedFoldExecute(t,e,s,ast,&accepted)
 analysis,cell:=accepted.Analysis,accepted.Folds
 generation:=cell.Current();backing:=&generation.Entries[0]
 owner,epoch:=accepted.CatalogOwner,accepted.CatalogEpoch
 value:=generation.Entries[0].Value
 for _,tc:=range []struct{q,code string}{
  {`SELECT xmlforest($1 AS a)`,"42P18"},
  {`SELECT xmlelement(NAME r,XMLATTRIBUTES($1 AS a))`,"42P18"},
  {`SELECT xmlelement(NAME r,$1)`,"42P18"},
  {`SELECT xmlforest($1 AS a,$1::integer AS b)`,"42P08"},
  {`SELECT xmlforest($1 AS a),$1::integer`,"42P08"},
  {`SELECT xmlelement(NAME r,xmlforest($1 AS a)),$1::integer`,"42P08"},
  {`SELECT xmlforest($1)`,"42601"},
  {`SELECT xmlelement(NAME r,XMLATTRIBUTES($1))`,"42601"},
  {`SELECT xmlforest($1 AS a,missing AS b)`,"42703"},
 }{
  op,err:=s.Acquire(context.Background());if err!=nil{t.Fatal(err)}
  rejected,err:=e.Describe(op,enginetest.Statement(t,tc.q));op.Done()
  var diagnostic interface{SQLState()string}
  if !errors.As(err,&diagnostic)||diagnostic.SQLState()!=tc.code||rejected.Analysis!=nil||rejected.Folds!=nil{t.Fatal("unresolved analysis published executable authority",tc.q,rejected,err)}
  current:=cell.Current()
  if accepted.Analysis!=analysis||accepted.CatalogOwner!=owner||accepted.CatalogEpoch!=epoch||current.Generation!=generation.Generation||&current.Entries[0]!=backing||!sqltype.SameDatum(current.Entries[0].Value,value){t.Fatal("failed Describe mutated accepted owner/fold backing")}
 }
 enginetest.PreparedFoldExecute(t,e,s,ast,&accepted)
 if accepted.Folds.Current().Generation!=generation.Generation||!sqltype.SameDatum(accepted.Folds.Current().Entries[0].Value,value){t.Fatal("healthy reuse replaced accepted fold")}
}
