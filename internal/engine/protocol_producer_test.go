package engine

import (
	"context"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestProtocolPortalProducer(t *testing.T) {
	e, query, source := queryCoreFixture(t, "SELECT id FROM t ORDER BY id")
	root := query.Root.(*projectNode)
	original := root.expressions[0]
	calls := 0
	root.expressions[0] = queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) { calls++; return original.Eval(c) })
	state := queryState{source: source, hashFilter: hashQueryFilter(root)}
	stage, err := openQueryProjectStage(context.Background(), &state, root)
	if err != nil {
		t.Fatal(err)
	}
	cursor := &sqlCursor{root: root, source: source, state: state, stage: &stage}
	p := &protocolPortal{producer: cursor}
	row := make([]sqltype.Value, 1)
	if calls != 0 {
		t.Fatal("producer initialization demanded target", calls)
	}
	for i := range 4 {
		present, err := p.next(context.Background(), row)
		if err != nil || !present || calls != i+1 || cursor.ordinalPosition != int64(i+1) {
			t.Fatal("finite demand replayed or looked ahead", present, calls, cursor.ordinalPosition, err)
		}
	}
	if present, err := p.next(context.Background(), row); err != nil || present || calls != 4 {
		t.Fatal("exhaustion projected an extra target", present, calls, err)
	}
	// Savepoint retirement uses the canonical birth frontier without rewinding
	// older demand or disposing a different transaction's exported owner.
	session := sessionStart(t, e, nil)
	owner := session.transactions
	p.tx, p.born = owner.ref, 1
	newRows, foreignRows := newFinalRows(nil, 0), newFinalRows(nil, 0)
	newPortal := &protocolPortal{tx: owner.ref, born: 2, rows: newRows}
	foreign := &protocolPortal{tx: owner.ref, born: 3, rows: foreignRows}
	foreign.tx.id++
	session.preparedSQL.portals = map[string]*protocolPortal{"old": p, "new": newPortal, "foreign": foreign}
	owner.mu.Lock()
	owner.retireProtocolPortalsAfter(1)
	owner.mu.Unlock()
	if p.retired || !newPortal.retired || foreign.retired || cursor.ordinalPosition != 4 {
		t.Fatal("savepoint frontier changed older position or foreign ownership")
	}
	if err := owner.drainCursorCleanup(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if !newRows.closed || foreignRows.closed || p.producer == nil || stage.closed || calls != 4 {
		t.Fatal("savepoint retirement leaked new rows or disposed retained producer")
	}
	if err = p.close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !stage.closed || cursor.root != nil || cursor.source != nil || cursor.stage != nil || p.producer != nil {
		t.Fatal("terminal close retained producer")
	}
	if err = p.close(context.Background()); err != nil || calls != 4 {
		t.Fatal("repeat close replayed/disposed inconsistently", err, calls)
	}
}
