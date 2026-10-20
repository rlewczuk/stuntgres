package engine

import (
	"testing"

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

func TestSeededOperatorNamesIsOneIndex(t *testing.T) {
	first := SeededOperatorNames()
	if first == nil || first != SeededOperatorNames() {
		t.Fatal("seeded operator name index must be one process-wide value")
	}
	if len(first.Buckets) == 0 {
		t.Fatal("seeded operator name index is empty")
	}
}
