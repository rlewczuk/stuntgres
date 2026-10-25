package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestProtocolPortalProducer(t *testing.T) {
	e, query, source := queryCoreFixture(t, "SELECT id FROM t ORDER BY id")
	root := query.Root.(*plan.ProjectNode)
	original := root.Expressions[0]
	calls := 0
	root.Expressions[0] = queryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) { calls++; return original.Eval(c) })
	state := exec.State{Source: source, HashFilter: exec.HashQueryFilter(root)}
	stage, err := exec.OpenQueryProjectStage(context.Background(), &state, root)
	if err != nil {
		t.Fatal(err)
	}
	cursor := core.NewCursor(core.CursorFields{Root: root, Source: source, State: state, Stage: &stage})
	p := core.NewPortal(core.PortalFields{Producer: cursor})
	row := make([]sqltype.Value, 1)
	if calls != 0 {
		t.Fatal("producer initialization demanded target", calls)
	}
	for i := range 4 {
		present, err := core.PortalNext(p, context.Background(), row)
		if err != nil || !present || calls != i+1 || cursor.OrdinalPosition != int64(i+1) {
			t.Fatal("finite demand replayed or looked ahead", present, calls, cursor.OrdinalPosition, err)
		}
	}
	if present, err := core.PortalNext(p, context.Background(), row); err != nil || present || calls != 4 {
		t.Fatal("exhaustion projected an extra target", present, calls, err)
	}
	// Savepoint retirement uses the canonical birth frontier without rewinding
	// older demand or disposing a different transaction's exported owner.
	session := sessionStart(t, e, nil)
	owner := session.Transactions
	*core.PortalTxRef(p), *core.PortalBornRef(p) = owner.Ref, 1
	newRows, foreignRows := exec.NewFinalRows(nil, 0), exec.NewFinalRows(nil, 0)
	newPortal := core.NewPortal(core.PortalFields{Tx: owner.Ref, Born: 2, Rows: newRows})
	foreign := core.NewPortal(core.PortalFields{Tx: owner.Ref, Born: 3, Rows: foreignRows})
	core.SetPortalTx(foreign, withTxID(core.PortalTx(foreign), core.PortalTx(foreign).ID()+1))
	core.SetPreparedRegistryPortals(core.PayloadPreparedSQL(core.PayloadOf(session)), map[string]*core.ProtocolPortal{"old": p, "new": newPortal, "foreign": foreign})
	owner.Mu.Lock()
	core.RetireProtocolPortalsAfter(owner, 1)
	owner.Mu.Unlock()
	if core.PortalRetired(p) || !core.PortalRetired(newPortal) || core.PortalRetired(foreign) || cursor.OrdinalPosition != 4 {
		t.Fatal("savepoint frontier changed older position or foreign ownership")
	}
	if err := core.DrainCursorCleanup(owner, context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if !newRows.Closed || foreignRows.Closed || core.PortalProducer(p) == nil || stage.Closed || calls != 4 {
		t.Fatal("savepoint retirement leaked new rows or disposed retained producer")
	}
	if err = core.PortalClose(p, context.Background()); err != nil {
		t.Fatal(err)
	}
	if !stage.Closed || core.CursorRoot(cursor) != nil || cursor.Source != nil || core.CursorStage(cursor) != nil || core.PortalProducer(p) != nil {
		t.Fatal("terminal close retained producer")
	}
	if err = core.PortalClose(p, context.Background()); err != nil || calls != 4 {
		t.Fatal("repeat close replayed/disposed inconsistently", err, calls)
	}
}
