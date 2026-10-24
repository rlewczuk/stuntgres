package engine

import (
 "context"
 "testing"

 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/exec"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/extension"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/plan"
 "stuntgres/internal/engine/stored"
 "stuntgres/internal/sqltype"
)

func TestCitextRegexpRemap(t *testing.T) {
 source,sourceExtension,sourceType:=citextUnitInstalled(t);sourceView:=source.CatalogRead()
 target:=New();owner,closeOwner:=extensionTemporaryOperation(t,target,"postgres");t.Cleanup(closeOwner)
 namespace:=intarrayUnitTemporaryNamespace(t,target,owner)
 installer,closeInstaller:=extensionTemporaryOperation(t,target,"postgres")
 extensionUnitPublish(t,target,installer,citextUnitStage(t,target,installer,"unaccent",namespace.Name()));closeInstaller()
 if _,err:=target.ReplaceCatalog(context.Background(),source,sourceView);err!=nil{t.Fatal(err)}
 view:=target.CatalogRead();installation:=view.MustExtension(view.NameAddress(ids.NameKey{ClassID:ids.ExtensionClassID,Name:"citext"}));imported:=view.MustType(installation.Members[0])
 if imported.SQLType.ID()==sourceType.SQLType.ID(){t.Fatal("fixture did not displace allocator")}
 candidate:=catalog.CloneState(view.State());candidate.SetScalarImport(sourceType.SQLType,imported.SQLType);view=view.WithState(candidate)
 mapType:=func(old *sqltype.Descriptor)*sqltype.Descriptor{if old==nil{return nil};if old==sourceType.SQLType{return imported.SQLType};current,_:=view.LookupType(old.ID());return current}
 mapping:=map[ids.ObjectAddress]ids.ObjectAddress{sourceExtension.Address():installation.Address()}
 for i,address:=range sourceExtension.Members{mapping[address]=installation.Members[i]}
 mapping[ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(sourceType.Array)}]=ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(imported.Array)}
 mapper:=stored.ViewQueryRemapper{Catalog:view,Type:mapType,Ref:func(ref ids.ObjectRef)ids.ObjectRef{address:=ref.Address;if mapped,ok:=mapping[address];ok{address=mapped};return view.Owner().Domain.Ref(view.Epoch(),address)},Procedure:extension.ProcedureRemapper(mapping,replacementProcedurePolicyFixture(source,target),nil)}
 for index:=range extension.CitextRegexpMembers {
 call:=citextRegexpUnitCall(t,sourceView,sourceExtension,sourceType,index)
 original:=expr.Expr(functions.Regexp{RegexpCall:call});if call.Plan.ReturnsSet(){original=functions.RegexpSet{RegexpCall:call}}
 query,err:=mapper.Query(&plan.ValuesNode{Rows:[][]expr.Expr{{original}}});if err!=nil{t.Fatal("mapped original",index,err)}
 node:=query.(*plan.ValuesNode).Rows[0][0];var mapped functions.RegexpCall;switch n:=node.(type){case functions.Regexp:mapped=n.RegexpCall;case functions.RegexpSet:mapped=n.RegexpCall;default:t.Fatalf("lost wrapper %T",node)}
 current:=view.MustProcedure(installation.Members[42+index]);captured,ok:=mapped.RoutineReference.(*catalog.Procedure)
 if !ok||!exec.SameRoutineDeclaration(current,captured)||captured.Revision()!=call.RoutineReference.(*catalog.Procedure).Revision()||mapped.Citext.Ref!=view.Owner().Domain.Ref(view.Epoch(),current.Address())||mapped.Args[0].ResultUsage().Type!=imported.SQLType||mapped.Args[1].ResultUsage().Type!=imported.SQLType{t.Fatal("lost capture",index,mapped)}
 if call.Args[0].ResultUsage().Type!=sourceType.SQLType||call.Citext.Ref.Domain()!=sourceView.Owner().Domain{t.Fatal("source mutated")}
 badMapper:=mapper
 badMapper.Procedure=func(original *catalog.Procedure)(*catalog.Procedure,error){mapped,err:=mapper.Procedure(original);if err!=nil{return nil,err};wrong:=*mapped;wrong.Installed.Rows++;return &wrong,nil}
 if _,err:=badMapper.Query(&plan.ValuesNode{Rows:[][]expr.Expr{{original}}});err==nil{t.Fatal("mapped-original mismatch admitted")}
 relocated,err:=stored.RemapExpression(original,mapper.Ref,nil);if err!=nil{t.Fatal(err)}
 if _,_,err:=stored.RetagTypes(view,relocated,mapType);err==nil{t.Fatal("name-only authority rebinding admitted")}
 }
}
