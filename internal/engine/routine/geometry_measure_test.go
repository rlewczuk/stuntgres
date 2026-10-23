package routine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

func TestGeometryMeasureBoxLineDeclarations(t *testing.T) {
	for _, tc := range []struct {
		oid             ids.OID
		name, signature string
		result          ids.OID
	}{
		{976, "width", "603", 701},
		{977, "height", "603", 701},
		{981, "diagonal", "603", 601},
		{992, "slope", "600,600", 701},
	} {
		got := BuiltinRoutine(tc.oid)
		identity := catalog.RoutineIdentity{ID: tc.oid, Namespace: ids.PGCatalogNamespaceOID, Name: tc.name, Signature: catalog.ProcedureSignature(tc.signature)}
		if got == nil || got.Identity != identity || got.Kind != catalog.FunctionRoutine || got.Result != tc.result || !got.Strict || got.Volatility != expr.Immutable || got.Parallel != 's' || got.ReturnsSet || got.Defaults != 0 || got.Variadic != 0 || got.Providers != functions.GeometryMeasureRoutineProvider {
			t.Fatalf("selected measurement %d: %+v", tc.oid, got)
		}
	}
}

func TestGeometryMeasurePathDeclarations(t *testing.T) {
	for _, tc := range []struct {
		oid             ids.OID
		name, signature string
		result          ids.OID
	}{
		{1430, "isclosed", "602", 16},
		{1431, "isopen", "602", 16},
		{1433, "pclose", "602", 602},
		{1434, "popen", "602", 602},
		{1530, "length", "601", 701},
		{1531, "length", "602", 701},
		{1545, "npoints", "602", 23},
		{1556, "npoints", "604", 23},
	} {
		got := BuiltinRoutine(tc.oid)
		identity := catalog.RoutineIdentity{ID: tc.oid, Namespace: ids.PGCatalogNamespaceOID, Name: tc.name, Signature: catalog.ProcedureSignature(tc.signature)}
		if got == nil || got.Identity != identity || got.Kind != catalog.FunctionRoutine || got.Result != tc.result || !got.Strict || got.Volatility != expr.Immutable || got.Parallel != 's' || got.ReturnsSet || got.Defaults != 0 || got.Variadic != 0 || got.Providers != functions.GeometryMeasureRoutineProvider {
			t.Fatalf("selected path measurement %d: %+v", tc.oid, got)
		}
	}
}
