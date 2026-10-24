package proc

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqltype"
)

func walkQueryRun(t *testing.T, body string, values ...int64) (*walkHost, *PLRun) {
	t.Helper()
	h := newWalkHost(t)
	h.rows = &walkRows{host: h, values: values}
	source := "DECLARE r record; s integer:=0; BEGIN FOR r IN SELECT 1 AS i LOOP " + body + " END LOOP; RETURN s; END"
	return h, walkUnitRun(t, h, source)
}

func walkFound(t *testing.T, run *PLRun) sqltype.Value {
	t.Helper()
	return run.Frame.Values[run.Frame.Program.FoundSlot]
}

func TestPLRunQueryLoop(t *testing.T) {
	t.Run("detached_records_and_found", func(t *testing.T) {
		h, run := walkQueryRun(t, "s:=s+r.i;", 1, 2, 3)
		value, flow, err := run.Block(run.Definition.PL.Root)
		if got, _ := value.Integer(); err != nil || flow.Kind != PLReturn || got != 6 {
			t.Fatal("sum", got, flow, err)
		}
		if h.rows.closed != 1 || h.rows.primary != nil {
			t.Fatal("producer close", h.rows.closed, h.rows.primary)
		}
		if found, _ := walkFound(t, run).Boolean(); !found {
			t.Fatal("FOUND not published")
		}
		if len(h.seen) != 3 {
			t.Fatal("record reads", len(h.seen))
		}
		for i, row := range h.seen {
			if got, _ := row.CompositeField(0).Integer(); got != int64(i+1) || row.Type().CompositeMember(0).Name != "i" {
				t.Fatal("published record backing reused", i, got)
			}
		}
	})
	t.Run("empty_assigns_shaped_null", func(t *testing.T) {
		h, run := walkQueryRun(t, "s:=s+r.i;")
		in := run.Definition.PL.Root.Statements[0].Block.Statements[0]
		var row sqltype.Value
		h.onEvaluate = func(f *PLFrame) { row = f.Values[in.QueryLoop.Target.Slot] }
		if _, _, err := run.Block(run.Definition.PL.Root); err != nil || h.rows.closed != 1 {
			t.Fatal("empty loop", err, h.rows.closed)
		}
		if found, err := walkFound(t, run).Boolean(); err != nil || found {
			t.Fatal("empty FOUND", found, err)
		}
		if row.Type() == nil || row.CompositeLen() != 1 || row.Type().CompositeMember(0).Name != "i" || !row.CompositeField(0).IsNull() {
			t.Fatal("empty loop lost descriptor-shaped NULL", row)
		}
	})
	for _, tc := range []struct {
		name, body string
		kind       PLFlowKind
	}{
		{"own_exit", "EXIT;", PLReturn},
		{"own_continue", "CONTINUE; s:=99;", PLReturn},
		{"return", "RETURN 7;", PLReturn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, run := walkQueryRun(t, tc.body, 1, 2)
			value, flow, err := run.Block(run.Definition.PL.Root)
			got, _ := value.Integer()
			want := map[string]int64{"own_exit": 0, "own_continue": 0, "return": 7}[tc.name]
			if err != nil || flow.Kind != tc.kind || got != want || h.rows.closed != 1 || h.rows.primary != nil {
				t.Fatal("transfer", got, flow, err, h.rows.closed)
			}
			if found, _ := walkFound(t, run).Boolean(); !found {
				t.Fatal("FOUND after transfer")
			}
			if tc.name == "own_exit" && h.rows.next != 1 {
				t.Fatal("EXIT did not stop demand", h.rows.next)
			}
		})
	}
	t.Run("body_failure_reaches_close", func(t *testing.T) {
		h, run := walkQueryRun(t, "s:=1/0;", 1)
		before := walkFound(t, run)
		_, _, err := run.Block(run.Definition.PL.Root)
		if walkState(err) != "22012" || walkState(h.rows.primary) != "22012" || walkFound(t, run) != before {
			t.Fatal("body failure", err, h.rows.primary)
		}
	})
	t.Run("close_failure_wins_without_found", func(t *testing.T) {
		h, run := walkQueryRun(t, "s:=s+r.i;", 1)
		h.rows.closeErr = errors.New("close veto")
		before := walkFound(t, run)
		_, _, err := run.Block(run.Definition.PL.Root)
		if !errors.Is(err, h.rows.closeErr) || walkFound(t, run) != before {
			t.Fatal("close failure", err)
		}
	})
	t.Run("fetch_failure_keeps_target", func(t *testing.T) {
		h, run := walkQueryRun(t, "s:=s+r.i;", 1)
		h.rows.nextErr = errors.New("fetch failure")
		in := run.Definition.PL.Root.Statements[0].Block.Statements[0]
		assigned := true
		h.rows.atClose = func() { assigned = run.Frame.Values[in.QueryLoop.Target.Slot].Type() != nil }
		_, _, err := run.Block(run.Definition.PL.Root)
		if !errors.Is(err, h.rows.nextErr) || !errors.Is(h.rows.primary, h.rows.nextErr) || assigned {
			t.Fatal("fetch failure", err)
		}
	})
	t.Run("open_failure_skips_close", func(t *testing.T) {
		h, run := walkQueryRun(t, "s:=s+r.i;")
		rows := h.rows
		h.rows = nil
		_, _, err := run.Block(run.Definition.PL.Root)
		if walkState(err) != "42P01" || rows.closed != 0 {
			t.Fatal("open failure", err, rows.closed)
		}
	})
	t.Run("cancellation_per_iteration", func(t *testing.T) {
		h, run := walkQueryRun(t, "", 1, 2, 3)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		in := run.Definition.PL.Root.Statements[0].Block.Statements[0]
		h.ctx = &assignedCancelContext{Context: ctx, cancel: cancel, assigned: func() bool { return run.Frame.Values[in.QueryLoop.Target.Slot].Type() != nil }}
		_, _, err := run.Block(run.Definition.PL.Root)
		if !errors.Is(err, context.Canceled) || h.rows.next != 1 || h.rows.closed != 1 {
			t.Fatal("empty-body loop not cancelled per iteration", err, h.rows.next)
		}
	})
}

// assignedCancelContext cancels once the loop target has been assigned.
type assignedCancelContext struct {
	context.Context
	cancel   context.CancelFunc
	assigned func() bool
}

func (c *assignedCancelContext) Err() error {
	if c.assigned() {
		c.cancel()
	}
	return c.Context.Err()
}
