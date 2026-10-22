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
	for _, oid := range []ids.OID{3622, 3634, 3635, 3668, 3672} {
		if BuiltinRoutine(oid) != nil {
			t.Fatal("adjacent routine promoted", oid)
		}
	}
}
