package tablecmds_test

import (
 "context"
 "errors"
 "reflect"
 "sync"
 "testing"

 "stuntgres/internal/engine"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/ids"
)

type partitionRenameRejectProvider struct{catalog.Provider;failure error;entered *bool}
func(p partitionRenameRejectProvider)Validate(v catalog.Read,d catalog.Descriptor)error{if err:=p.Provider.Validate(v,d);err!=nil{return err};*p.entered=true;return p.failure}

func TestPartitionRenameColumnPublication(t *testing.T){
 for _,kind:=range []string{"cancel","reset","authority","descriptor","provider"}{t.Run(kind,func(t *testing.T){
  e:=partitionRenameEngine(t);old:=e.Catalog.State
  originals:=make(map[ids.ObjectAddress]*catalog.Relation);stores:=make(map[ids.ObjectAddress]any)
  for _,name:=range partitionRenameMembers{ref:=enginetest.RoleTableRef(t,e,name);originals[ref.Address]=old.MustRelation(ref.Address);stores[ref.Address]=e.Storage.Relations[ref.Address]}
  ctx,cancel:=context.WithCancel(context.Background());defer cancel()
  provider:=e.Catalog.Providers[ids.RelationClassID];entered:=false;var once sync.Once
  failure:=errors.New("partition rename provider rejected")
  e.Catalog.Providers[ids.RelationClassID]=alterDropProvider{Provider:provider,callback:func(){once.Do(func(){
   entered=true;e.Catalog.Providers[ids.RelationClassID]=provider
   switch kind{
   case "cancel":cancel()
   case "reset":if err:=engine.Reset(context.Background(),e);err!=nil{t.Fatal(err)}
   case "authority":enginetest.Run(t,e,"CREATE ROLE pr_concurrent_authority")
   case "descriptor":
    ref:=enginetest.RoleTableRef(t,e,"pr_h1")
    next:=*originals[ref.Address];next.ObjectRevision++;next.Comment="external winner"
    stage:=e.BeginCatalogStage(e.CatalogRead());defer e.DiscardCatalogStage(stage)
    if err:=stage.Replace(&next);err!=nil{t.Fatal(err)}
    prepared,err:=e.ValidateCatalogStage(context.Background(),stage);if err!=nil{t.Fatal(err)}
    if _,err:=enginetest.PublishCatalog(e,prepared,core.PublicationToken{Epoch:ref.Epoch});err!=nil{t.Fatal(err)}
   }
  })}}
  if kind=="provider"{e.Catalog.Providers[ids.RelationClassID]=partitionRenameRejectProvider{provider,failure,&entered}}
  _,err:=enginetest.Execute(e,ctx,enginetest.Statement(t,"ALTER TABLE pr_root RENAME k TO renamed"),nil)
  if !entered{t.Fatal("rename failed before real publication provider admission",err)}
  if err==nil{t.Fatal("invalid publication accepted")}
  switch kind{case "cancel":if !errors.Is(err,context.Canceled){t.Fatal(err)};case "reset":if !errors.Is(err,ids.ErrCatalogReplaced){t.Fatal(err)}}
  if kind=="provider"&&!errors.Is(err,failure){t.Fatal("provider rejection replaced",err)}
  for address,original:=range originals{
   if old.MustRelation(address)!=original||original.Columns[0].Name!="k"{t.Fatal("old reader changed")}
   if kind=="reset"{continue}
   next:=e.Catalog.State.MustRelation(address)
   if next.Columns[0].Name!="k"||e.Storage.Relations[address]!=stores[address]{t.Fatal("partial publication",next)}
   if kind=="descriptor"&&original.Name()=="pr_h1"{if next.Comment!="external winner"||next==original{t.Fatal("external winner lost")}}else if next!=original{t.Fatal("failed rename replaced descriptor",original,next)}
  }
  if kind=="authority"{enginetest.Run(t,e,"SELECT oid FROM pg_roles WHERE rolname='pr_concurrent_authority'")}
  if kind=="provider"{e.Catalog.Providers[ids.RelationClassID]=provider}
  if kind!="reset"{
   enginetest.Run(t,e,"ALTER TABLE pr_root RENAME k TO renamed")
   for address,original:=range originals{next:=e.Catalog.State.MustRelation(address);if next.Columns[0].Name!="renamed"||!reflect.DeepEqual(original.Attributes,next.Attributes){t.Fatal("subsequent valid SQL failed",next)}}
  }
 })}
}
