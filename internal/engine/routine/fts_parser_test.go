package routine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestFTSParserRoutineDeclarations(t *testing.T) {
	for _, tc := range []struct {
		oid                     ids.OID
		name, signature, source string
		volatility              expr.Volatility
	}{
		{3713, "ts_token_type", "26", "ts_token_type_byid", expr.Immutable}, {3714, "ts_token_type", "25", "ts_token_type_byname", expr.Stable},
		{3715, "ts_parse", "26,25", "ts_parse_byid", expr.Immutable}, {3716, "ts_parse", "25,25", "ts_parse_byname", expr.Stable},
	} {
		d := BuiltinRoutine(tc.oid)
		identity := catalog.RoutineIdentity{ID: tc.oid, Namespace: 11, Name: tc.name, Signature: catalog.ProcedureSignature(tc.signature)}
		if d == nil || d.Identity != identity || d.Result != 2249 || !d.ReturnsSet || !d.Strict || d.Volatility != tc.volatility || d.Parallel != 's' || d.Providers != functions.FTSParserRoutineProvider || d.Defaults != 0 || d.Variadic != 0 {
			t.Fatal("parser declaration", tc, d)
		}
		if display, ok := builtinRoutineDisplay[tc.oid]; !ok || display != (builtinDisplayFacts{Language: catalog.InternalRoutineLanguage, Source: tc.source}) {
			t.Fatal("parser display metadata", tc.oid, display)
		}
	}
	for _, tc := range []struct {
		oid ids.OID
		name, signature string
		result ids.OID
		inputs []sqltype.ID
	}{
		{3717,"prsd_start","2281,23",2281,[]sqltype.ID{2281,sqltype.Int4}},
		{3718,"prsd_nexttoken","2281,2281,2281",2281,[]sqltype.ID{2281,2281,2281}},
		{3719,"prsd_end","2281",2278,[]sqltype.ID{2281}},
		{3720,"prsd_headline","2281,2281,3615",2281,[]sqltype.ID{2281,2281,sqltype.TSQuery}},
		{3721,"prsd_lextype","2281",2281,[]sqltype.ID{2281}},
	} {
		d := BuiltinRoutine(tc.oid)
		identity := catalog.RoutineIdentity{ID:tc.oid,Namespace:11,Name:tc.name,Signature:catalog.ProcedureSignature(tc.signature)}
		if d == nil || d.Identity != identity || d.Kind != catalog.FunctionRoutine || d.Result != tc.result || d.ReturnsSet || !d.Strict || d.Volatility != expr.Immutable || d.Parallel != 's' || d.Providers != 0 || d.Defaults != 0 || d.Variadic != 0 || d.Outputs != nil {
			t.Fatal("internal handler lacks complete non-executable metadata",tc,d)
		}
		if display,ok := builtinRoutineDisplay[tc.oid];!ok || display != (builtinDisplayFacts{Language:catalog.InternalRoutineLanguage,Source:tc.name}) {
			t.Fatal("internal handler display metadata",tc.oid,display)
		}
		for _, provider := range []expr.RoutineProvider{functions.FTSParserRoutineProvider,functions.CatalogRoutineProvider} {
			if _,err := LookupBuiltinRoutineDeclaration(provider,tc.name,tc.inputs);err == nil {
				t.Fatal("internal handler acquired SQL execution authority",tc.oid,provider)
			}
		}
	}
}
