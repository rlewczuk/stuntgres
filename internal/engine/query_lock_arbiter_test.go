package engine

import (
	"context"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

// Fixed package-owned occurrence order is deliberate here. A public PostgreSQL
// witness must not infer planner join order from which contributor was acquired.
func TestQueryLockArbiterSkipRetainsAcquiredContributor(t *testing.T) {
	e, a, b, c := rowLockEngineFixture(t)
	sessionSQL(t, c, "CREATE TABLE u(id INT,v INT)")
	sessionSQL(t, c, "INSERT INTO u VALUES(1,10)")
	ra := transactionBegin(t, a)
	t.Cleanup(func() { transactionEnd(t, a, ra, false) })
	rb := transactionBegin(t, b)
	t.Cleanup(func() { transactionEnd(t, b, rb, false) })
	tRef, uRef := roleTableRef(t, e, "t"), roleTableRef(t, e, "u")
	e.mu.Lock()
	tRow, uRow := e.storage.Relations[tRef.Address].Versions[0].Row, e.storage.Relations[uRef.Address].Versions[0].Row
	e.mu.Unlock()
	if err := e.locks.Acquire(context.Background(), a.transactions.ticket, lock.Resource{ObjectRef: uRef, Row: uRow}, lock.ForUpdate); err != nil {
		t.Fatal(err)
	}
	op, err := b.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	})
	raw := statement(t, "SELECT t.id FROM t CROSS JOIN u FOR UPDATE OF t SKIP LOCKED FOR UPDATE OF u SKIP LOCKED").(*parser.Select)
	// StartStatement creates the scope; gate attaches the live transaction ticket
	// and epoch before private relation admission, exactly as Execute does.
	if err := scope.gate(raw); err != nil {
		t.Fatal(err)
	}
	if err := scope.acquireRelation(raw, true); err != nil {
		t.Fatal(err)
	}
	marks, err := queryLockTargets(raw.From, raw.Locking)
	if err != nil {
		t.Fatal(err)
	}
	leaves := scope.querySources
	join := &joinNode{querySchema: append(append(querySchema(nil), leaves[0].relation.Columns...), leaves[1].relation.Columns...), left: &sourceNode{querySchema: leaves[0].relation.Columns, ref: tRef, occurrence: 0}, right: &sourceNode{querySchema: leaves[1].relation.Columns, ref: uRef, occurrence: 1}, leftWidth: len(leaves[0].relation.Columns), rightWidth: len(leaves[1].relation.Columns)}
	root := &rowLockNode{querySchema: join.Schema(), child: join, marks: marks, firstMode: lock.ForUpdate}
	e.mu.Lock()
	err = e.initializeQueryLocksLocked(scope, root, leaves)
	var inputs *querySource
	if err == nil {
		inputs, err = e.captureQuerySourcesLocked(scope, leaves, false)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer inputs.close()
	if err := selectQuerySources(op.Context(), inputs); err != nil {
		t.Fatal(err)
	}
	frame := append(append([]sqltype.Value(nil), querySourceAt(inputs, 0).row(0)...), querySourceAt(inputs, 1).row(0)...)
	candidate := &querySource{materialized: true, anonymous: [][]sqltype.Value{frame}, flatCount: 1, occurrences: []int{0, 1}, provenance: []int{0, 0}}
	child := indexedStage{source: candidate, schema: join.Schema(), pending: appendPendingRowMarks(nil, marks, inputs)}
	out, err := arbitrateViewQuery(op.Context(), &queryState{source: inputs}, root, child)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if _, present, err := out.nextOccurrence(op.Context()); err != nil || present {
		t.Fatal("partial joined candidate escaped SKIP", present, err)
	}
	lockState := e.locks.Inspect()
	first, second := lockState.Resources[lock.Resource{ObjectRef: tRef, Row: tRow}], lockState.Resources[lock.Resource{ObjectRef: uRef, Row: uRow}]
	bad := first == nil || first.Holders[b.transactions.ticket].Mode != lock.ForUpdate || second == nil || second.Holders[b.transactions.ticket].Mode != 0 || second.Holders[a.transactions.ticket].Mode != lock.ForUpdate || len(second.Pending) != 0
	if bad {
		t.Fatal("SKIP undid an acquired contributor, acquired unavailable source, or left a waiter")
	}
}
