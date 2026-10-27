package schemacmds_test

import (
 "context"
 "strings"
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/extension"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/routine"
 "stuntgres/internal/sqltype"
)

func hstoreUnitInstalled(t *testing.T)(*core.Engine,*catalog.Extension,*catalog.Type){
 t.Helper();e:=engine.New();op,done:=enginetest.RoleOperation(t,e,"postgres");defer done()
 stage:=hstoreArraysStage(t,e,op);publication,err:=e.ValidateCatalogStage(t.Context(),stage);if err!=nil{t.Fatal(err)}
 if _,err:=e.PublishCatalogOperation(op,nil,publication,core.PublicationToken{Epoch:stage.Base.Epoch()});err!=nil{t.Fatal(err)}
 view:=e.CatalogRead();ext:=view.MustExtension(view.NameAddress(ids.NameKey{ClassID:ids.ExtensionClassID,Name:"hstore"}))
 return e,ext,view.MustType(ext.Members[extension.HstoreTypeSlot])
}

func TestHstoreComparisonIndexSupport(t *testing.T){
 e,ext,base:=hstoreUnitInstalled(t);view:=e.CatalogRead()
 metadata,err:=extension.OwnerMetadata(view,e.RoleStore.PublishedRead(&e.Catalog.Owner,view),ext);if err!=nil{t.Fatal(err)}
 owners,found,err:=extension.DecodeOwners(strings.TrimPrefix(metadata,"-- "));if err!=nil||!found{t.Fatal("symbolic owners",err)}
 imported:=catalog.CloneState(view.State())
 roles:=e.RoleStore.PublishedRead(&e.Catalog.Owner,view)
 if _,err:=extension.ImportOwners(context.Background(),view.WithState(imported),roles,[]extension.OwnerRecord{owners});err!=nil{t.Fatal("authentic symbolic owners failed import",err)}
 for _,slot:=range []int{extension.HstoreBtreeClassSlot,extension.HstoreHashClassSlot,extension.HstoreBtreeFamilySlot,extension.HstoreHashFamilySlot}{
  bad:=owners;bad.Members=append([]extension.MemberOwner(nil),owners.Members...)
  for i:=range bad.Members{if bad.Members[i].Slot==slot{bad.Members[i].Signature="wrong_method"}}
  candidate:=catalog.CloneState(view.State())
  if _,err:=extension.ImportOwners(context.Background(),view.WithState(candidate),roles,[]extension.OwnerRecord{bad});err==nil{t.Fatal("corrupt class/family symbolic method accepted")}
 }
 classes,families:=0,0
 for _,m:=range owners.Members{switch m.Kind{case "operator_class":classes++;case "operator_family":families++};if m.Slot>=extension.HstoreComparisonProcedureStart&&m.Slot<extension.HstoreComparisonOperatorStart{want:="hstore,hstore";if m.Slot==extension.HstoreComparisonProcedureStart+7{want="hstore"};if m.Slot==extension.HstoreComparisonProcedureStart+8{want="hstore,20"};if m.Signature!=want{t.Fatal("symbolic routine signature",m)}}}
 if classes!=2||families!=2{t.Fatal("missing owner roles",classes,families)}
 for _,method:=range []ids.OID{403,405}{
  class,ok,err:=view.DefaultIndexClass(method,base.SQLType);if err!=nil||!ok{t.Fatal("default class",method,err)}
  bound,err:=routine.BindClassSupport(view,class);if err!=nil{t.Fatal(err)}
  count:=1;first:=6;if method==405{count=2;first=7}
  for slot:=1;slot<=count;slot++{s:=bound.Support(slot);if s.Provider!=sqltype.HstoreIndexSupport||s.Routine!=uint32(ext.Members[extension.HstoreComparisonProcedureStart+first+slot-1].ObjectID)||s.Member==0||s.Revision==0||s.FamilyRevision==0||s.RoutineRevision==0{t.Fatal("incomplete genuine support",s)}}
  if method==403{order,err:=routine.BindIndexOrdering(view,base.SQLType.Usage(),class);if err!=nil{t.Fatal(err)};a,err:=base.SQLType.Input(context.Background(),"a=>bc",-1);if err!=nil{t.Fatal(err)};b,err:=base.SQLType.Input(context.Background(),"ab=>c",-1);if err!=nil{t.Fatal(err)};if n,err:=order.Compare(context.Background(),a,b,sqltype.InputEnvironment{});err!=nil||n!=-1{t.Fatal("typed ordering",n,err)}}
  familySlot:=extension.HstoreBtreeFamilySlot;if method==405{familySlot=extension.HstoreHashFamilySlot}
  for _,corrupt:=range []func(*catalog.State){
   func(s *catalog.State){m,_:=view.FamilySupport(ext.Members[familySlot].ObjectID,ids.OID(base.SQLType.ID()),ids.OID(base.SQLType.ID()),1);n:=*m;n.Function=ext.Members[extension.HstoreComparisonProcedureStart].ObjectID;s.SetDescriptor(n.Address(),&n)},
   func(s *catalog.State){m,_:=view.FamilySupport(ext.Members[familySlot].ObjectID,ids.OID(base.SQLType.ID()),ids.OID(base.SQLType.ID()),1);n:=*m;n.InternalOwner=ext.Members[familySlot];s.SetDescriptor(n.Address(),&n)},
   func(s *catalog.State){p:=*view.MustProcedure(ext.Members[extension.HstoreComparisonProcedureStart+first]);p.Result=25;s.SetDescriptor(p.Address(),&p)},
   func(s *catalog.State){p:=*view.MustProcedure(ext.Members[extension.HstoreComparisonProcedureStart+first]);p.Implementation=extension.StockCube;s.SetDescriptor(p.Address(),&p)},
  }{candidate:=catalog.CloneState(view.State());corrupt(candidate);if _,err:=routine.BindClassSupport(view.WithState(candidate),class);err==nil{t.Fatal("invalid installed support accepted",method)}}
  if method==405 {
   candidate:=catalog.CloneState(view.State())
   second,_:=view.FamilySupport(ext.Members[familySlot].ObjectID,ids.OID(base.SQLType.ID()),ids.OID(base.SQLType.ID()),2)
   bad:=*second;bad.Function=ext.Members[extension.HstoreComparisonProcedureStart+7].ObjectID
   candidate.SetDescriptor(bad.Address(),&bad)
   if _,err:=routine.BindClassSupport(view.WithState(candidate),class);err==nil{t.Fatal("unseeded routine substituted for seeded support")}
   p:=*view.MustProcedure(ext.Members[extension.HstoreComparisonProcedureStart+8]);p.Signature=extension.HstoreSignature(base.SQLType.ID())+",23"
   candidate=catalog.CloneState(view.State());candidate.SetDescriptor(p.Address(),&p)
   if _,err:=routine.BindClassSupport(view.WithState(candidate),class);err==nil{t.Fatal("wrong seed formal accepted")}
  }
 }
}
