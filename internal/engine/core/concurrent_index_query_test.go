package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
)

func TestConcurrentIndexHashInvalidQueryCapture(t *testing.T) {
	e := engine.New()
	first, later, builder := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, first, "CREATE TABLE t(n INT)")
	enginetest.SessionSQL(t, first, "INSERT INTO t VALUES(1)")
	enginetest.SessionSQL(t, first, "BEGIN")
	enginetest.SessionSQL(t, first, "INSERT INTO t VALUES(2)")
	cancel, await := indexMaintenanceAsync(t, e, builder, "CREATE INDEX CONCURRENTLY hi ON t USING hash(n)")
	// Observe actual manager completion requests, after the coordinator froze
	// its cohort; worker start and descriptor flags alone are not queue proof.
	concurrentUnitCompletion(t, e, first.Transactions.Ticket, builder)
	concurrentUnitIndex(t, e, "hi", false, false)
	enginetest.SessionSQL(t, later, "BEGIN")
	enginetest.SessionSQL(t, later, "INSERT INTO t VALUES(4)")
	enginetest.SessionSQL(t, first, "COMMIT")
	concurrentUnitCompletion(t, e, later.Transactions.Ticket, builder)
	concurrentUnitIndex(t, e, "hi", true, false)
	cancel()
	if err := await(); !errors.Is(err, context.Canceled) {
		t.Fatal("builder cancellation", err)
	}
	enginetest.SessionSQL(t, later, "COMMIT")
	index, incomplete := hashRoot(t, e, "hi")
	if !index.SQLIndex.Ready || index.SQLIndex.Valid || !index.SQLIndex.Live {
		t.Fatal("cancellation/grandfathered commit changed retained flags", index.SQLIndex)
	}
	if got := hashKeys(t, incomplete); !reflect.DeepEqual(got, []string{"1", "2"}) {
		t.Fatal("fixture did not retain the genuinely incomplete bucket root", got)
	}
	e.Mu.Lock()
	store := e.Storage.Relations[index.SQLIndex.Parent]
	currentIdentity := incomplete.Storage == store && incomplete.Generation == store.Generation && incomplete.Prefix == len(store.Versions) && incomplete.Ends == store.Ends && len(store.Versions) == 3
	e.Mu.Unlock()
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
	enginetest.SessionSQL(t, first, "REINDEX INDEX hi")
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
	enginetest.LockEmpty(t, e)
}

func btreeRoot(t *testing.T, e *core.Engine, name string) (*catalog.Relation, *storage.IndexRoot) {
	t.Helper()
	ref := enginetest.RoleTableRef(t, e, name)
	idx := e.Catalog.State.MustRelation(ref.Address)
	root := e.Storage.IndexRoots[ref.Address]
	if idx.RelationKind != catalog.IndexRelation || idx.SQLIndex == nil || root == nil || e.Storage.Relations[ref.Address] != nil {
		t.Fatal("not a substantive index", idx, root)
	}
	if err := index.ValidateIndexDefinition(e.CatalogRead(), idx); err != nil {
		t.Fatal(err)
	}
	return idx, root
}
