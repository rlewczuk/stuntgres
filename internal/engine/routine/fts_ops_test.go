package routine

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

func TestFTSOpsRoutineDeclarations(t *testing.T) {
	for _, tc := range []struct {
		oid             ids.OID
		name, signature string
		result          ids.OID
		provider        expr.RoutineProvider
	}{
		{3625, "tsvector_concat", "3614,3614", 3614, functions.OperatorRoutineProvider},
		{3669, "tsquery_and", "3615,3615", 3615, functions.OperatorRoutineProvider},
		{3670, "tsquery_or", "3615,3615", 3615, functions.OperatorRoutineProvider},
		{3671, "tsquery_not", "3615", 3615, functions.OperatorRoutineProvider},
		{5003, "tsquery_phrase", "3615,3615", 3615, functions.OperatorRoutineProvider},
		{5004, "tsquery_phrase", "3615,3615,23", 3615, functions.FTSRoutineProvider},
		{3691, "tsq_mcontains", "3615,3615", 16, functions.OperatorRoutineProvider},
		{3692, "tsq_mcontained", "3615,3615", 16, functions.OperatorRoutineProvider},
	} {
		got := BuiltinRoutine(tc.oid)
		identity := catalog.RoutineIdentity{ID: tc.oid, Namespace: ids.PGCatalogNamespaceOID, Name: tc.name, Signature: catalog.ProcedureSignature(tc.signature)}
		if got == nil || got.Identity != identity || got.Kind != catalog.FunctionRoutine || got.Result != tc.result || !got.Strict || got.Volatility != expr.Immutable || got.Parallel != 's' || got.ReturnsSet || got.Defaults != 0 || got.Variadic != 0 || got.Providers != tc.provider {
			t.Fatalf("selected routine %d = %+v", tc.oid, got)
		}
	}
	descriptors := BuiltinOperatorDescriptors(authorization.PrincipalRef{})
	for _, tc := range []struct {
		oid, routine, left, right, result, commutator, restriction, join ids.OID
		name                                                             string
	}{
		{3633, 3625, 3614, 3614, 3614, 0, 0, 0, "||"},
		{3680, 3669, 3615, 3615, 3615, 0, 0, 0, "&&"},
		{3681, 3670, 3615, 3615, 3615, 0, 0, 0, "||"},
		{3682, 3671, 0, 3615, 3615, 0, 0, 0, "!!"},
		{5005, 5003, 3615, 3615, 3615, 0, 0, 0, "<->"},
		{3693, 3691, 3615, 3615, 16, 3694, 5040, 5041, "@>"},
		{3694, 3692, 3615, 3615, 16, 3693, 5040, 5041, "<@"},
	} {
		declaration := OperatorRoutine(tc.name, tc.left, tc.right)
		if declaration == nil || declaration.Identity.ID != tc.routine {
			t.Fatal("selected operator routine", tc, declaration)
		}
		found := false
		for _, d := range descriptors {
			if d.Address().ObjectID == tc.oid {
				found = true
				if d.Function != tc.routine || d.Left != tc.left || d.Right != tc.right || d.Result != tc.result || d.Commutator != tc.commutator || d.Negator != 0 || d.Restriction != tc.restriction || d.Join != tc.join || d.Hash || d.Merge {
					t.Fatal("selected operator metadata", tc, d)
				}
			}
		}
		if !found {
			t.Fatal("missing operator identity", tc)
		}
	}
	for _, oid := range []ids.OID{3622, 3668} {
		if BuiltinRoutine(oid) != nil {
			t.Fatal("adjacent routine promoted", oid)
		}
	}
}

func TestFTSQueryFunctionsRoutineDeclarations(t *testing.T) {
	for _, tc := range []struct {
		oid, result     ids.OID
		name, signature string
		volatility      expr.Volatility
		parallel        byte
	}{
		{3672, 23, "numnode", "3615", expr.Immutable, 's'},
		{3673, 25, "querytree", "3615", expr.Immutable, 's'},
		{3684, 3615, "ts_rewrite", "3615,3615,3615", expr.Immutable, 's'},
		{3685, 3615, "ts_rewrite", "3615,25", expr.Volatile, 'u'},
	} {
		got := BuiltinRoutine(tc.oid)
		identity := catalog.RoutineIdentity{ID: tc.oid, Namespace: ids.PGCatalogNamespaceOID, Name: tc.name, Signature: catalog.ProcedureSignature(tc.signature)}
		if got == nil || got.Identity != identity || got.Result != tc.result || got.Kind != catalog.FunctionRoutine || !got.Strict || got.Volatility != tc.volatility || got.Parallel != tc.parallel || got.ReturnsSet || got.Defaults != 0 || got.Variadic != 0 || got.Providers != functions.FTSQueryRoutineProvider {
			t.Fatalf("query function declaration %d = %+v", tc.oid, got)
		}
	}
}

func TestFTSMatchRoutineDeclarations(t *testing.T) {
	for _, tc := range []struct {
		oid, operator, left, right, commutator, restriction, join ids.OID
		name, signature                                           string
		volatility                                                expr.Volatility
		cost                                                      float32
	}{
		{3634, 3636, 3614, 3615, 3637, 3686, 3687, "ts_match_vq", "3614,3615", expr.Immutable, 1},
		{3635, 3637, 3615, 3614, 3636, 3686, 3687, "ts_match_qv", "3615,3614", expr.Immutable, 1},
		{3760, 3762, 25, 25, 0, 5040, 5041, "ts_match_tt", "25,25", expr.Stable, 100},
		{3761, 3763, 25, 3615, 0, 5040, 5041, "ts_match_tq", "25,3615", expr.Stable, 100},
	} {
		r := BuiltinRoutine(tc.oid)
		want := catalog.RoutineIdentity{ID: tc.oid, Namespace: ids.PGCatalogNamespaceOID, Name: tc.name, Signature: catalog.ProcedureSignature(tc.signature)}
		if r == nil || r.Identity != want || r.Result != 16 || r.Kind != catalog.FunctionRoutine || !r.Strict ||
			r.Volatility != tc.volatility || r.Parallel != 's' || r.ReturnsSet || r.Defaults != 0 || r.Variadic != 0 ||
			r.Providers != functions.OperatorRoutineProvider || BuiltinRoutineCost(r) != tc.cost {
			t.Fatal(tc, r)
		}
		if selected := OperatorRoutine("@@", tc.left, tc.right); selected != r {
			t.Fatal("selected routine authority", tc, selected)
		}
		found := false
		for _, d := range BuiltinOperatorDescriptors(authorization.PrincipalRef{}) {
			if d.Address().ObjectID != tc.operator {
				continue
			}
			found = true
			if d.Function != tc.oid || d.Left != tc.left || d.Right != tc.right || d.Result != 16 ||
				d.Commutator != tc.commutator || d.Restriction != tc.restriction || d.Join != tc.join ||
				d.Negator != 0 || d.Hash || d.Merge {
				t.Fatal(tc, d)
			}
		}
		if !found {
			t.Fatal("missing selected operator", tc)
		}
		if OperatorRoutine("@@@", tc.left, tc.right) != nil {
			t.Fatal("deprecated operator activated", tc)
		}
	}
}
