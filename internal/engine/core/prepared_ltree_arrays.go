package core

import (
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/extension"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/sqltype"
)

// Shape is eligibility only; every import additionally proves both captured
// declarations/dependencies and the genuine current installed association.
func preparedLtreeArrayShape(d *sqltype.Descriptor) bool {
 return d != nil && d.Concrete() && d.IsArray() && (d.Element().IsLtree() || d.Element().IsLquery())
}
func (a *Analysis) capturedLtreeArray(read catalog.Read, source *sqltype.Descriptor) (*catalog.Type,*catalog.Type,bool) {
 if a==nil || !preparedLtreeArrayShape(source) || a.Domain!=read.Owner().Domain || a.Epoch!=read.Epoch(){return nil,nil,false}
 address:=ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(source.ID())}
 array:=a.Types[address]
 scalarAddress:=ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(source.Element().ID())}
 scalar:=a.Types[scalarAddress]
 if array==nil || scalar==nil || array.Address()!=address || scalar.Address()!=scalarAddress || array.SQLType!=source || scalar.SQLType!=source.Element() || scalar.Array!=source.ID() || scalar.Extension==(ids.ObjectAddress{}) || array.Extension!=(ids.ObjectAddress{}) || array.Owner!=scalar.Owner || array.Namespace()!=scalar.Namespace(){return nil,nil,false}
 for _,typ:=range []*catalog.Type{array,scalar}{
  found:=false
  for _,dep:=range a.Dependencies{
   if dep.Ref.Address==typ.Address() && dep.Ref.Domain()==a.Domain && dep.Ref.Epoch==a.Epoch && dep.Revision==DefinitionStamp(typ,PreparedEnvironment{}){found=true;break}
  }
  if !found{return nil,nil,false}
 }
 return array,scalar,true
}
func(a *Analysis) currentLtreeArray(read catalog.Read,source *sqltype.Descriptor)(*catalog.Type,*catalog.Type,bool){
 oldArray,oldScalar,ok:=a.capturedLtreeArray(read,source);if !ok{return nil,nil,false}
 array,ok:=read.Type(oldArray.Address());if !ok || !preparedLtreeArrayShape(array.SQLType){return nil,nil,false}
 scalar,ok:=read.Type(oldScalar.Address());if !ok || scalar.Extension!=oldScalar.Extension || scalar.SQLType.IsLtree()!=oldScalar.SQLType.IsLtree() || scalar.SQLType.IsLquery()!=oldScalar.SQLType.IsLquery() || array.SQLType.Element()!=scalar.SQLType || scalar.Array!=array.SQLType.ID(){return nil,nil,false}
 for _,pair:=range [][2]*catalog.Type{{oldArray,array},{oldScalar,scalar}}{
  nominal:=*pair[0];nominal.Owner,nominal.ACL=pair[1].Owner,pair[1].ACL
  if !catalog.SamePreparedTypeDefinition(&nominal,pair[1]){return nil,nil,false}
 }
 installed,ok:=read.Extension(scalar.Extension);if !ok || extension.StockLtree.ValidateExtension(read,installed)!=nil{return nil,nil,false}
 if _,err:=extension.MemberTypeRole(read,installed,scalar);err!=nil{return nil,nil,false}
 return array,scalar,true
}
