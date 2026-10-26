package routine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestAccessSpecialRoutineDeclarations(t *testing.T) {
	for _, tc := range []struct {
		oid    ids.OID
		name   string
		sig    catalog.ProcedureSignature
		source string
	}{
		{6348, "has_largeobject_privilege", "19,26,25", "has_largeobject_privilege_name_id"},
		{6349, "has_largeobject_privilege", "26,25", "has_largeobject_privilege_id"},
		{6350, "has_largeobject_privilege", "26,26,25", "has_largeobject_privilege_id_id"},
		{6205, "has_parameter_privilege", "19,25,25", "has_parameter_privilege_name_name"},
		{6206, "has_parameter_privilege", "26,25,25", "has_parameter_privilege_id_name"},
		{6207, "has_parameter_privilege", "25,25", "has_parameter_privilege_name"},
	} {
		count := 0
		for _, d := range BuiltinRoutineDeclarations {
			if d.Identity.ID != tc.oid {
				continue
			}
			count++
			want := catalog.RoutineIdentity{tc.oid, ids.PGCatalogNamespaceOID, tc.name, tc.sig}
			if d.Identity != want || d.Kind != catalog.FunctionRoutine || d.Result != 16 || !d.Strict || d.Volatility != expr.Stable || d.Parallel != 's' || d.ReturnsSet || d.Defaults != 0 || d.Variadic != 0 || d.Providers != functions.CatalogRoutineProvider {
				t.Fatalf("declaration %+v", d)
			}
		}
		if count != 1 {
			t.Fatalf("OID %d count %d", tc.oid, count)
		}
		d, ok := builtinRoutineDisplay[tc.oid]
		if !ok || d.Language != catalog.InternalRoutineLanguage || d.Source != tc.source {
			t.Fatalf("display %+v", d)
		}
	}
}
