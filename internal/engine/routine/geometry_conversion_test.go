package routine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

func TestGeometryConversionDeclarations(t *testing.T) {
	for _, tc := range []struct {
		oid             ids.OID
		name, signature string
		result          ids.OID
	}{
		{1421, "box", "600,600", 603},
		{1446, "box", "604", 603},
		{1473, "circle", "600,701", 718},
		{1474, "circle", "604", 718},
		{1479, "circle", "603", 718},
		{1480, "box", "718", 603},
		{1493, "line", "600,600", 628},
		{4067, "bound_box", "603,603", 603},
		{4091, "box", "600", 603},
	} {
		got := BuiltinRoutine(tc.oid)
		identity := catalog.RoutineIdentity{ID: tc.oid, Namespace: ids.PGCatalogNamespaceOID, Name: tc.name, Signature: catalog.ProcedureSignature(tc.signature)}
		if got == nil || got.Identity != identity || got.Kind != catalog.FunctionRoutine || got.Result != tc.result || !got.Strict || got.Volatility != expr.Immutable || got.Parallel != 's' || got.ReturnsSet || got.Defaults != 0 || got.Variadic != 0 || got.Providers != functions.GeometryConversionRoutineProvider {
			t.Fatalf("selected conversion %d: %+v", tc.oid, got)
		}
	}
}
