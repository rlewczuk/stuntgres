package types_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"

	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqltype"
)


func TestXMLProducePreparedFoldOwnership(t *testing.T){
 e:=rootengine.New();s:=enginetest.StartSession(t,e,nil)
 ast,d:=enginetest.PreparedDescription(t,e,s,`SELECT xmltext('<&') AS tx,xmlcomment('owned') AS cm,xmlelement(NAME r,'live') AS el`)
 analysis:=d.Analysis;owner,epoch:=d.CatalogOwner,d.CatalogEpoch
 if analysis==nil||d.Folds==nil||len(d.Folds.Current().Entries)!=0{t.Fatal("Prepare published execution folds")}
 enginetest.PreparedFoldExecute(t,e,s,ast,&d)
 accepted:=d.Folds.Current();cell:=d.Folds
 callables:=maps.Clone(analysis.FoldCallables)
 if len(accepted.Entries)==0{t.Fatal("ordinary XML routines did not publish actual folds")}
 backing:=&accepted.Entries[0];var generated []sqltype.Value
 for _,entry:=range accepted.Entries{if entry.Value.Type()!=nil&&entry.Value.Type().ID()==sqltype.XML{generated=append(generated,entry.Value)}}
 if len(generated)<2{t.Fatal("missing genuine generated ordinary fold values",accepted.Entries)}
 for range 2{enginetest.PreparedFoldExecute(t,e,s,ast,&d);current:=cell.Current();if d.Analysis!=analysis||d.CatalogOwner!=owner||d.CatalogEpoch!=epoch||current.Generation!=accepted.Generation||&current.Entries[0]!=backing||!reflect.DeepEqual(analysis.FoldCallables,callables){t.Fatal("Execute replaced accepted definition/fold authority")};for i,entry:=range current.Entries{if !sqltype.SameDatum(entry.Value,accepted.Entries[i].Value){t.Fatal("Execute changed retained generated datum backing")}}}
 op,err:=s.Acquire(context.Background());if err!=nil{t.Fatal(err)};defer op.Done()
 e.Mu.Lock();auth,err:=e.AuthorizationLocked(op);if err!=nil{e.Mu.Unlock();t.Fatal(err)};env,err:=e.PreparedEnvironmentLocked(auth,true);if err!=nil{e.Mu.Unlock();t.Fatal(err)};publication,err:=core.BeginFoldPublication(&d,ast,env,auth.Catalog,nil);if err!=nil{e.Mu.Unlock();t.Fatal(err)}
 canceled,cancel:=context.WithCancel(context.Background());cancel();err=publication.Accept(canceled,auth.Catalog,env,e.Catalog.State.Epoch());e.Mu.Unlock()
 if !errors.Is(err,context.Canceled)||publication.Group.Sealed||cell.Current().Generation!=accepted.Generation||&cell.Current().Entries[0]!=backing{t.Fatal("rejected publication mutated accepted fold backing",err)}
 for i,entry:=range cell.Current().Entries{if !sqltype.SameDatum(entry.Value,accepted.Entries[i].Value){t.Fatal("rejected publication changed genuine datum")}}
}
