package engine

import (
	"context"
	"stuntgres/internal/engine/session"
	"testing"
)

func TestOperationAnalysisSlotHoldsItsHost(t *testing.T) {
	e := New()
	op, cleanup, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	h, _ := op.Analysis.(*BindingHost)
	if h == nil || h.Op != op || h.Engine != e || e.AnalysisHost(op) != h {
		t.Fatalf("analysis slot %#v is not the operation's own host", op.Analysis)
	}
	if literal := (&session.Operation{}); literal.Analysis != nil {
		t.Fatal("an operation literal has an analysis slot")
	}
}

func TestOperationReplaySlot(t *testing.T) {
	op := &session.Operation{}
	allocs := testing.AllocsPerRun(10, func() {
		if OperationReplayOrZero(op).TableDestination {
			t.Fatal("missing replay state does not read as zero")
		}
	})
	if allocs != 0 || op.ReplaySlot != nil {
		t.Fatalf("reading missing replay state allocated (%v allocs, slot %v)", allocs, op.ReplaySlot)
	}
	r := OperationReplay(op)
	r.TableDestination = true
	if OperationReplay(op) != r || !OperationReplayOrZero(op).TableDestination {
		t.Fatal("operationReplay did not keep one replay state")
	}
}
