package routine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

func TestFTSParserRoutineDeclarations(t *testing.T) {
	for _,tc:=range []struct { oid ids.OID; name,signature,source string; volatility expr.Volatility }{
		{3713,"ts_token_type","26","ts_token_type_byid",expr.Immutable},{3714,"ts_token_type","25","ts_token_type_byname",expr.Stable},
		{3715,"ts_parse","26,25","ts_parse_byid",expr.Immutable},{3716,"ts_parse","25,25","ts_parse_byname",expr.Stable},
	} {
		d:=BuiltinRoutine(tc.oid)
		identity:=catalog.RoutineIdentity{ID:tc.oid,Namespace:11,Name:tc.name,Signature:catalog.ProcedureSignature(tc.signature)}
		if d==nil || d.Identity!=identity || d.Result!=2249 || !d.ReturnsSet || !d.Strict || d.Volatility!=tc.volatility || d.Parallel!='s' || d.Providers!=functions.FTSParserRoutineProvider || d.Defaults!=0 || d.Variadic!=0 { t.Fatal("parser declaration",tc,d) }
		if display,ok:=builtinRoutineDisplay[tc.oid]; !ok || display!=(catalog.RoutineDisplay{Language:catalog.InternalRoutineLanguage,Source:tc.source}) { t.Fatal("parser display metadata",tc.oid,display) }
	}
	for _,oid:=range []ids.OID{3717,3718,3719,3720,3721} {
		if BuiltinRoutine(oid)!=nil { t.Fatal("internal handler became SQL executable",oid) }
	}
}
