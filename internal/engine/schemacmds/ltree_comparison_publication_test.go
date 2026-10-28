package schemacmds_test

import (
 "context"
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/extension"
 "stuntgres/internal/engine/ids"
)

func TestLtreeComparisonPublication(t *testing.T){
 for _,corrupt:=range []func(catalog.Read,*catalog.Extension){
  func(view catalog.Read,ext *catalog.Extension){view.MustProcedure(ext.Members[extension.LtreeComparisonProcedureStart+8]).Display.Source="hash_ltree"},
  func(view catalog.Read,ext *catalog.Extension){base:=view.MustType(ext.Members[3]);member,_:=view.FamilySupport(ext.Members[extension.LtreeHashFamilySlot].ObjectID,ids.OID(base.SQLType.ID()),ids.OID(base.SQLType.ID()),2);member.InternalOwner=ext.Members[extension.LtreeHashClassSlot]},
 }{
  e:=engine.New();op,done:=enginetest.RoleOperation(t,e,"postgres")
  stage:=lqueryProviderStage(t,e,op);view:=e.StageRead(stage)
  ext:=view.MustExtension(view.NameAddress(ids.NameKey{ClassID:ids.ExtensionClassID,Name:"ltree"}))
  before:=e.CatalogRead().State();corrupt(view,ext)
  if _,err:=e.ValidateCatalogStage(context.Background(),stage);err==nil{t.Fatal("late invalid support graph accepted")}
  if e.CatalogRead().State()!=before||e.CatalogRead().NameAddress(ids.NameKey{ClassID:ids.ExtensionClassID,Name:"ltree"})!=(ids.ObjectAddress{}){t.Fatal("failed support validation published a prefix")}
  done()
 }
}
