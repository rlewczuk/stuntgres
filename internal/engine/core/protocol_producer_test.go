package core_test

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/core"
	"context"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/engine/enginetest"
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
	p := core.NewProtocolPortal(core.PreparedDefinition{}, ids.TxRef{})
	(*core.PortalProducerRef(p)) = cursor
	p.Resource = &cursor.PreparedResource
	p.Resource.Closer = p
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
	session := enginetest.StartSession(t, e, nil)
	owner := session.Transactions
	p.Transaction, p.Birth = owner.Ref, 1
	newRows, foreignRows := exec.NewFinalRows(nil, 0), exec.NewFinalRows(nil, 0)
	newPortal := core.NewProtocolPortal(core.PreparedDefinition{}, owner.Ref)
	newPortal.Birth, (*core.PortalRowsRef(newPortal)) = 2, newRows
	foreign := core.NewProtocolPortal(core.PreparedDefinition{}, owner.Ref)
	foreign.Birth, (*core.PortalRowsRef(foreign)) = 3, foreignRows
	foreign.Transaction = withTxID(foreign.Transaction, foreign.Transaction.ID()+1)
	core.PayloadOf(session).PreparedSQL.Bind("old", &p.PreparedBinding)
	core.PayloadOf(session).PreparedSQL.Bind("new", &newPortal.PreparedBinding)
	core.PayloadOf(session).PreparedSQL.Bind("foreign", &foreign.PreparedBinding)
	owner.Mu.Lock()
	core.RetireProtocolPortalsAfter(owner, 1)
	owner.Mu.Unlock()
	if p.Retired || !newPortal.Retired || foreign.Retired || cursor.OrdinalPosition != 4 {
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
	if !stage.Closed || cursor.Root != nil || cursor.Source != nil || core.CursorStage(cursor) != nil || (*core.PortalProducerRef(p)) != nil {
		t.Fatal("terminal close retained producer")
	}
	if err = core.PortalClose(p, context.Background()); err != nil || calls != 4 {
		t.Fatal("repeat close replayed/disposed inconsistently", err, calls)
	}
}
