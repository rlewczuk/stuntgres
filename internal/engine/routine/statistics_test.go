package routine

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

func TestStatisticsObjectIORoutineDeclarations(t *testing.T) {
	// Released pg_proc identities and C source names, including receive metadata
	// that deliberately has no SQL-callable INTERNAL argument provider.
	for _, tc := range []struct {
		oid, result     ids.OID
		name, signature string
		provider        functions.RoutineProvider
	}{
		{3355, 3361, "pg_ndistinct_in", "2275", functions.EmbeddedRoutineProvider},
		{3356, 2275, "pg_ndistinct_out", "3361", functions.EmbeddedRoutineProvider},
		{3357, 3361, "pg_ndistinct_recv", "2281", 0},
		{3358, 17, "pg_ndistinct_send", "3361", functions.EmbeddedRoutineProvider},
		{3404, 3402, "pg_dependencies_in", "2275", functions.EmbeddedRoutineProvider},
		{3405, 2275, "pg_dependencies_out", "3402", functions.EmbeddedRoutineProvider},
		{3406, 3402, "pg_dependencies_recv", "2281", 0},
		{3407, 17, "pg_dependencies_send", "3402", functions.EmbeddedRoutineProvider},
		{5018, 5017, "pg_mcv_list_in", "2275", functions.EmbeddedRoutineProvider},
		{5019, 2275, "pg_mcv_list_out", "5017", functions.EmbeddedRoutineProvider},
		{5020, 5017, "pg_mcv_list_recv", "2281", 0},
		{5021, 17, "pg_mcv_list_send", "5017", functions.EmbeddedRoutineProvider},
	} {
		d := BuiltinRoutine(tc.oid)
		volatility := expr.Immutable
		switch tc.oid {
		case 3357, 3358, 3406, 3407, 5020, 5021:
			volatility = expr.Stable
		}
		identity := catalog.RoutineIdentity{ID: tc.oid, Namespace: ids.PGCatalogNamespaceOID, Name: tc.name, Signature: catalog.ProcedureSignature(tc.signature)}
		if d == nil || d.Identity != identity || d.Result != tc.result || d.Kind != catalog.FunctionRoutine || !d.Strict || d.Volatility != volatility || d.Parallel != 's' || d.ReturnsSet || d.Defaults != 0 || d.Variadic != 0 || d.Providers != tc.provider {
			t.Fatal("statistics I/O declaration", tc, d)
		}
		want := builtinDisplayFacts{Language: catalog.InternalRoutineLanguage, Source: tc.name}
		if got, ok := builtinRoutineDisplay[tc.oid]; !ok || got != want || BuiltinRoutineCost(d) != 1 {
			t.Fatal("statistics I/O display", tc, got)
		}
	}
	// Materialize the actual complete seed template, not only the display map.
	owner := (&authorization.Domain{}).Principal(10)
	seen := 0
	for _, p := range BuiltinProcedures(owner) {
		switch p.Address().ObjectID {
		case 3355, 3356, 3357, 3358, 3404, 3405, 3406, 3407, 5018, 5019, 5020, 5021:
			seen++
			if p.Owner != owner || p.Display.Language != catalog.InternalRoutineLanguage || p.Display.Source != p.Name() || p.Cost != 1 || p.Builtin != BuiltinRoutine(p.Address().ObjectID) {
				t.Fatal("statistics I/O seed lost immutable display authority", p)
			}
		}
	}
	if seen != 12 {
		t.Fatal("incomplete statistics I/O seed", seen)
	}
}
