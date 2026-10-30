package routine

import (
	"testing"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

func TestXMLMapRowsRoutineDeclarations(t *testing.T){
	for _,tc:=range []struct{oid ids.OID;name,signature string;volatility expr.Volatility;parallel byte;labels []string}{
		{2923,"table_to_xml","2205,16,16,25",expr.Stable,'r',[]string{"tbl","nulls","tableforest","targetns"}},
		{2924,"query_to_xml","25,16,16,25",expr.Volatile,'u',[]string{"query","nulls","tableforest","targetns"}},
		{2925,"cursor_to_xml","1790,23,16,16,25",expr.Volatile,'u',[]string{"cursor","count","nulls","tableforest","targetns"}},
		{2926,"table_to_xmlschema","2205,16,16,25",expr.Stable,'r',[]string{"tbl","nulls","tableforest","targetns"}},
		{2927,"query_to_xmlschema","25,16,16,25",expr.Volatile,'u',[]string{"query","nulls","tableforest","targetns"}},
		{2928,"cursor_to_xmlschema","1790,16,16,25",expr.Volatile,'u',[]string{"cursor","nulls","tableforest","targetns"}},
		{2929,"table_to_xml_and_xmlschema","2205,16,16,25",expr.Stable,'r',[]string{"tbl","nulls","tableforest","targetns"}},
		{2930,"query_to_xml_and_xmlschema","25,16,16,25",expr.Volatile,'u',[]string{"query","nulls","tableforest","targetns"}},
	}{d:=BuiltinRoutine(tc.oid);identity:=catalog.RoutineIdentity{ID:tc.oid,Namespace:ids.PGCatalogNamespaceOID,Name:tc.name,Signature:catalog.ProcedureSignature(tc.signature)}
		if d==nil||d.Identity!=identity||d.Result!=142||d.Kind!=catalog.FunctionRoutine||!d.Strict||d.Volatility!=tc.volatility||d.Parallel!=tc.parallel||d.ReturnsSet||d.Defaults!=0||d.Variadic!=0||d.Providers!=functions.XMLMappingRoutineProvider||d.Outputs!=nil{t.Fatal("released mapper declaration",tc,d)}
		display:=builtinRoutineDisplay[tc.oid];if display.Language!=catalog.InternalRoutineLanguage||display.Source!=tc.name||display.Cost!=100||BuiltinRoutineCost(d)!=100{t.Fatal("mapper display",tc,display)}
		names:=builtinRoutineInputNames[tc.oid];if names==nil||names.Len()!=len(tc.labels){t.Fatal("mapper labels",tc)};for i,want:=range tc.labels{if names.At(i)!=want{t.Fatal("mapper label",tc.oid,i)}}
	}
}
