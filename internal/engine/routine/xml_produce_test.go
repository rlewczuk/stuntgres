package routine

import (
 "testing"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/sqltype"
)

func TestXMLProduceRoutineInventory(t *testing.T){
 for _,tc:=range []struct{name string;oid ids.OID}{{"xmltext",3813},{"xmlcomment",2895}}{
  declaration,err:=LookupBuiltinRoutineDeclaration(functions.CatalogRoutineProvider,tc.name,[]sqltype.ID{sqltype.Text})
  if err!=nil{t.Fatal(err)}
  want:=catalog.RoutineIdentity{tc.oid,ids.PGCatalogNamespaceOID,tc.name,"25"}
  if declaration.Identity!=want||declaration.Kind!=catalog.FunctionRoutine||declaration.Result!=142||!declaration.Strict||declaration.Volatility!=expr.Immutable||declaration.Parallel!='s'||declaration.ReturnsSet||declaration.Defaults!=0||declaration.Variadic!=0||declaration.Providers!=functions.CatalogRoutineProvider{t.Fatalf("released XML routine declaration: %+v",declaration)}
 }
 for _,name:=range []string{"xmlconcat","xmlelement","xmlforest","xmlpi","xmlroot"}{if _,err:=LookupBuiltinRoutineDeclaration(functions.CatalogRoutineProvider,name,[]sqltype.ID{sqltype.XML});err==nil{t.Fatal("special syntax acquired invented routine authority",name)}}
}
