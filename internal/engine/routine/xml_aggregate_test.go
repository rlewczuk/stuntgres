package routine

import (
	"testing"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

func TestXMLAggregateRoutineDeclaration(t *testing.T){
	d:=BuiltinRoutine(2901)
	want:=catalog.RoutineIdentity{2901,ids.PGCatalogNamespaceOID,"xmlagg","142"}
	if d==nil{t.Fatal("missing XML aggregate")}
	if d.Identity!=want||d.Kind!=catalog.AggregateRoutine||d.Result!=142||d.Strict||d.Volatility!=expr.Immutable||d.Parallel!='s'||d.ReturnsSet||d.Defaults!=0||d.Variadic!=0||d.Providers!=functions.AggregateRoutineProvider{t.Fatalf("declaration %+v",d)}
	if ObjectRoutineKind(2901)!="aggregate"{t.Fatal("wrong object class")}
	display,ok:=builtinRoutineDisplay[2901]
	if !ok||display.Language!=catalog.InternalRoutineLanguage||display.Source!="aggregate_dummy"{t.Fatalf("display %+v",display)}
}
