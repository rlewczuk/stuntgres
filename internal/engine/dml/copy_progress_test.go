package dml

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/copyfmt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestCopyProgressClassification(t *testing.T) {
	var boolean *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Bool {
			boolean = d
			break
		}
	}
	predicate := plan.ColumnExpr{Info: expr.Info{Usage: boolean.Usage()}, Scope: 1, Frame: 1, Slot: 0}
	progress := &CopyProgress{}
	state := CopyFromState{Predicate: predicate, Options: copyfmt.Options{OnError: copyfmt.Ignore, RejectLimit: 1}, Progress: progress}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	for _, value := range []sqltype.Value{sqltype.Boolean(boolean, true), sqltype.Boolean(boolean, false), sqltype.Null(boolean)} {
		evaluation.Row = []sqltype.Value{value}
		if _, err := state.admit(&evaluation); err != nil {
			t.Fatal(err)
		}
	}
	if progress.Excluded.Load() != 2 || progress.Skipped.Load() != 0 || progress.Processed.Load() != 0 {
		t.Fatal("predicate acceptance is not completed insertion", progress)
	}
	// A predicate evaluation error is not an exclusion. A conversion rejection
	// bypasses this invalid frame, including the rejection that exceeds the limit.
	evaluation.Row = nil
	if accepted, err := state.admit(&evaluation); err == nil || accepted || progress.Excluded.Load() != 2 {
		t.Fatal("predicate failure classification", accepted, err)
	}
	state.pending = true
	if accepted, err := state.admit(&evaluation); accepted || err != nil || progress.Skipped.Load() != 1 {
		t.Fatal("soft reject classification", accepted, err)
	}
	state.pending = true
	accepted, err := state.admit(&evaluation)
	var record *sqlstate.Error
	if accepted || !errors.As(err, &record) || record.Code != "22P02" || progress.Skipped.Load() != 2 {
		t.Fatal("skip must advance before reject-limit failure", accepted, err)
	}
	state.completed(3)
	if progress.Processed.Load() != 3 || progress.Excluded.Load() != 2 || progress.Skipped.Load() != 2 {
		t.Fatal("counter domains overlap")
	}
}
