package routine

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

func TestMACOperationsRoutineMetadata(t *testing.T) {
	for _, tc := range []struct {
		oid             ids.OID
		name, signature string
		result          ids.OID
		provider        functions.RoutineProvider
	}{
		{753, "trunc", "829", 829, functions.NetworkRoutineProvider},
		{3144, "macaddr_not", "829", 829, functions.OperatorRoutineProvider},
		{3145, "macaddr_and", "829,829", 829, functions.OperatorRoutineProvider},
		{3146, "macaddr_or", "829,829", 829, functions.OperatorRoutineProvider},
		{4112, "trunc", "774", 774, functions.NetworkRoutineProvider},
		{4120, "macaddr8_not", "774", 774, functions.OperatorRoutineProvider},
		{4121, "macaddr8_and", "774,774", 774, functions.OperatorRoutineProvider},
		{4122, "macaddr8_or", "774,774", 774, functions.OperatorRoutineProvider},
		{4125, "macaddr8_set7bit", "774", 774, functions.NetworkRoutineProvider},
	} {
		got := BuiltinRoutine(tc.oid)
		identity := catalog.RoutineIdentity{ID: tc.oid, Namespace: ids.PGCatalogNamespaceOID, Name: tc.name, Signature: catalog.ProcedureSignature(tc.signature)}
		if got == nil || got.Identity != identity || got.Kind != catalog.FunctionRoutine || got.Result != tc.result || !got.Strict || got.Volatility != expr.Immutable || got.Parallel != 's' || got.ReturnsSet || got.Defaults != 0 || got.Variadic != 0 || got.Providers != tc.provider {
			t.Fatalf("MAC routine %d: %+v", tc.oid, got)
		}
	}
	descriptors := BuiltinOperatorDescriptors(authorization.PrincipalRef{})
	for _, tc := range []struct {
		oid, routine, left, right, result ids.OID
		name, kind                        string
	}{
		{3147, 3144, 0, 829, 829, "~", "l"}, {3148, 3145, 829, 829, 829, "&", "b"}, {3149, 3146, 829, 829, 829, "|", "b"},
		{3368, 4120, 0, 774, 774, "~", "l"}, {3369, 4121, 774, 774, 774, "&", "b"}, {3370, 4122, 774, 774, 774, "|", "b"},
	} {
		decl := OperatorRoutine(tc.name, tc.left, tc.right)
		if decl == nil || decl != BuiltinRoutine(tc.routine) {
			t.Fatal("operator routine authority", tc, decl)
		}
		found := false
		for _, d := range descriptors {
			if d.Address().ObjectID != tc.oid {
				continue
			}
			found = true
			if d.Name() != tc.name || d.Left != tc.left || d.Right != tc.right || d.Result != tc.result || d.Function != tc.routine || d.Commutator != 0 || d.Negator != 0 || d.Restriction != 0 || d.Join != 0 || d.Hash || d.Merge {
				t.Fatalf("MAC operator %d: %+v", tc.oid, d)
			}
		}
		if !found {
			t.Fatal("missing published MAC operator", tc.oid)
		}
		published, found := OperatorMetadata{}, false
		for _, m := range SupportedOperatorMetadata {
			if m.OID == tc.oid {
				published, found = m, true
			}
		}
		want := OperatorMetadata{OID: tc.oid, Result: uint32(tc.result), Kind: tc.kind, Procedure: uint32(tc.routine)}
		if !found || published != want {
			t.Fatal("published MAC metadata", published, want)
		}
	}
}
