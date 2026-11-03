package routine

import (
	"testing"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

func TestXMLMapScopeRoutineDeclarations(t *testing.T){
	for _,tc:=range []struct{oid ids.OID;name,signature string;labels []string}{
		{2933,"schema_to_xml","19,16,16,25",[]string{"schema","nulls","tableforest","targetns"}},
		{2934,"schema_to_xmlschema","19,16,16,25",[]string{"schema","nulls","tableforest","targetns"}},
		{2935,"schema_to_xml_and_xmlschema","19,16,16,25",[]string{"schema","nulls","tableforest","targetns"}},
		{2936,"database_to_xml","16,16,25",[]string{"nulls","tableforest","targetns"}},
		{2937,"database_to_xmlschema","16,16,25",[]string{"nulls","tableforest","targetns"}},
		{2938,"database_to_xml_and_xmlschema","16,16,25",[]string{"nulls","tableforest","targetns"}},
	}{
		d:=BuiltinRoutine(tc.oid);want:=catalog.RoutineIdentity{ID:tc.oid,Namespace:ids.PGCatalogNamespaceOID,Name:tc.name,Signature:catalog.ProcedureSignature(tc.signature)}
		if d==nil||d.Identity!=want||d.Result!=142||d.Kind!=catalog.FunctionRoutine||!d.Strict||d.Volatility!=expr.Stable||d.Parallel!='r'||d.ReturnsSet||d.Defaults!=0||d.Variadic!=0||d.Providers!=functions.XMLMappingRoutineProvider||d.Outputs!=nil{t.Fatal("released scope declaration",tc,d)}
		display:=builtinRoutineDisplay[tc.oid];if display.Language!=catalog.InternalRoutineLanguage||display.Source!=tc.name||display.Cost!=100||BuiltinRoutineCost(d)!=100{t.Fatal("released scope display",tc,display)}
		names:=builtinRoutineInputNames[tc.oid];if names==nil||names.Len()!=len(tc.labels){t.Fatal("scope labels",tc)};for i,label:=range tc.labels{if names.At(i)!=label{t.Fatal("scope formal",tc.oid,i)}}
	}
}
