package routine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestFTSJSONRoutineDeclarations(t *testing.T) {
	for _, tc := range []struct {
		oid                     ids.OID
		name, signature, source string
		volatility              expr.Volatility
	}{
		{4209, "to_tsvector", "3802", "jsonb_string_to_tsvector", expr.Stable},
		{4210, "to_tsvector", "114", "json_string_to_tsvector", expr.Stable},
		{4211, "to_tsvector", "3734,3802", "jsonb_string_to_tsvector_byid", expr.Immutable},
		{4212, "to_tsvector", "3734,114", "json_string_to_tsvector_byid", expr.Immutable},
		{4213, "jsonb_to_tsvector", "3802,3802", "jsonb_to_tsvector", expr.Stable},
		{4214, "jsonb_to_tsvector", "3734,3802,3802", "jsonb_to_tsvector_byid", expr.Immutable},
		{4215, "json_to_tsvector", "114,3802", "json_to_tsvector", expr.Stable},
		{4216, "json_to_tsvector", "3734,114,3802", "json_to_tsvector_byid", expr.Immutable},
	} {
		got := BuiltinRoutine(tc.oid)
		identity := catalog.RoutineIdentity{ID: tc.oid, Namespace: ids.PGCatalogNamespaceOID, Name: tc.name, Signature: catalog.ProcedureSignature(tc.signature)}
		if got == nil || got.Identity != identity || got.Result != 3614 || got.Kind != catalog.FunctionRoutine || !got.Strict || got.Volatility != tc.volatility || got.Parallel != 's' || got.ReturnsSet || got.Defaults != 0 || got.Variadic != 0 || got.Outputs != nil || got.Providers != functions.FTSQueryRoutineProvider {
			t.Fatalf("JSON declaration %d: %+v", tc.oid, got)
		}
		display := builtinRoutineDisplay[tc.oid]
		if display.Language != catalog.InternalRoutineLanguage || display.Source != tc.source || display.Cost != 100 || BuiltinRoutineCost(got) != 100 {
			t.Fatal(tc, display)
		}
	}
}
