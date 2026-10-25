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
	old := e.Catalog.State.MustRelation(ref.Address)
	storage := e.Storage.Relations[ref.Address]
	generator := e.Storage.Sequences[seq.Address]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	original := e.Catalog.Providers[ids.RelationClassID]
	calls := 0
	e.Catalog.Providers[ids.RelationClassID] = alterDropProvider{original, func() {
		calls++
		if !e.Mu.TryLock() {
			t.Error("ADD called real provider under Engine.mu")
		} else {
			e.Mu.Unlock()
		}
		if generator.Last != 6 || e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage || old.ColumnMeta.HighWater != 1 {
			t.Error("provider did not see fully demanded unpublished ADD", generator.Last)
		}
		cancel()
	}}
	op, err := worker.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, statement(t, "ALTER TABLE t ADD b BIGINT DEFAULT nextval('s') CHECK(nextval('s')>0)"), 0, nil)
	op.Done()
	e.Catalog.Providers[ids.RelationClassID] = original
	if !errors.Is(err, context.Canceled) || calls != 1 || generator.Last != 6 || e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage || old.ColumnMeta.HighWater != 1 {
		t.Fatal("final provider veto published candidate or rewound effects", err, calls, generator.Last)
	}
	lockEmpty(t, e)
	if !e.ReplacementMu.TryLock() {
		t.Fatal("final provider veto leaked replacement reservation")
	}
	e.ReplacementMu.Unlock()
	transactionRows(t, worker, "SELECT nextval('s')", 7)
	sessionSQL(t, worker, "ALTER TABLE t ADD b INT DEFAULT 7 CHECK(b>0)")
	checkReviewDependencyValid(t, e)
	lockEmpty(t, e)
}
