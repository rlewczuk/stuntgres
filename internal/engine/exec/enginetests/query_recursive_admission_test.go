package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/workadmission"
)

func TestQueryRecursiveAdmissionFailureOwnership(t *testing.T) {
	owner, state, node := recursiveUnitFixture(t, false, []int64{1}, 3)
	defer owner.Close()
	stage, err := exec.OpenCTEScan(context.Background(), state, node)
	if err != nil {
		t.Fatal(err)
	}
	cteUnitNext(t, &stage)
	producer := owner.Producers[node.CTE]
	runtime := producer.Recursion
	before := runtime.Source.FlatCount
	refusal := errors.New("recursive workload credit refused")
	ctx := workadmission.Activate(workadmission.With(context.Background(), func(_ context.Context, class workadmission.Class) error {
		if class != workadmission.WorkRecursiveUnion {
			t.Fatalf("unexpected recursive class: %v", class)
		}
		return refusal
	}))
	if _, present, err := stage.NextOccurrence(ctx); err != refusal || present {
		t.Fatal("refused recursion published a row", present, err)
	}
	if runtime.Source.FlatCount != before {
		t.Fatal("refused step appended recursive input")
	}
	if err := owner.Close(); err != refusal {
		t.Fatal("primary failure was displaced by cleanup", err)
	}
	if runtime.Opened || runtime.Current != nil || runtime.Seen != nil {
		t.Fatal("failed recursive producer retained arm or generation state")
	}
	if _, err := producer.Ensure(context.Background(), 0); err != refusal {
		t.Fatal("failed producer replay lost original refusal", err)
	}
	// A distinct invocation has no retained admission or failed generation.
	freshOwner, freshState, freshNode := recursiveUnitFixture(t, false, []int64{1}, 3)
	defer freshOwner.Close()
	fresh, err := exec.OpenCTEScan(context.Background(), freshState, freshNode)
	if err != nil {
		t.Fatal(err)
	}
	for want := int64(1); want <= 3; want++ {
		row := cteUnitNext(t, &fresh)
		got, err := row[0].Integer()
		if err != nil || got != want {
			t.Fatal(got, want, err)
		}
	}
	if _, present, err := fresh.NextOccurrence(context.Background()); present || err != nil {
		t.Fatal(present, err)
	}
}
