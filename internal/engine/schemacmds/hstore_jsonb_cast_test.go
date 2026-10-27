package schemacmds_test

import (
 "slices"
 "strings"
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/access"
 "stuntgres/internal/engine/authorization"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/extension"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/sqltype"
)

func TestHstoreJSONBCastMembership(t *testing.T){
 e:=engine.New();op,done:=enginetest.RoleOperation(t,e,"postgres");defer done()
 stage:=hstoreArraysStage(t,e,op);publication,err:=e.ValidateCatalogStage(t.Context(),stage);if err!=nil{t.Fatal(err)}
 if _,err:=e.PublishCatalogOperation(op,nil,publication,core.PublicationToken{Epoch:stage.Base.Epoch()});err!=nil{t.Fatal(err)}
 view:=e.CatalogRead();ext:=view.MustExtension(view.NameAddress(ids.NameKey{ClassID:ids.ExtensionClassID,Name:"hstore"}))
 if len(ext.Members)!=12{t.Fatalf("manifest roles %d want12",len(ext.Members))}
 unique:=map[ids.ObjectAddress]bool{}
 for i,a:=range ext.Members{
  class:=ids.ProcedureClassID;if i==0{class=ids.TypeClassID};if i==7 || i==8{class=ids.OperatorClassID};if i==11{class=ids.CastClassID}
  if a.ObjectID==0 || a.ClassID!=class || unique[a]{t.Fatalf("invalid manifest role%d=%v",i,a)};unique[a]=true
 }
 base:=view.MustType(ext.Members[0]);cast,ok:=view.Cast(base.SQLType.ID(),sqltype.JSONB)
 if !ok || cast.Address()!=ext.Members[11] || cast.Function!=ext.Members[9] || cast.Context!="e" || cast.Method!="f" || cast.GeneratedRange!=(ids.ObjectAddress{}) || cast.InputCast!=(ids.ObjectAddress{}) || cast.ResultCast!=(ids.ObjectAddress{}) || cast.Namespace()!=(ids.ObjectAddress{}) || cast.Revision()==0{t.Fatalf("installed cast %#v",cast)}
 if err:=(catalog.CastProvider{}).Validate(view,cast);err!=nil{t.Fatal(err)}
 membership:=catalog.ExtensionMembership(cast);if membership!=ext.Address(){t.Fatal("cast lacks reciprocal extension membership",membership)}
 owner,acl,columns:=access.DescriptorACL(cast)
 if owner!=(authorization.PrincipalRef{}) || acl.Explicit || len(acl.Entries)!=0 || columns!=nil{t.Fatal("cast acquired owner/ACL",owner,acl,columns)}
 metadata,err:=extension.OwnerMetadata(view,e.RoleStore.PublishedRead(&e.Catalog.Owner,view),ext);if err!=nil{t.Fatal(err)}
 owners,found,err:=extension.DecodeOwners(strings.TrimPrefix(strings.TrimSpace(metadata),"-- "));if err!=nil || !found || len(owners.Members)!=11{t.Fatal("ownerless cast must not acquire portable ownership",owners,err)}
 for slot,owner:=range owners.Members{if owner.Slot!=slot || owner.Kind=="cast"{t.Fatalf("portable owner role%d=%+v",slot,owner)}}
 foreign:=engine.New()
 foreignOp,foreignDone:=enginetest.RoleOperation(t,foreign,"postgres");defer foreignDone()
 foreignStage:=hstoreArraysStage(t,foreign,foreignOp)
 foreignPublication,err:=foreign.ValidateCatalogStage(t.Context(),foreignStage);if err!=nil{t.Fatal(err)}
 if _,err:=foreign.PublishCatalogOperation(foreignOp,nil,foreignPublication,core.PublicationToken{Epoch:foreignStage.Base.Epoch()});err!=nil{t.Fatal(err)}
 foreignCast,ok:=foreign.CatalogRead().Cast(base.SQLType.ID(),sqltype.JSONB)
 if !ok || foreignCast.Address()!=cast.Address() || foreignCast==cast{t.Fatal("foreign-catalog equal-OID fixture lost its distinguishing precondition")}
 if err:=(catalog.CastProvider{}).Validate(foreign.CatalogRead(),cast);err==nil{t.Fatal("equal numeric identities authorized foreign cast descriptor")}
 for _,cause:=range []string{"context","target","function","adapter","result_adapter","generated","extension","revision","member"}{t.Run(cause,func(t *testing.T){
  candidate:=catalog.CloneState(view.State());bad:=*cast;manifest:=*ext;manifest.Members=slices.Clone(ext.Members)
  switch cause{case "context":bad.Context="i";case "target":bad.Target=sqltype.JSON;case "function":bad.Function=ext.Members[10];case "adapter":bad.InputCast=cast.Address();case "result_adapter":bad.ResultCast=cast.Address();case "generated":bad.GeneratedRange=ext.Members[0];case "extension":bad.Extension=ids.ObjectAddress{};case "revision":bad.ObjectRevision=0;case "member":manifest.Members[11]=ext.Members[9]}
  candidate.SetDescriptor(bad.Address(),&bad);candidate.SetDescriptor(manifest.Address(),&manifest)
  if err:=extension.StockHstore.ValidateExtension(e.Catalog.Read(candidate),&manifest);err==nil{t.Fatal("corrupt installed cast accepted",cause)}
  current,ok:=view.Cast(base.SQLType.ID(),sqltype.JSONB);if !ok || current!=cast || view.MustExtension(ext.Address())!=ext{t.Fatal("candidate mutated retained catalog/index")}
 })}
}
