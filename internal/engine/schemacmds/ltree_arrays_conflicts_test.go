package schemacmds_test

import (
 "errors"
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/extension"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/schemacmds"
 "stuntgres/internal/sqlstate"
)

// Install a genuine competing declaration in the unpublished catalog after the
// penultimate selected role. The normal stage collision path must reject it.
type ltreeArrayConflictStage struct {extension.Stage; operator bool}
func(s ltreeArrayConflictStage)Create(d catalog.Descriptor)error{
 if err:=s.Stage.Create(d);err!=nil{return err}
 var competitor catalog.Descriptor
 if proc,ok:=d.(*catalog.Procedure);ok && !s.operator && proc.Name()=="_ltree_extract_isparent"{
  copied:=*proc;copied.ObjectName="_ltree_extract_risparent";copied.Extension=ids.ObjectAddress{};competitor=&copied
 }
 if op,ok:=d.(*catalog.Operator);ok && s.operator && op.Name()=="?@>"{
  copied:=*op;copied.ObjectName="?<@";copied.Extension=ids.ObjectAddress{};competitor=&copied
 }
 if competitor==nil{return nil}
 oid,err:=s.AllocateOID();if err!=nil{return err};revision,err:=s.Revision();if err!=nil{return err}
 switch copied:=competitor.(type){case *catalog.Procedure:copied.ObjectAddress.ObjectID=oid;copied.ObjectRevision=revision;case *catalog.Operator:copied.ObjectAddress.ObjectID=oid;copied.ObjectRevision=revision}
 return s.Stage.Create(competitor)
}
func ltreeArrayProviderConflicts(t *testing.T){
 t.Helper()
 for _,operator:=range []bool{false,true}{
  e:=engine.New();op,done:=enginetest.RoleOperation(t,e,"postgres")
  before:=e.CatalogRead().State()
  e.Mu.Lock()
  err:=func()error{
   auth,err:=e.AuthorizationLocked(op);if err!=nil{return err}
   stage:=e.BeginCatalogStage(auth.Catalog);defer e.DiscardCatalogStage(stage);stage.AuthorizeLocked(auth,op.Session)
   oid,err:=e.Catalog.AllocateOIDLocked();if err!=nil{return err};revision,err:=e.Catalog.RevisionLocked();if err!=nil{return err}
   ext:=&catalog.Extension{ObjectAddress:ids.ObjectAddress{ClassID:ids.ExtensionClassID,ObjectID:oid},ObjectName:"ltree",ObjectRevision:revision,Version:"1.3",TargetNamespace:ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:ids.PublicNamespaceOID},Owner:auth.State.CurrentUser}
   if err:=stage.Create(ext);err!=nil{return err}
   host:=&schemacmds.ExtensionHost{Engine:e,Auth:auth,Stage:stage}
   return extension.StockLtree.Stage(ltreeArrayConflictStage{Stage:host,operator:operator},ext,ext.Owner)
  }()
  e.Mu.Unlock();done()
  var diagnostic *sqlstate.Error
  message:=`function "_ltree_extract_risparent" already exists with same argument types`;if operator{message="operator ?<@ already exists"}
  if !errors.As(err,&diagnostic) || diagnostic.Code!="42723" || diagnostic.Message!=message{t.Fatal("late selected collision was not rejected",operator,err)}
  if e.CatalogRead().State()!=before{t.Fatal("late collision published a prefix")}
 }
}
