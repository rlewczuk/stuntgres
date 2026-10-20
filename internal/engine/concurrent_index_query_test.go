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
	concurrentUnitCompletion(t, e, first.Transactions.Ticket, builder)
	concurrentUnitIndex(t, e, "hi", false, false)
	sessionSQL(t, later, "BEGIN")
	sessionSQL(t, later, "INSERT INTO t VALUES(4)")
	sessionSQL(t, first, "COMMIT")
	concurrentUnitCompletion(t, e, later.Transactions.Ticket, builder)
	concurrentUnitIndex(t, e, "hi", true, false)
	cancel()
	if err := await(); !errors.Is(err, context.Canceled) {
		t.Fatal("builder cancellation", err)
	}
	sessionSQL(t, later, "COMMIT")
	index, incomplete := hashRoot(t, e, "hi")
	if !index.SQLIndex.Ready || index.SQLIndex.Valid || !index.SQLIndex.Live {
		t.Fatal("cancellation/grandfathered commit changed retained flags", index.SQLIndex)
	}
	if got := hashKeys(t, incomplete); !reflect.DeepEqual(got, []string{"1", "2"}) {
		t.Fatal("fixture did not retain the genuinely incomplete bucket root", got)
	}
	e.mu.Lock()
	store := e.storage.Relations[index.SQLIndex.Parent]
	currentIdentity := incomplete.Storage == store && incomplete.Generation == store.Generation && incomplete.Prefix == len(store.Versions) && incomplete.Ends == store.Ends && len(store.Versions) == 3
	e.mu.Unlock()
	if !currentIdentity {
		t.Fatal("fixture must satisfy existing storage identity proofs despite incomplete coverage")
	}
	query, source, release := hashImplicitQuery(t, e, "SELECT n FROM t WHERE n=4")
	if len(source.HashIndexes) != 0 {
		t.Fatal("invalid root was selected for query capture", source.HashIndexes)
	}
	stage := hashFilterStage(t, query, source, nil)
	if stage.HashCandidates != nil {
		t.Fatal("invalid root supplied filter candidates", stage.HashCandidates)
	}
	if err := stage.Drain(context.Background()); err != nil || stage.Count() != 1 {
		t.Fatal("heap fallback lost grandfathered row", stage.Count(), err)
	}
	stage.Close()
	release()
	sessionSQL(t, first, "REINDEX INDEX hi")
	healthyIndex, healthy := hashRoot(t, e, "hi")
	if !healthyIndex.SQLIndex.Ready || !healthyIndex.SQLIndex.Valid || !healthyIndex.SQLIndex.Live || healthy == incomplete {
		t.Fatal("successful reconstruction did not publish a healthy new root")
	}
	if got := hashKeys(t, healthy); !reflect.DeepEqual(got, []string{"1", "2", "4"}) {
		t.Fatal("reconstruction omitted committed row", got)
	}
	query, source, release = hashImplicitQuery(t, e, "SELECT n FROM t WHERE n=4")
	if len(source.HashIndexes) != 1 || source.HashIndexes[0].Root != healthy {
		t.Fatal("healthy actual root was not captured", source.HashIndexes)
	}
	stage = hashFilterStage(t, query, source, nil)
	if stage.HashCandidates == nil || stage.HashCandidates.Root != healthy || len(stage.HashCandidates.Occurrences) != 1 {
		t.Fatal("healthy equality bucket was not selected", stage.HashCandidates)
	}
	if err := stage.Drain(context.Background()); err != nil || stage.Count() != 1 {
		t.Fatal("healthy lookup lost committed row", stage.Count(), err)
	}
	stage.Close()
	release()
	if got := hashKeys(t, incomplete); !reflect.DeepEqual(got, []string{"1", "2"}) {
		t.Fatal("query/reconstruction mutated retained invalid root", got)
	}
	lockEmpty(t, e)
}
