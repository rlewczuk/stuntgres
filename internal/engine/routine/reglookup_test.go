package routine

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

func TestRegLookupCollRoleDeclarations(t *testing.T) {
	// REL_18_6 pg_proc.dat gives the OIDs and TEXT/result signatures;
	// pg_proc.h supplies strict/internal/parallel-safe/scalar defaults.
	owner := (&authorization.Domain{}).Principal(10)
	procedures := BuiltinProcedures(owner)
	for _, tc := range []struct {
		oid, result ids.OID
		name string
	}{
		{4195, 4191, "to_regcollation"},
		{4093, 4096, "to_regrole"},
	} {
		declaration := BuiltinRoutine(tc.oid)
		identity := catalog.RoutineIdentity{ID: tc.oid, Namespace: ids.PGCatalogNamespaceOID, Name: tc.name, Signature: "25"}
		if declaration == nil || declaration.Identity != identity || declaration.Kind != catalog.FunctionRoutine || declaration.Result != tc.result || !declaration.Strict || declaration.Volatility != expr.Stable || declaration.Parallel != 's' || declaration.ReturnsSet || declaration.Defaults != 0 || declaration.Variadic != 0 || declaration.Providers != functions.CatalogRoutineProvider || declaration.Outputs != nil {
			t.Fatal("released registry lookup declaration", tc, declaration)
		}
		wantDisplay := builtinDisplayFacts{Language: catalog.InternalRoutineLanguage, Source: tc.name}
		if display, ok := builtinRoutineDisplay[tc.oid]; !ok || display != wantDisplay || BuiltinRoutineCost(declaration) != 1 {
			t.Fatal("released registry lookup display", tc, display)
		}
		seen := 0
		for _, procedure := range procedures {
			if procedure.Address().ObjectID != tc.oid {
				continue
			}
			seen++
			if procedure.Owner != owner || procedure.Builtin != declaration || procedure.Name() != tc.name || string(procedure.Signature) != "25" || procedure.Result != tc.result || !procedure.Strict || procedure.Volatility != expr.Stable || procedure.ReturnsSet() || procedure.InputNames != nil || procedure.LiteralDefaults != nil || procedure.OutputDeclaration() != nil || procedure.Display.Language != catalog.InternalRoutineLanguage || procedure.Display.Source != tc.name || procedure.Cost != 1 || procedure.RowEstimate() != 0 {
				t.Fatal("complete seed lost registry lookup declaration", tc, procedure)
			}
		}
		if seen != 1 {
			t.Fatal("missing/duplicate registry lookup seed", tc, seen)
		}
	}
}
