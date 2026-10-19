package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestAlterAddColumnFinalProviderVeto(t *testing.T) {
	e := New()
	worker := sessionStart(t, e, nil)
	sessionSQL(t, worker, "CREATE SEQUENCE s START 1 CACHE 1")
	sessionSQL(t, worker, "CREATE TABLE t(id INT)")
	sessionSQL(t, worker, "INSERT INTO t VALUES(1),(2),(2)")
	ref := roleTableRef(t, e, "t")
	seq := roleTableRef(t, e, "s")
	old := e.catalog.state.MustRelation(ref.Address)
	storage := e.storage.relations[ref.Address]
	generator := e.storage.sequences[seq.Address]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	original := e.catalog.Providers[ids.RelationClassID]
	calls := 0
	e.catalog.Providers[ids.RelationClassID] = alterDropProvider{original, func() {
		calls++
		if !e.mu.TryLock() {
			t.Error("ADD called real provider under Engine.mu")
		} else {
			e.mu.Unlock()
		}
		if generator.last != 6 || e.catalog.state.Get(ref.Address) != old || e.storage.relations[ref.Address] != storage || old.AttributeHighWater != 1 {
			t.Error("provider did not see fully demanded unpublished ADD", generator.last)
		}
		cancel()
	}}
	op, err := worker.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, statement(t, "ALTER TABLE t ADD b BIGINT DEFAULT nextval('s') CHECK(nextval('s')>0)"), 0, nil)
	op.Done()
	e.catalog.Providers[ids.RelationClassID] = original
	if !errors.Is(err, context.Canceled) || calls != 1 || generator.last != 6 || e.catalog.state.Get(ref.Address) != old || e.storage.relations[ref.Address] != storage || old.AttributeHighWater != 1 {
		t.Fatal("final provider veto published candidate or rewound effects", err, calls, generator.last)
	}
	lockEmpty(t, e)
	if !e.replacementMu.TryLock() {
		t.Fatal("final provider veto leaked replacement reservation")
	}
	e.replacementMu.Unlock()
	transactionRows(t, worker, "SELECT nextval('s')", 7)
	sessionSQL(t, worker, "ALTER TABLE t ADD b INT DEFAULT 7 CHECK(b>0)")
	checkReviewDependencyValid(t, e)
	lockEmpty(t, e)
}
