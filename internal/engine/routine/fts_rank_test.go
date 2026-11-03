package routine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestFTSRankRoutineDeclarations(t *testing.T) {
	for _, tc := range []struct {
		oid, result             ids.OID
		name, signature, source string
		volatility              expr.Volatility
		cost                    float32
	}{
		{3703, 700, "ts_rank", "1021,3614,3615,23", "ts_rank_wttf", expr.Immutable, 1},
		{3704, 700, "ts_rank", "1021,3614,3615", "ts_rank_wtt", expr.Immutable, 1},
		{3705, 700, "ts_rank", "3614,3615,23", "ts_rank_ttf", expr.Immutable, 1},
		{3706, 700, "ts_rank", "3614,3615", "ts_rank_tt", expr.Immutable, 1},
		{3707, 700, "ts_rank_cd", "1021,3614,3615,23", "ts_rankcd_wttf", expr.Immutable, 1},
		{3708, 700, "ts_rank_cd", "1021,3614,3615", "ts_rankcd_wtt", expr.Immutable, 1},
		{3709, 700, "ts_rank_cd", "3614,3615,23", "ts_rankcd_ttf", expr.Immutable, 1},
		{3710, 700, "ts_rank_cd", "3614,3615", "ts_rankcd_tt", expr.Immutable, 1},
		{3743, 25, "ts_headline", "3734,25,3615,25", "ts_headline_byid_opt", expr.Immutable, 100},
		{3744, 25, "ts_headline", "3734,25,3615", "ts_headline_byid", expr.Immutable, 100},
		{3754, 25, "ts_headline", "25,3615,25", "ts_headline_opt", expr.Stable, 100},
		{3755, 25, "ts_headline", "25,3615", "ts_headline", expr.Stable, 100},
	} {
		declaration := BuiltinRoutine(tc.oid)
		provider := functions.FTSRankRoutineProvider
		if tc.name == "ts_headline" {
			provider = functions.FTSHeadlineRoutineProvider
		}
		identity := catalog.RoutineIdentity{ID: tc.oid, Namespace: ids.PGCatalogNamespaceOID, Name: tc.name, Signature: catalog.ProcedureSignature(tc.signature)}
		if declaration == nil || declaration.Identity != identity || declaration.Result != tc.result || declaration.Kind != catalog.FunctionRoutine || !declaration.Strict || declaration.Volatility != tc.volatility || declaration.Parallel != 's' || declaration.ReturnsSet || declaration.Defaults != 0 || declaration.Variadic != 0 || declaration.Outputs != nil || declaration.Providers != provider {
			t.Fatal(tc, declaration)
		}
		display := builtinRoutineDisplay[tc.oid]
		if display.Language != catalog.InternalRoutineLanguage || display.Source != tc.source || BuiltinRoutineCost(declaration) != tc.cost {
			t.Fatal(tc, display)
		}
	}
	for _, oid := range []ids.OID{4201, 4202, 4203, 4204, 4205, 4206, 4207, 4208} {
		if BuiltinRoutine(oid) != nil {
			t.Fatal("unselected JSON headline published", oid)
		}
	}
	if handler := BuiltinRoutine(3720); handler == nil || handler.Providers != 0 {
		t.Fatal("headline parser handler acquired SQL authority", handler)
	}
}
