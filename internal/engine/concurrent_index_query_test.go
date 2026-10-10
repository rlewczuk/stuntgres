package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestConcurrentIndexHashInvalidQueryCapture(t *testing.T) {
	e := New()
	first, later, builder := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, first, "CREATE TABLE t(n INT)")
	sessionSQL(t, first, "INSERT INTO t VALUES(1)")
	sessionSQL(t, first, "BEGIN")
	sessionSQL(t, first, "INSERT INTO t VALUES(2)")
	cancel, await := indexMaintenanceAsync(t, e, builder, "CREATE INDEX CONCURRENTLY hi ON t USING hash(n)")
	// Observe actual manager completion requests, after the coordinator froze
	// its cohort; worker start and descriptor flags alone are not queue proof.
	concurrentUnitCompletion(t, e, first.transactions.ticket, builder)
	concurrentUnitIndex(t, e, "hi", false, false)
	sessionSQL(t, later, "BEGIN")
	sessionSQL(t, later, "INSERT INTO t VALUES(4)")
	sessionSQL(t, first, "COMMIT")
	concurrentUnitCompletion(t, e, later.transactions.ticket, builder)
	concurrentUnitIndex(t, e, "hi", true, false)
	cancel()
	if err := await(); !errors.Is(err, context.Canceled) {
		t.Fatal("builder cancellation", err)
	}
	sessionSQL(t, later, "COMMIT")
	index, incomplete := hashRoot(t, e, "hi")
	if !index.sqlIndex.ready || index.sqlIndex.valid || !index.sqlIndex.live {
		t.Fatal("cancellation/grandfathered commit changed retained flags", index.sqlIndex)
	}
	if got := hashKeys(t, incomplete); !reflect.DeepEqual(got, []string{"1", "2"}) {
		t.Fatal("fixture did not retain the genuinely incomplete bucket root", got)
	}
	e.mu.Lock()
	store := e.storage[index.sqlIndex.parent]
	currentIdentity := incomplete.storage == store && incomplete.generation == store.generation && incomplete.prefix == len(store.versions) && incomplete.ends == store.ends && len(store.versions) == 3
	e.mu.Unlock()
	if !currentIdentity {
		t.Fatal("fixture must satisfy existing storage identity proofs despite incomplete coverage")
	}
	query, source := hashImplicitQuery(t, e, "SELECT n FROM t WHERE n=4")
	if len(source.hashIndexes) != 0 {
		t.Fatal("invalid root was selected for query capture", source.hashIndexes)
	}
	stage := hashFilterStage(t, query, source, nil)
	if stage.hashCandidates != nil {
		t.Fatal("invalid root supplied filter candidates", stage.hashCandidates)
	}
	if err := stage.drain(context.Background()); err != nil || stage.count() != 1 {
		t.Fatal("heap fallback lost grandfathered row", stage.count(), err)
	}
	stage.Close()
	sessionSQL(t, first, "REINDEX INDEX hi")
	healthyIndex, healthy := hashRoot(t, e, "hi")
	if !healthyIndex.sqlIndex.ready || !healthyIndex.sqlIndex.valid || !healthyIndex.sqlIndex.live || healthy == incomplete {
		t.Fatal("successful reconstruction did not publish a healthy new root")
	}
	if got := hashKeys(t, healthy); !reflect.DeepEqual(got, []string{"1", "2", "4"}) {
		t.Fatal("reconstruction omitted committed row", got)
	}
	query, source = hashImplicitQuery(t, e, "SELECT n FROM t WHERE n=4")
	if len(source.hashIndexes) != 1 || source.hashIndexes[0].root != healthy {
		t.Fatal("healthy actual root was not captured", source.hashIndexes)
	}
	stage = hashFilterStage(t, query, source, nil)
	if stage.hashCandidates == nil || stage.hashCandidates.root != healthy || len(stage.hashCandidates.occurrences) != 1 {
		t.Fatal("healthy equality bucket was not selected", stage.hashCandidates)
	}
	if err := stage.drain(context.Background()); err != nil || stage.count() != 1 {
		t.Fatal("healthy lookup lost committed row", stage.count(), err)
	}
	stage.Close()
	if got := hashKeys(t, incomplete); !reflect.DeepEqual(got, []string{"1", "2"}) {
		t.Fatal("query/reconstruction mutated retained invalid root", got)
	}
	lockEmpty(t, e)
}
