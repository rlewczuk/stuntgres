package extension

import (
 "fmt"
 "stuntgres/internal/engine/authorization"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/sqlstate"
 "stuntgres/internal/sqltype"
)

func (p *HstoreProvider) stageComparisons(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef, typ *sqltype.Descriptor) error {
 for i:=range HstoreComparisonNames {
  revision,err:=stage.Revision();if err!=nil{return err}
  name,signature,result:=HstoreComparisonDeclaration(i,typ.ID())
  proc:=&catalog.Procedure{ObjectAddress:ext.Members[HstoreComparisonProcedureStart+i],ObjectNamespace:ext.TargetNamespace,Extension:ext.Address(),ObjectRevision:revision,ObjectName:name,Signature:signature,RoutineKind:catalog.FunctionRoutine,Owner:owner,Result:result,Volatility:expr.Immutable,Strict:true,Display:catalog.RoutineDisplay{Language:catalog.CRoutineLanguage,Source:name},Implementation:p}
  proc.Display.Binary, proc.Display.HasBinary = "$libdir/hstore", true
  if stage.LookupName(catalog.DescriptorNameKey(proc))!=(ids.ObjectAddress{}) {return &sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"42723",Message:fmt.Sprintf(`function "%s" already exists with same argument types`,name)}}
  if err:=stage.Create(proc);err!=nil{return err}
 }
 for i,name:=range hstoreComparisonOperators {
  revision,err:=stage.Revision();if err!=nil{return err}
  o:=&catalog.Operator{ObjectAddress:ext.Members[HstoreComparisonOperatorStart+i],ObjectNamespace:ext.TargetNamespace,Extension:ext.Address(),ObjectRevision:revision,ObjectName:name,Owner:owner,Left:ids.OID(typ.ID()),Right:ids.OID(typ.ID()),Result:16,Function:ext.Members[HstoreComparisonProcedureStart+i].ObjectID,Commutator:ext.Members[HstoreComparisonOperatorStart+cubeCommutators[i]].ObjectID,Negator:ext.Members[HstoreComparisonOperatorStart+cubeNegators[i]].ObjectID,Restriction:cubeRestrictions[i],Join:cubeJoins[i],Hash:i==0,Merge:i==0}
  if err:=stage.Create(o);err!=nil{return err}
 }
 for _,hash:=range []bool{false,true} {
  fs,cs,method,name:=HstoreBtreeFamilySlot,HstoreBtreeClassSlot,ids.OID(403),"btree_hstore_ops"
  strategies:=cubeStrategies[:];supports:=[]int{6}
  if hash {fs,cs,method,name=HstoreHashFamilySlot,HstoreHashClassSlot,405,"hash_hstore_ops";strategies=[]int{0};supports=[]int{7,8}}
  revision,err:=stage.Revision();if err!=nil{return err}
  family:=&catalog.OperatorFamily{ObjectAddress:ext.Members[fs],ObjectNamespace:ext.TargetNamespace,Extension:ext.Address(),ObjectRevision:revision,ObjectName:name,Method:method,Owner:owner}
  if err:=stage.Create(family);err!=nil{return err}
  revision,err=stage.Revision();if err!=nil{return err}
  class:=&catalog.OperatorClass{ObjectAddress:ext.Members[cs],ObjectNamespace:ext.TargetNamespace,Extension:ext.Address(),ObjectRevision:revision,ObjectName:name,Owner:owner,Method:method,Family:family.Address().ObjectID,InputType:ids.OID(typ.ID()),Default:true}
  if err:=stage.Create(class);err!=nil{return err}
  for i,op:=range strategies {
   oid,err:=stage.AllocateOID();if err!=nil{return err};revision,err:=stage.Revision();if err!=nil{return err}
   m:=&catalog.OperatorFamilyOperator{ObjectAddress:ids.ObjectAddress{ClassID:ids.AccessMethodOperatorClassID,ObjectID:oid},ObjectRevision:revision,Family:class.Family,Left:class.InputType,Right:class.InputType,Operator:ext.Members[HstoreComparisonOperatorStart+op].ObjectID,Method:method,InternalOwner:class.Address(),Number:int16(i+1),Purpose:'s'}
   if err:=stage.Create(m);err!=nil{return err}
  }
  for i,kind:=range supports {
   oid,err:=stage.AllocateOID();if err!=nil{return err};revision,err:=stage.Revision();if err!=nil{return err}
   m:=&catalog.OperatorFamilySupport{ObjectAddress:ids.ObjectAddress{ClassID:ids.AccessMethodProcedureClassID,ObjectID:oid},ObjectRevision:revision,Family:class.Family,Left:class.InputType,Right:class.InputType,Function:ext.Members[HstoreComparisonProcedureStart+kind].ObjectID,InternalOwner:class.Address(),Number:int16(i+1),CatalogVisible:true}
   if err:=stage.Create(m);err!=nil{return err}
  }
 }
 return nil
}
