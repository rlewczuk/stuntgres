package routine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestFTSDebugStatDeclarations(t *testing.T) {
	for _, tc := range []struct {
		oid                     ids.OID
		name, signature, source string
		volatility              expr.Volatility
		parallel                byte
		cost, rows              float32
		language                catalog.RoutineLanguage
		outputs                 int
	}{
		{6183, "ts_debug", "3734,25", "", expr.Stable, 's', 100, 1000, catalog.SQLRoutineLanguage, 6},
		{6184, "ts_debug", "25", "", expr.Stable, 's', 100, 1000, catalog.SQLRoutineLanguage, 6},
		{3689, "ts_stat", "25", "ts_stat1", expr.Volatile, 'u', 10, 10000, catalog.InternalRoutineLanguage, 3},
		{3690, "ts_stat", "25,25", "ts_stat2", expr.Volatile, 'u', 10, 10000, catalog.InternalRoutineLanguage, 3},
	} {
		d := BuiltinRoutine(tc.oid)
		identity := catalog.RoutineIdentity{ID: tc.oid, Namespace: 11, Name: tc.name, Signature: catalog.ProcedureSignature(tc.signature)}
		if d == nil || d.Identity != identity || d.Result != 2249 || !d.Strict || !d.ReturnsSet || d.Volatility != tc.volatility || d.Parallel != tc.parallel || d.Providers != functions.FTSInspectionRoutineProvider || d.Defaults != 0 || d.Variadic != 0 {
			t.Fatal("released declaration", tc, d)
		}
		if d.Outputs == nil || d.Outputs.Len() != tc.outputs || !d.Outputs.MatchesInputs(d.Identity.Signature) {
			t.Fatal("fixed OUT ownership", tc.oid, d.Outputs)
		}
		found := false
		for _, p := range BuiltinProcedures(authorization.PrincipalRef{}) {
			if p.Address().ObjectID != tc.oid {
				continue
			}
			found = true
			if p.CostEstimate() != tc.cost || p.RowEstimate() != tc.rows || p.Display.Language != tc.language || p.Display.Source != tc.source || (p.DeclarationBody != nil) != (tc.language == catalog.SQLRoutineLanguage) || p.SQLBody != nil || p.SQL != nil {
				t.Fatal("display/body owner", tc, p)
			}
		}
		if !found {
			t.Fatal("unmaterialized declaration", tc.oid)
		}
	}
}
