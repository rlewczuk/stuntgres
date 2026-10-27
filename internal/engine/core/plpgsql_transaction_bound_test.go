package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

type transactionBoundHost struct {
	*core.PLHost
	scope      *core.StatementScope
	t          *testing.T
	frame      *proc.PLFrame
	cursorSlot int
	name       string
	cursor     *core.SQLCursor
	backing    *sqltype.Value
	visits     int
	cancel     context.CancelFunc
}

func (h *transactionBoundHost) Transaction(in proc.PLInstruction) error {
	h.t.Helper()
	name, err := h.frame.CursorName(h.cursorSlot)
	if err != nil {
		h.t.Fatal(err)
	}
	h.name = name
	c := core.PayloadOf(h.scope.Op.Session).Cursors.Entries[name]
	if c == nil || !core.CursorPinned(c) {
		h.t.Fatal("bound FOR does not own its portal")
	}
	position := c.OrdinalPosition
	if h.cursor == nil {
		h.cursor = c
		if core.CursorPromoted(c) || core.CursorStage(c) == nil || c.Source == nil {
			h.t.Fatal("first boundary lacks live transaction source")
		}
	}
	if err := h.PLHost.Transaction(in); err != nil {
		return err
	}
	values := core.CursorValues(c)
	if !core.CursorPinned(c) || !core.CursorPromoted(c) || c.OrdinalPosition != position || core.CursorStage(c) != nil || c.Source != nil || core.CursorRoot(c) != nil || core.CursorAuthority(c) != nil || core.CursorTx(c) != (ids.TxRef{}) || len(values) != core.CursorCount(c)*len(core.CursorSchema(c)) || len(values) == 0 {
		h.t.Fatal("promotion changed position or retained producer authority")
	}
	if h.backing == nil {
		h.backing = &values[0]
	} else if h.backing != &values[0] {
		h.t.Fatal("later boundary rebuilt immutable loop storage")
	}
	h.visits++
	if h.cancel != nil {
		h.cancel()
		h.cancel = nil
	}
	return nil
}

func transactionBoundOwnership(t *testing.T) {
	for _, command := range []string{"COMMIT", "ROLLBACK"} {
		t.Run(command, func(t *testing.T) {
			s, run, in := transactionUnitRun(t, "DECLARE tx_bound_owner CURSOR FOR SELECT x FROM generate_series(1,3) x;BEGIN FOR r IN tx_bound_owner LOOP "+command+";END LOOP;RETURN 7;END")
			host := &transactionBoundHost{PLHost: run.Host.(*core.PLHost), scope: s, t: t, frame: run.Frame, cursorSlot: in.Cursor.Slot}
			run.Host = host
			_, flow, err := run.Block(run.Frame.Program.Root)
			if err != nil || flow.Kind != proc.PLReturn || host.visits != 3 {
				t.Fatal("bound loop did not finish over its held source", flow, err)
			}
			c := host.cursor
			if c == nil || core.CursorPinned(c) || core.CursorStage(c) != nil || c.Source != nil || core.CursorValues(c) != nil || core.PayloadOf(s.Op.Session).Cursors.Entries[host.name] != nil {
				t.Fatal("loop exit retained held storage/portal")
			}
		})
	}
}

func transactionBoundCleanup(t *testing.T) {
	for _, outcome := range []string{"EXIT;", "RETURN 7;", "PERFORM 1/0;", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			body := outcome
			if outcome == "cancel" {
				body = ""
			}
			s, run, in := transactionUnitRun(t, "DECLARE tx_bound_owner CURSOR FOR SELECT x FROM generate_series(1,3) x;BEGIN FOR r IN tx_bound_owner LOOP COMMIT;"+body+"END LOOP;RETURN 7;END")
			host := &transactionBoundHost{PLHost: run.Host.(*core.PLHost), scope: s, t: t, frame: run.Frame, cursorSlot: in.Cursor.Slot}
			if outcome == "cancel" {
				ctx, cancel := context.WithCancel(s.Op.Context())
				defer cancel()
				s.Op.Ctx = ctx
				host.cancel = cancel
			}
			run.Host = host
			_, _, err := run.Block(run.Frame.Program.Root)
			if outcome == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
			if outcome == "PERFORM 1/0;" && err == nil {
				t.Fatal("body failure lost")
			}
			if outcome != "cancel" && outcome != "PERFORM 1/0;" && err != nil {
				t.Fatal(err)
			}
			c := host.cursor
			if host.visits != 1 || c == nil || core.CursorPinned(c) || core.CursorStage(c) != nil || c.Source != nil || core.CursorValues(c) != nil || core.PayloadOf(s.Op.Session).Cursors.Entries[host.name] != nil || len(run.Frame.Scopes) != 0 {
				t.Fatal("nonlocal exit retained loop state")
			}
			_ = s.Finish(err)
			if s.Owner.State != session.TxIdle || s.Owner.Ticket != nil || s.LockTicket != nil {
				t.Fatal("nonlocal exit retained successor ticket")
			}
		})
	}
}
