package core

import (
	"math"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/exec"
)

// These test the receipt-age algorithm independently of observation cardinality;
// genuine grant/invocation outcomes belong to the external owner/reference tests.
func TestSepgsqlInitializationCompletionMarks(t *testing.T) {
	scope := &StatementScope{sepgsqlObservations: []access.SepgsqlObservation{{Subject: "one deduplicated observation"}}, routineInitializations: make(map[exec.RoutineInitializationCall]routineInitializationReceipt), directRoutineInitializations: make(map[exec.DirectRoutineInitializationCall]uint64)}
	call := func(occurrence uint32) exec.RoutineInitializationCall {
		return exec.RoutineInitializationCall{Site: exec.RoutineInitializationSite{Occurrence: occurrence}}
	}
	stamp := func() uint64 {
		t.Helper()
		n, err := scope.completeRoutineInitialization()
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	a, b, c := call(1), call(2), call(3)
	scope.routineInitializations[a] = routineInitializationReceipt{completion: stamp()}
	outer := scope.MarkSepgsqlObservations()
	scope.routineInitializations[b] = routineInitializationReceipt{completion: stamp()}
	inner := scope.MarkSepgsqlObservations()
	direct := exec.DirectRoutineInitializationCall{Site: exec.DirectRoutineInitializationSite{Occurrence: 4}}
	scope.directRoutineInitializations[direct] = stamp()
	scope.RestoreSepgsqlObservations(inner)
	if _, ok := scope.routineInitializations[b]; !ok {
		t.Fatal("inner rollback removed earlier completed receipt")
	}
	if _, ok := scope.directRoutineInitializations[direct]; ok {
		t.Fatal("inner rollback kept newer direct-source certificate")
	}
	scope.RestoreSepgsqlObservations(outer)
	if len(scope.sepgsqlObservations) != 1 {
		t.Fatal("receipt rollback changed shared observation")
	}
	if _, ok := scope.routineInitializations[a]; !ok {
		t.Fatal("shared observation rollback removed A")
	}
	if _, ok := scope.routineInitializations[b]; ok {
		t.Fatal("shared observation rollback retained B")
	}
	previous := scope.routineInitializationCompletion
	scope.routineInitializations[c] = routineInitializationReceipt{completion: stamp()}
	if scope.routineInitializations[c].completion <= previous {
		t.Fatal("completion sequence rewound")
	}
	if _, ok := scope.routineInitializations[b]; ok {
		t.Fatal("later completion resurrected B")
	}
	scope.RestoreSepgsqlObservations(outer)
	if len(scope.routineInitializations) != 1 {
		t.Fatal("reusing an older mark discarded the older receipt or kept a newer one")
	}
}

func TestSepgsqlInitializationCompletionExhaustion(t *testing.T) {
	scope := &StatementScope{routineInitializationCompletion: math.MaxUint64}
	if _, err := scope.completeRoutineInitialization(); err == nil {
		t.Fatal("completion exhaustion granted a stamp")
	}
	if scope.routineInitializationCompletion != math.MaxUint64 {
		t.Fatal("exhaustion wrapped counter")
	}
	scope.RestoreSepgsqlObservations(scope.MarkSepgsqlObservations())
	if scope.routineInitializationCompletion != math.MaxUint64 {
		t.Fatal("restore rewound completion counter")
	}
}
