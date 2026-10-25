package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Fixed package-owned occurrence order is deliberate here. A public PostgreSQL
// witness must not infer planner join order from which contributor was acquired.
func TestQueryLockArbiterSkipRetainsAcquiredContributor(t *testing.T) {
	e, a, b, c := rowLockEngineFixture(t)
	enginetest.SessionSQL(t, c, "CREATE TABLE u(id INT,v INT)")
	enginetest.SessionSQL(t, c, "INSERT INTO u VALUES(1,10)")
	ra := enginetest.TransactionBegin(t, a)
	t.Cleanup(func() { enginetest.TransactionEnd(t, a, ra, false) })
	rb := enginetest.TransactionBegin(t, b)
	t.Cleanup(func() { enginetest.TransactionEnd(t, b, rb, false) })
	tRef, uRef := enginetest.RoleTableRef(t, e, "t"), enginetest.RoleTableRef(t, e, "u")
	e.Mu.Lock()
	tRow, uRow := e.Storage.Relations[tRef.Address].Versions[0].Row, e.Storage.Relations[uRef.Address].Versions[0].Row
	e.Mu.Unlock()
	if err := e.Locks.Acquire(context.Background(), a.Transactions.Ticket, lock.Resource{ObjectRef: uRef, Row: uRow}, lock.ForUpdate); err != nil {
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
	raw := enginetest.Statement(t, "SELECT t.id FROM t CROSS JOIN u FOR UPDATE OF t SKIP LOCKED FOR UPDATE OF u SKIP LOCKED").(*parser.Select)
	// StartStatement creates the scope; gate attaches the live transaction ticket
	// and epoch before private relation admission, exactly as Execute does.
	if err := scope.Gate(raw); err != nil {
		t.Fatal(err)
	}
	if err := scope.AcquireRelation(raw, true); err != nil {
		t.Fatal(err)
	}
	marks, err := analyze.RowLockTargets(raw.From, raw.Locking)
	if err != nil {
		t.Fatal(err)
	}
	leaves := scope.QuerySources
	join := &plan.JoinNode{QuerySchema: append(append(plan.QuerySchema(nil), leaves[0].Relation.Columns...), leaves[1].Relation.Columns...), Left: &plan.SourceNode{QuerySchema: leaves[0].Relation.Columns, Ref: tRef, Occurrence: 0}, Right: &plan.SourceNode{QuerySchema: leaves[1].Relation.Columns, Ref: uRef, Occurrence: 1}, LeftWidth: len(leaves[0].Relation.Columns), RightWidth: len(leaves[1].Relation.Columns)}
	root := &plan.RowLockNode{QuerySchema: join.Schema(), Child: join, Marks: marks, FirstMode: lock.ForUpdate}
	e.Mu.Lock()
	err = e.InitializeQueryLocksLocked(scope, root, leaves)
	var inputs *exec.Source
	if err == nil {
		inputs, err = e.CaptureQuerySourcesLocked(scope, leaves, false)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer inputs.Close()
	if err := exec.SelectQuerySources(op.Context(), inputs); err != nil {
		t.Fatal(err)
	}
	frame := append(append([]sqltype.Value(nil), exec.QuerySourceAt(inputs, 0).Row(0)...), exec.QuerySourceAt(inputs, 1).Row(0)...)
	candidate := &exec.Source{Materialized: true, Anonymous: [][]sqltype.Value{frame}, FlatCount: 1, Occurrences: []int{0, 1}, Provenance: []int{0, 0}}
	child := exec.Stage{Source: candidate, Columns: join.Schema(), Pending: exec.AppendPendingRowMarks(nil, marks, inputs)}
	out, err := exec.ArbitrateViewQuery(op.Context(), &exec.State{Source: inputs}, root, child)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if _, present, err := out.NextOccurrence(op.Context()); err != nil || present {
		t.Fatal("partial joined candidate escaped SKIP", present, err)
	}
	lockState := e.Locks.Inspect()
	first, second := lockState.Resources[lock.Resource{ObjectRef: tRef, Row: tRow}], lockState.Resources[lock.Resource{ObjectRef: uRef, Row: uRow}]
	bad := first == nil || first.Holders[b.Transactions.Ticket].Mode != lock.ForUpdate || second == nil || second.Holders[b.Transactions.Ticket].Mode != 0 || second.Holders[a.Transactions.Ticket].Mode != lock.ForUpdate || len(second.Pending) != 0
	if bad {
		t.Fatal("SKIP undid an acquired contributor, acquired unavailable source, or left a waiter")
	}
}
