package core_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
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

func hashRoot(t *testing.T, e *engine.Engine, name string) (*catalog.Relation, *storage.HashRoot) {
	t.Helper()
	index, carrier := btreeRoot(t, e, name)
	if index.SQLIndex.Method != catalog.HashMethod || carrier.Hash == nil || len(carrier.Entries) != 0 {
		t.Fatal("not a hash root", index, carrier)
	}
	return index, carrier.Hash
}

func hashKeys(t *testing.T, root *storage.HashRoot) []string {
	t.Helper()
	keys := []string{}
	versions := map[ids.VersionID]bool{}
	for _, bucket := range root.Buckets {
		for _, entry := range bucket {
			if entry.Key.IsNull() || entry.Row == 0 || entry.Version == 0 || versions[entry.Version] || entry.Position < 0 || entry.Position >= root.Prefix {
				t.Fatal("invalid/collapsed hash identity", entry)
			}
			versions[entry.Version] = true
			s, err := entry.Key.Type().Output(context.Background(), entry.Key)
			if err != nil {
				t.Fatal(err)
			}
			keys = append(keys, s)
		}
	}
	slices.Sort(keys)
	return keys
}

func hashImplicitQuery(t *testing.T, e *engine.Engine, q string) (*plan.Query, *exec.Source, func()) {
	t.Helper()
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(done)
	query, source, release := hashCaptureQuery(t, e, op, q)
	return query, source, func() {
		release()
		done()
	}
}

func hashFilterStage(t *testing.T, query *plan.Query, source *exec.Source, parameters []sqltype.Value) exec.Stage {
	t.Helper()
	filter := exec.HashQueryFilter(query.Root)
	if filter == nil {
		t.Fatal("fixture has no top-level filter")
	}
	state := &exec.State{Source: source, Parameters: parameters, HashFilter: filter, Frame: query.Root.(*plan.ProjectNode).Frame}
	if scope := engine.ScopeOf(source.LiveHost()); scope != nil {
		state.Input, state.Output = scope.Input, scope.Output
	}
	stage, err := exec.OpenNode(context.Background(), state, filter)
	if err != nil {
		t.Fatal(err)
	}
	return stage
}

func btreeRoot(t *testing.T, e *engine.Engine, name string) (*catalog.Relation, *storage.IndexRoot) {
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

// Follow the existing queryJoinsFixture capture seam, but on the actual hash
// owner's engine. No public adapter or testing-only production switch is used.
func hashCaptureQuery(t *testing.T, e *engine.Engine, op *session.Operation, q string) (*plan.Query, *exec.Source, func()) {
	t.Helper()
	ast := enginetest.Statement(t, q)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scope.Describe(ast); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var bound analyze.Binding
	if err == nil {
		bound, err = engine.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
	}
	var source *exec.Source
	if err == nil {
		// Logical-stage fixtures supply cancellation contexts directly, but keep
		// the actual admission alive until every retained stage is closed.
		source, err = e.CaptureQuerySourcesForInvokerLocked(scope, bound.QuerySources, false, authorization.PrincipalRef{}, plan.RowMark{}, scope.QueryLockContext)
	}
	e.Mu.Unlock()
	if err == nil {
		err = exec.SelectQuerySources(op.Ctx, source)
	}
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		source.Close()
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(release)
	return bound.Query, source, release
}
