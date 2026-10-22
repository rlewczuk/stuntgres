package dml

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/copyfmt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestCopyFilterRowAdmission(t *testing.T) {
	var boolean *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Bool {
			boolean = d
			break
		}
	}
	predicate := plan.ColumnExpr{Info: expr.Info{Usage: boolean.Usage()}, Scope: 1, Frame: 1, Slot: 1}
	state := CopyFromState{Predicate: predicate, Options: copyfmt.Options{OnError: copyfmt.Ignore, RejectLimit: 1}}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	for _, tc := range []struct {
		value sqltype.Value
		want  bool
	}{{sqltype.Boolean(boolean, true), true}, {sqltype.Boolean(boolean, false), false}, {sqltype.Null(boolean), false}} {
		evaluation.Row = []sqltype.Value{sqltype.Null(boolean), tc.value}
		got, err := state.admit(&evaluation)
		if err != nil || got != tc.want || state.Rejected != 0 {
			t.Fatal(got, err, state)
		}
	}
	// A pending reject bypasses predicate evaluation only after the caller's
	// default phase. An absent row frame would fail if WHERE were demanded.
	evaluation.Row = nil
	state.pending = true
	if got, err := state.admit(&evaluation); got || err != nil || state.Rejected != 1 || state.pending {
		t.Fatal(got, err, state)
	}
	state.pending = true
	got, err := state.admit(&evaluation)
	var record *sqlstate.Error
	if got || !errors.As(err, &record) || record.Code != "22P02" || state.Rejected != 2 {
		t.Fatal(got, err, state)
	}
	op := &session.Operation{}
	state.Options.LogVerbosity = copyfmt.LogSilent
	state.Success(op)
	if len(op.DetachNotices()) != 0 {
		t.Fatal("silent success emitted notices")
	}
}
