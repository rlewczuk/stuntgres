package routine

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

func TestBuiltinRoutineLookup(t *testing.T) {
	for i := range BuiltinRoutineDeclarations {
		d := &BuiltinRoutineDeclarations[i]
		if got := BuiltinRoutine(d.Identity.ID); got != d {
			t.Fatalf("BuiltinRoutine(%d) = %p, want the declaration at index %d", d.Identity.ID, got, i)
		}
	}
	for _, id := range []ids.OID{0, 4294967295} {
		if got := BuiltinRoutine(id); got != nil {
			t.Fatalf("BuiltinRoutine(%d) = %v, want nil", id, got)
		}
	}
}

func TestOperatorRoutineLookup(t *testing.T) {
	// int4eq (65) implements = (int4, int4), operator 96.
	if got := OperatorRoutine("=", 23, 23); got == nil || got.Identity.ID != 65 {
		t.Fatalf("OperatorRoutine(=, 23, 23) = %v, want int4eq (65)", got)
	}
	if got := OperatorRoutine("=", 23, 4294967295); got != nil {
		t.Fatalf("unknown operand pair resolved to %v", got)
	}
	for _, tc := range []struct {
		name string
		id   ids.OID
	}{
		{"~=", 135}, {"<>", 988}, {"<<", 132}, {">>", 133},
		{"<<|", 134}, {"|>>", 131}, {"?-", 990}, {"?|", 989},
		{"<^", 134}, {">^", 131},
	} {
		got := OperatorRoutine(tc.name, 600, 600)
		if got == nil || got.Identity.ID != tc.id || got != BuiltinRoutine(tc.id) {
			t.Fatalf("OperatorRoutine(%s, 600, 600) = %v, want declared routine %d", tc.name, got, tc.id)
		}
	}
	for _, name := range []string{"=", "<", "<=", ">", ">="} {
		if got := OperatorRoutine(name, 600, 600); got != nil {
			t.Fatalf("unsupported point operator %s resolved to %v", name, got)
		}
	}
	count := 0
	for key, declaration := range OperatorRoutines() {
		count++
		if got := OperatorRoutine(key.Name, key.Left, key.Right); got != declaration {
			t.Fatalf("OperatorRoutine(%v) = %v, iteration yielded %v", key, got, declaration)
		}
	}
	if count == 0 {
		t.Fatal("OperatorRoutines yielded nothing")
	}
	stopped := 0
	for range OperatorRoutines() {
		stopped++
		break
	}
	if stopped != 1 {
		t.Fatal("OperatorRoutines ignored early termination")
	}
}

func TestPointOperatorMetadata(t *testing.T) {
	descriptors := BuiltinOperatorDescriptors(authorization.PrincipalRef{})
	for _, want := range []struct {
		oid, routine, commutator, negator, restriction, join ids.OID
	}{
		{506, 131, 0, 0, 1300, 1301},
		{507, 132, 0, 0, 1300, 1301},
		{508, 133, 0, 0, 1300, 1301},
		{509, 134, 0, 0, 1300, 1301},
		{510, 135, 510, 713, 101, 105},
		{713, 988, 713, 510, 102, 106},
		{808, 990, 808, 0, 0, 0},
		{809, 989, 809, 0, 0, 0},
		{4161, 131, 0, 0, 1300, 1301},
		{4162, 134, 0, 0, 1300, 1301},
	} {
		found := false
		for _, d := range descriptors {
			if d.Address().ObjectID != want.oid {
				continue
			}
			found = true
			if d.Left != 600 || d.Right != 600 || d.Result != 16 || d.Function != want.routine ||
				d.Commutator != want.commutator || d.Negator != want.negator ||
				d.Restriction != want.restriction || d.Join != want.join || d.Merge || d.Hash {
				t.Fatalf("point operator %d descriptor = %+v, want %+v", want.oid, d, want)
			}
		}
		if !found {
			t.Fatalf("point operator %d has no seeded descriptor", want.oid)
		}
	}
}

func TestMACADDR8OperatorMetadata(t *testing.T) {
	descriptors := BuiltinOperatorDescriptors(authorization.PrincipalRef{})
	for _, want := range []struct {
		oid, routine, commutator, negator, restriction, join ids.OID
	}{
		{3362, 4113, 3362, 3363, 101, 105},
		{3363, 4118, 3363, 3362, 102, 106},
		{3364, 4114, 3366, 3367, 103, 107},
		{3365, 4115, 3367, 3366, 336, 386},
		{3366, 4116, 3364, 3365, 104, 108},
		{3367, 4117, 3365, 3364, 337, 398},
	} {
		found := false
		for _, d := range descriptors {
			if d.Address().ObjectID != want.oid {
				continue
			}
			found = true
			equality := want.oid == 3362
			if d.Left != 774 || d.Right != 774 || d.Result != 16 || d.Function != want.routine ||
				d.Commutator != want.commutator || d.Negator != want.negator ||
				d.Restriction != want.restriction || d.Join != want.join ||
				d.Merge != equality || d.Hash != equality {
				t.Fatalf("macaddr8 operator %d descriptor = %+v, want %+v", want.oid, d, want)
			}
		}
		if !found {
			t.Fatalf("macaddr8 operator %d has no seeded descriptor", want.oid)
		}
	}
}

func TestSeededOperatorNamesIsOneIndex(t *testing.T) {
	first := SeededOperatorNames()
	if first == nil || first != SeededOperatorNames() {
		t.Fatal("seeded operator name index must be one process-wide value")
	}
	if len(first.Buckets) == 0 {
		t.Fatal("seeded operator name index is empty")
	}
}
