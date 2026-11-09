package routine

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestJSONBStripNullsRoutineDeclarations(t *testing.T) {
	procedures := BuiltinProcedures(authorization.PrincipalRef{})
	for _, tc := range []struct {
		oid       ids.OID
		name      string
		target    sqltype.ID
		signature catalog.ProcedureSignature
	}{
		{3261, "json_strip_nulls", sqltype.JSON, "114,16"},
		{3262, "jsonb_strip_nulls", sqltype.JSONB, "3802,16"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := BuiltinRoutine(tc.oid)
			if d == nil || d.Identity.ID != tc.oid || d.Identity.Namespace != ids.PGCatalogNamespaceOID || d.Identity.Name != tc.name || d.Identity.Signature != tc.signature || d.Kind != catalog.FunctionRoutine || d.Result != ids.OID(tc.target) || !d.Strict || d.Volatility != expr.Immutable || d.Parallel != 's' || d.ReturnsSet || d.Variadic != 0 || d.Defaults != 1 || d.Providers != functions.JSONRoutineProvider || d.Outputs != nil {
				t.Fatal("incorrect released executable declaration", d)
			}
			lookup, err := LookupBuiltinRoutineDeclaration(functions.JSONRoutineProvider, tc.name, []sqltype.ID{tc.target, sqltype.Bool})
			if err != nil || lookup != d {
				t.Fatal("lookup lost executable declaration authority", lookup, err)
			}
			identity, ok := catalog.BuiltinRoutineIdentityByOID(tc.oid)
			if !ok || identity != d.Identity {
				t.Fatal("executable declaration diverged from existing identity", identity)
			}
			seen := 0
			for _, p := range procedures {
				if p.Address().ObjectID != tc.oid {
					continue
				}
				seen++
				if p.Builtin != d || p.Signature != tc.signature || p.Display.Language != catalog.InternalRoutineLanguage || p.Display.Source != tc.name || p.Display.HasBinary || p.Display.Binary != "" || p.Cost != 1 || BuiltinRoutineCost(d) != 1 {
					t.Fatal("materialized strip lost internal source/cost/authority", p)
				}
				if p.InputNames.Len() != 2 || p.InputNames.At(0) != "target" || p.InputNames.At(1) != "strip_in_arrays" || p.LiteralDefaults.Len() != 1 || p.LiteralDefaults.At(0) != (catalog.RoutineLiteral{SourceType: 16, Text: "false"}) {
					t.Fatal("strip lost formal names or typed false default", p)
				}
			}
			if seen != 1 {
				t.Fatal("duplicate or missing executable strip descriptor", seen)
			}
		})
	}
}
