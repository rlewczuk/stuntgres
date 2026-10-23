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
		{993, "lseg", "600,600", 601},
		{1541, "lseg", "603", 601},
		{1447, "path", "604", 602},
		{1440, "point", "701,701", 600},
		{1534, "point", "603", 600},
		{1416, "point", "718", 600},
		{1532, "point", "601", 600},
		{1540, "point", "604", 600},
		{1448, "polygon", "603", 604},
		{1449, "polygon", "602", 604},
		{1475, "polygon", "23,718", 604},
		{1544, "polygon", "718", 604},
	} {
		got := BuiltinRoutine(tc.oid)
		identity := catalog.RoutineIdentity{ID: tc.oid, Namespace: ids.PGCatalogNamespaceOID, Name: tc.name, Signature: catalog.ProcedureSignature(tc.signature)}
		if got == nil || got.Identity != identity || got.Kind != catalog.FunctionRoutine || got.Result != tc.result || !got.Strict || got.Volatility != expr.Immutable || got.Parallel != 's' || got.ReturnsSet || got.Defaults != 0 || got.Variadic != 0 || got.Providers != functions.GeometryConversionRoutineProvider {
			t.Fatalf("selected conversion %d: %+v", tc.oid, got)
		}
	}
}
