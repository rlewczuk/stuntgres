package routine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestFTSConvertRoutineDeclarations(t *testing.T) {
	for _, tc := range []struct {
		oid, result             ids.OID
		name, signature, source string
		volatility              expr.Volatility
	}{
		{3745, 3614, "to_tsvector", "3734,25", "to_tsvector_byid", expr.Immutable},
		{3749, 3614, "to_tsvector", "25", "to_tsvector", expr.Stable},
		{3746, 3615, "to_tsquery", "3734,25", "to_tsquery_byid", expr.Immutable},
		{3750, 3615, "to_tsquery", "25", "to_tsquery", expr.Stable},
		{3747, 3615, "plainto_tsquery", "3734,25", "plainto_tsquery_byid", expr.Immutable},
		{3751, 3615, "plainto_tsquery", "25", "plainto_tsquery", expr.Stable},
		{5006, 3615, "phraseto_tsquery", "3734,25", "phraseto_tsquery_byid", expr.Immutable},
		{5001, 3615, "phraseto_tsquery", "25", "phraseto_tsquery", expr.Stable},
		{5007, 3615, "websearch_to_tsquery", "3734,25", "websearch_to_tsquery_byid", expr.Immutable},
		{5009, 3615, "websearch_to_tsquery", "25", "websearch_to_tsquery", expr.Stable},
	} {
		got := BuiltinRoutine(tc.oid)
		identity := catalog.RoutineIdentity{ID: tc.oid, Namespace: ids.PGCatalogNamespaceOID, Name: tc.name, Signature: catalog.ProcedureSignature(tc.signature)}
		if got == nil || got.Identity != identity || got.Result != tc.result || got.Kind != catalog.FunctionRoutine || !got.Strict || got.Volatility != tc.volatility || got.Parallel != 's' || got.ReturnsSet || got.Defaults != 0 || got.Variadic != 0 || got.Outputs != nil || got.Providers != functions.FTSQueryRoutineProvider {
			t.Fatalf("conversion declaration %d: %+v", tc.oid, got)
		}
		display := builtinRoutineDisplay[tc.oid]
		if display.Language != catalog.InternalRoutineLanguage || display.Source != tc.source || display.Cost != 100 || BuiltinRoutineCost(got) != 100 {
			t.Fatal(tc, display)
		}
	}
	for _, oid := range []ids.OID{4209, 4210, 4211, 4212} {
		if BuiltinRoutine(oid) != nil {
			t.Fatal("JSON overload registered", oid)
		}
	}
}
