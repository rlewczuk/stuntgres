package engine

import (
	"context"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
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
	cursor := &sqlCursor{root: root, Source: source, state: state, stage: &stage}
	p := &protocolPortal{producer: cursor}
	row := make([]sqltype.Value, 1)
	if calls != 0 {
		t.Fatal("producer initialization demanded target", calls)
	}
	for i := range 4 {
		present, err := p.next(context.Background(), row)
		if err != nil || !present || calls != i+1 || cursor.OrdinalPosition != int64(i+1) {
			t.Fatal("finite demand replayed or looked ahead", present, calls, cursor.OrdinalPosition, err)
		}
	}
	if present, err := p.next(context.Background(), row); err != nil || present || calls != 4 {
		t.Fatal("exhaustion projected an extra target", present, calls, err)
	}
	// Savepoint retirement uses the canonical birth frontier without rewinding
	// older demand or disposing a different transaction's exported owner.
	session := sessionStart(t, e, nil)
	owner := session.Transactions
	p.tx, p.born = owner.Ref, 1
	newRows, foreignRows := exec.NewFinalRows(nil, 0), exec.NewFinalRows(nil, 0)
	newPortal := &protocolPortal{tx: owner.Ref, born: 2, rows: newRows}
	foreign := &protocolPortal{tx: owner.Ref, born: 3, rows: foreignRows}
	foreign.tx = withTxID(foreign.tx, foreign.tx.ID()+1)
	PayloadOf(session).preparedSQL.portals = map[string]*protocolPortal{"old": p, "new": newPortal, "foreign": foreign}
	owner.Mu.Lock()
	retireProtocolPortalsAfter(owner, 1)
	owner.Mu.Unlock()
	if p.retired || !newPortal.retired || foreign.retired || cursor.OrdinalPosition != 4 {
		t.Fatal("savepoint frontier changed older position or foreign ownership")
	}
	if err := drainCursorCleanup(owner, context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if !newRows.Closed || foreignRows.Closed || p.producer == nil || stage.Closed || calls != 4 {
		t.Fatal("savepoint retirement leaked new rows or disposed retained producer")
	}
	if err = p.close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !stage.Closed || cursor.root != nil || cursor.Source != nil || cursor.stage != nil || p.producer != nil {
		t.Fatal("terminal close retained producer")
	}
	if err = p.close(context.Background()); err != nil || calls != 4 {
		t.Fatal("repeat close replayed/disposed inconsistently", err, calls)
	}
}
