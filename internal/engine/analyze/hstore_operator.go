package analyze

import (
 "fmt"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/extension"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/sqltype"
)

func (b *Binder) hstoreOperatorProcedure(name string,typ *sqltype.Descriptor)(*catalog.Procedure,error){
 view:=b.BindingCatalog()
 base,ok:=view.Type(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(typ.ID())});if !ok||base.SQLType!=typ{return nil,fmt.Errorf("stuntgres: foreign hstore comparison type")}
 probe:=&catalog.Operator{ObjectAddress:ids.ObjectAddress{ClassID:ids.OperatorClassID},ObjectNamespace:base.Namespace(),ObjectName:name,Left:ids.OID(typ.ID()),Right:ids.OID(typ.ID())}
 operator,ok:=view.Operator(view.NameAddress(catalog.DescriptorNameKey(probe)));if !ok{return nil,fmt.Errorf("stuntgres: missing installed hstore operator")}
 p,ok:=view.Procedure(ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:operator.Function});if !ok||extension.HstoreProviderOf(p)!=extension.StockHstore||operator.Extension!=base.Extension{return nil,fmt.Errorf("stuntgres: invalid installed hstore operator")}
 if err:=extension.StockHstore.ValidateProcedure(view,p);err!=nil{return nil,err}
 for _,d:=range []catalog.Descriptor{operator,p}{b.dependency(view.Owner().Domain.Ref(view.Epoch(),d.Address()),d.Revision())}
 return p,nil
}
