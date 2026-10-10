package engine

import (
	"context"
	"maps"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

// Follow the existing queryJoinsFixture capture seam, but on the actual hash
// owner's engine. No public adapter or testing-only production switch is used.
func hashCaptureQuery(t *testing.T, e *Engine, op *Operation, q string) (*BoundQuery, *querySource, func()) {
	t.Helper()
	ast := statement(t, q)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scope.Describe(ast); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	var bound binding
	if err == nil {
		bound, err = e.describe(op.ctx, ast, auth, scope.lockedRef, scope.input, op, nil, &scope.queryLockAdmission)
	}
	var source *querySource
	if err == nil {
		// Logical-stage fixtures supply cancellation contexts directly, but keep
		// the actual admission alive until every retained stage is closed.
		source, err = e.captureQuerySourcesForInvokerLocked(scope, bound.querySources, false, PrincipalRef{}, queryRowMark{}, scope.queryLocks)
	}
	e.mu.Unlock()
	if err == nil {
		err = selectQuerySources(op.ctx, source)
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
		source.close()
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(release)
	return bound.query, source, release
}
func hashImplicitQuery(t *testing.T, e *Engine, q string) (*BoundQuery, *querySource, func()) {
	t.Helper()
	op, done, err := testOperation(e, context.Background())
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
func hashFilterStage(t *testing.T, query *BoundQuery, source *querySource, parameters []sqltype.Value) indexedStage {
	t.Helper()
	filter := hashQueryFilter(query.Root)
	if filter == nil {
		t.Fatal("fixture has no top-level filter")
	}
	state := &queryState{source: source, parameters: parameters, hashFilter: filter, frame: query.Root.(*projectNode).frame}
	if scope := source.liveScope(); scope != nil {
		state.input, state.output = scope.input, scope.output
	}
	stage, err := openQueryNode(context.Background(), state, filter)
	if err != nil {
		t.Fatal(err)
	}
	return stage
}

func TestHashIndexLookupFeatureSmoke(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE TABLE t(n INT,s TEXT)", "INSERT INTO t VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')", "CREATE INDEX hn ON t USING hash(n)"} {
		run(t, e, q)
	}
	_, root := hashRoot(t, e, "hn")
	if got := hashKeys(t, root); !reflect.DeepEqual(got, []string{"1", "2", "2", "3"}) {
		t.Fatal(got)
	}
	for _, q := range []string{"SELECT n FROM t WHERE n=2", "SELECT n FROM t WHERE 2=n"} {
		query, source, release := hashImplicitQuery(t, e, q)
		if len(source.hashIndexes) != 1 || source.hashIndexes[0].root != root {
			t.Fatal("live capture did not retain actual root", source.hashIndexes)
		}
		stage := hashFilterStage(t, query, source, nil)
		if stage.hashCandidates == nil || stage.hashCandidates.root != root || len(stage.hashCandidates.occurrences) != 2 {
			t.Fatal("real filter did not prune to duplicate bucket", stage.hashCandidates)
		}
		if err := stage.drain(context.Background()); err != nil {
			t.Fatal(err)
		}
		if stage.count() != 2 || stage.hashCandidates.position != 2 {
			t.Fatal("candidates were not consumed by original predicate", stage.count(), stage.hashCandidates)
		}
		stage.Close()
		release()
	}
}

func TestHashIndexLookupParametersResidualAndFallback(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE TABLE t(n INT,s TEXT)", "INSERT INTO t VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')", "CREATE INDEX hn ON t USING hash(n)"} {
		run(t, e, q)
	}
	for _, tc := range []struct {
		q     string
		count int
	}{
		{"SELECT n FROM t WHERE n>1", 3}, {"SELECT n FROM t WHERE n IS NULL", 1}, {"SELECT n FROM t WHERE n=2 AND s='b'", 2},
		{"SELECT n FROM t WHERE n=2::bigint", 2}, {"SELECT n FROM t WHERE n::bigint=2::bigint", 2},
	} {
		query, source, release := hashImplicitQuery(t, e, tc.q)
		stage := hashFilterStage(t, query, source, nil)
		if stage.hashCandidates != nil {
			t.Fatal("unsupported predicate probed", tc.q)
		}
		if err := stage.drain(context.Background()); err != nil || stage.count() != tc.count {
			t.Fatal(tc.q, stage.count(), err)
		}
		stage.Close()
		release()
	}
	for _, tc := range []struct {
		probe string
		count int
	}{{"2", 2}, {"1", 1}, {"99", 0}, {"NULL", 0}} {
		query, source, release := hashImplicitQuery(t, e, "SELECT n FROM t WHERE n=$1")
		u := source.hashIndexes[0].usage
		value := sqltype.Null(u.Type)
		if tc.probe != "NULL" {
			var err error
			value, err = u.Type.Input(context.Background(), tc.probe, -1)
			if err != nil {
				t.Fatal(err)
			}
		}
		stage := hashFilterStage(t, query, source, []sqltype.Value{value})
		if stage.hashCandidates == nil || len(stage.hashCandidates.occurrences) != tc.count {
			t.Fatal("parameter was not rebound per execution", tc, stage.hashCandidates)
		}
		if err := stage.drain(context.Background()); err != nil || stage.count() != tc.count {
			t.Fatal(tc, stage.count(), err)
		}
		stage.Close()
		release()
	}
	query, source, release := hashImplicitQuery(t, e, "SELECT n FROM t WHERE n=2")
	original := source.hashIndexes[0].root
	clone := *original
	clone.buckets = maps.Clone(original.buckets)
	probe := source.committed[0].values[0]
	h, err := clone.binding.provider.Hash(context.Background(), probe)
	if err != nil {
		t.Fatal(err)
	}
	one := source.committed[1]
	// One forced hash collision is rejected by class equality; a false-positive
	// stored key passes that guard but must fail the original SQL comparison.
	clone.buckets[h] = append(append([]hashIndexEntry(nil), clone.buckets[h]...), hashIndexEntry{key: one.values[0], row: one.row, version: one.version, position: 1}, hashIndexEntry{key: probe, row: one.row, version: one.version, position: 1})
	source.hashIndexes[0].root = &clone
	stage := hashFilterStage(t, query, source, nil)
	if stage.hashCandidates == nil || len(stage.hashCandidates.occurrences) != 3 {
		t.Fatal("collision/equality guard not exercised", stage.hashCandidates)
	}
	if err := stage.drain(context.Background()); err != nil || stage.count() != 2 {
		t.Fatal("residual predicate not exercised", stage.count(), err)
	}
	stage.Close()
	release()
	if got := hashKeys(t, original); !reflect.DeepEqual(got, []string{"1", "2", "2", "3"}) {
		t.Fatal("collision fixture mutated retained root", got)
	}
	for _, q := range []string{"SELECT n FROM t ORDER BY n", "SELECT n FROM (SELECT n FROM t WHERE n=2) d", "SELECT a.n FROM t a JOIN t b ON a.n=b.n WHERE a.n=2"} {
		query, _, release := hashImplicitQuery(t, e, q)
		if hashQueryFilter(query.Root) != nil {
			t.Fatal("order/derived/join filter admitted", q)
		}
		release()
	}
}
