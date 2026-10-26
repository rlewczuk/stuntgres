package routine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

func TestAccessNamespacesRoutineDeclarations(t *testing.T) {
	// Released pg_proc identities and full-IN vectors, independently specified.
	for _, family := range []struct {
		name  string
		first ids.OID
	}{
		{"has_database_privilege", 2250},
		{"has_schema_privilege", 2268},
		{"has_tablespace_privilege", 2390},
		{"has_type_privilege", 3138},
	} {
		for i, signature := range []catalog.ProcedureSignature{"19,25,25", "19,26,25", "26,25,25", "26,26,25", "25,25", "26,25"} {
			oid := family.first + ids.OID(i)
			count := 0
			for _, d := range BuiltinRoutineDeclarations {
				if d.Identity.ID != oid {
					continue
				}
				count++
				want := catalog.RoutineIdentity{oid, ids.PGCatalogNamespaceOID, family.name, signature}
				if d.Identity != want || d.Kind != catalog.FunctionRoutine || d.Result != 16 || !d.Strict || d.Volatility != expr.Stable || d.Parallel != 's' || d.ReturnsSet || d.Defaults != 0 || d.Variadic != 0 || d.Providers != functions.CatalogRoutineProvider {
					t.Fatalf("OID %d declaration: %+v", oid, d)
				}
			}
			if count != 1 {
				t.Fatalf("OID %d declaration multiplicity %d", oid, count)
			}
			display, ok := builtinRoutineDisplay[oid]
			suffix := []string{"name_name", "name_id", "id_name", "id_id", "name", "id"}[i]
			if !ok || display.Language != catalog.InternalRoutineLanguage || display.Source != family.name+"_"+suffix {
				t.Fatalf("OID %d display: %+v", oid, display)
			}
		}
	}
}
