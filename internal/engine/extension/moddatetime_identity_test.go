package extension_test

import (
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/extension"
 "stuntgres/internal/engine/ids"
)

func TestModdatetimeProviderIdentity(t *testing.T) {
 create:=func()(catalog.Read,*catalog.Procedure){
  e:=engine.New();op,done:=enginetest.RoleOperation(t,e,"postgres");defer done()
  enginetest.RoleExecute(t,e,op,"CREATE EXTENSION moddatetime")
  enginetest.RoleExecute(t,e,op,"CREATE SCHEMA md_moved")
  v:=e.CatalogRead();ext:=v.MustExtension(v.NameAddress(ids.NameKey{ClassID:ids.ExtensionClassID,Name:"moddatetime"}))
  if len(ext.Members)!=1 {t.Fatal("release must have exactly one member",ext)}
  return v,v.MustProcedure(ext.Members[0])
 }
 v,p:=create()
 if symbol,err:=extension.ProcedureSymbol(v,p);err!=nil||symbol!="moddatetime" {t.Fatal(symbol,err)}
 for _,change:=range []func(*catalog.Procedure){
  func(p *catalog.Procedure){p.Origin=nil},
  func(p *catalog.Procedure){origin:=*p.Origin;installation:=*origin.Installation;origin.Installation=&installation;p.Origin=&origin},
  func(p *catalog.Procedure){p.Display.Source="foreign"},
  func(p *catalog.Procedure){p.Result=23},
  func(p *catalog.Procedure){p.Strict=true},
  func(p *catalog.Procedure){p.Signature="25"},
 } {
  bad:=*p;change(&bad)
  v.State().SetDescriptor(p.Address(),&bad)
  if _,err:=extension.ProcedureSymbol(v,&bad);err==nil {t.Fatal("forgery admitted",bad)}
  v.State().SetDescriptor(p.Address(),p)
 }
 foreign,_:=create();if _,err:=extension.ProcedureSymbol(foreign,p);err==nil {t.Fatal("foreign declaration admitted")}
 renamed:=*p;renamed.ObjectName="renamed";renamed.Extension=ids.ObjectAddress{}
 renamed.ObjectNamespace=v.NameAddress(ids.NameKey{ClassID:ids.NamespaceClassID,Name:"md_moved"})
 v.State().UnbindName(catalog.DescriptorNameKey(p));v.State().SetDescriptor(p.Address(),&renamed);v.State().BindName(catalog.DescriptorNameKey(&renamed),p.Address())
 if symbol,err:=extension.ProcedureSymbol(v,&renamed);err!=nil||symbol!="moddatetime" {t.Fatal("authentic detached rename rejected",symbol,err)}
 if _,err:=extension.ProcedureSymbol(v,p);err==nil {t.Fatal("stale declaration admitted")}
}
