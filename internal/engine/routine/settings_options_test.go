package routine

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

func TestSettingsOptionsRoutineDeclarations(t *testing.T) {
	// REL_18_6 pg_proc.dat supplies the identities, prosrc and options prorows;
	// pg_proc.h supplies the internal-language/default-cost facts.
	owner := (&authorization.Domain{}).Principal(10)
	procedures := BuiltinProcedures(owner)
	for _, tc := range []struct {
		oid, result ids.OID
		name, signature string
		set bool
		rows float32
	}{
		{2289, 2249, "pg_options_to_table", "1009", true, 3},
		{6240, 1009, "pg_settings_get_flags", "25", false, 0},
	} {
		declaration := BuiltinRoutine(tc.oid)
		identity := catalog.RoutineIdentity{ID: tc.oid, Namespace: ids.PGCatalogNamespaceOID, Name: tc.name, Signature: catalog.ProcedureSignature(tc.signature)}
		if declaration == nil || declaration.Identity != identity || declaration.Result != tc.result || declaration.Kind != catalog.FunctionRoutine || !declaration.Strict || declaration.Volatility != expr.Stable || declaration.Parallel != 's' || declaration.ReturnsSet != tc.set || declaration.Defaults != 0 || declaration.Variadic != 0 || declaration.Providers != functions.SettingRoutineProvider {
			t.Fatal("released settings/options declaration", tc, declaration)
		}
		wantDisplay := builtinDisplayFacts{Language: catalog.InternalRoutineLanguage, Source: tc.name}
		if display, ok := builtinRoutineDisplay[tc.oid]; !ok || display != wantDisplay || BuiltinRoutineCost(declaration) != 1 {
			t.Fatal("released settings/options display", tc, display)
		}
		seen := 0
		for _, procedure := range procedures {
			if procedure.Address().ObjectID != tc.oid {
				continue
			}
			seen++
			if procedure.Owner != owner || procedure.Builtin != declaration || procedure.Display.Language != catalog.InternalRoutineLanguage || procedure.Display.Source != tc.name || procedure.Cost != 1 || procedure.RowEstimate() != tc.rows || procedure.InputNames != nil {
				t.Fatal("complete seed lost released metadata", tc, procedure)
			}
			outputs := procedure.OutputDeclaration()
			if !tc.set {
				if outputs != nil {
					t.Fatal("flags invented formal names", outputs)
				}
				continue
			}
			if outputs == nil || outputs.Inputs() != 1 || outputs.Len() != 2 {
				t.Fatal("options output declaration", outputs)
			}
			for i, name := range []string{"options_array", "option_name", "option_value"} {
				if outputs.Name(i) != name {
					t.Fatal("options formal names", i, outputs.Name(i))
				}
			}
			for i := range 2 {
				if outputs.Type(i) != 25 {
					t.Fatal("options output types", i, outputs.Type(i))
				}
			}
		}
		if seen != 1 {
			t.Fatal("missing/duplicate settings/options seed", tc, seen)
		}
	}
}
