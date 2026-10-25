package engine

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func hashCancellationRows(rows []storage.RowVersion) []storage.RowVersion {
	copy := append([]storage.RowVersion(nil), rows...)
	for i := range copy {
		copy[i].Values = append([]sqltype.Value(nil), rows[i].Values...)
	}
	return copy
}

func hashCancellationBuckets(root *storage.HashRoot) map[uint64][]storage.HashEntry {
	copy := make(map[uint64][]storage.HashEntry, len(root.Buckets))
	for hash, bucket := range root.Buckets {
		copy[hash] = append([]storage.HashEntry(nil), bucket...)
	}
	return copy
}

func TestHashIndexLookupCancellation(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(2),(2),(2),(1),(NULL)", "CREATE INDEX hn ON t USING hash(n)"} {
		run(t, e, q)
	}
	index, root := hashRoot(t, e, "hn")
	query, source, release := hashImplicitQuery(t, e, "SELECT n FROM t WHERE n=2")
	defer release()
	if len(source.HashIndexes) != 1 || source.HashIndexes[0].Root != root {
		t.Fatal("cancellation fixture did not retain the real root")
	}
	filter := exec.HashQueryFilter(query.Root)
	if filter == nil {
		t.Fatal("missing real filter")
	}
	state := &exec.State{Source: source, HashFilter: filter, Frame: query.Root.(*plan.ProjectNode).Frame}
	if scope := ScopeOf(source.LiveHost()); scope != nil {
		state.Input, state.Output = scope.Input, scope.Output
	}
	view, data := source.View, source.View.Data()
	rows, visible := hashCancellationRows(source.Committed), append([]int(nil), source.Visible...)
	first := &source.Committed[0]
	buckets := hashCancellationBuckets(root)
	before := indexMaintenanceCapture(e)
	unchanged := func() {
		indexMaintenanceUnchanged(t, e, before)
		if source.View != view || source.View.Data() != data || &source.Committed[0] != first || !reflect.DeepEqual(source.Committed, rows) || !reflect.DeepEqual(source.Visible, visible) || len(source.HashIndexes) != 1 || source.HashIndexes[0].Root != root || !reflect.DeepEqual(root.Buckets, buckets) || e.Storage.IndexRoots[index.Address()].Hash != root {
			t.Fatal("canceled lookup changed retained source/root authority")
		}
	}

	// Calibrate the actual filter opening, including hashing and every duplicate
	// equality check. The penultimate poll is inside that probe, not precancel.
	calibration, stop := context.WithCancel(context.Background())
	defer stop()
	counting := &cancelAtErrCheck{Context: calibration, cancel: stop}
	opened, err := exec.OpenNode(counting, state, filter)
	if err != nil {
		t.Fatal(err)
	}
	if opened.HashCandidates == nil || opened.HashCandidates.Root != root || len(opened.HashCandidates.Occurrences) != 3 {
		t.Fatal("calibration did not execute the duplicate bucket", opened.HashCandidates)
	}
	polls := counting.checks.Load()
	opened.Close()
	if polls <= 2 {
		t.Fatal("probe did not perform cancellable work", polls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	duringProbe := &cancelAtErrCheck{Context: ctx, cancel: cancel, at: polls - 1}
	failed, err := exec.OpenNode(duringProbe, state, filter)
	if !errors.Is(err, context.Canceled) || duringProbe.checks.Load() != duringProbe.at || failed.Source != nil || failed.Produce != nil || failed.HashCandidates != nil {
		failed.Close()
		t.Fatal("canceled probe returned a successful/partial stage", err, duringProbe.checks.Load(), failed)
	}
	unchanged()

	// Also cancel the real residual consumer after one admitted occurrence. Its
	// terminal error must survive a later drain with a fresh context.
	warm, err := exec.OpenNode(context.Background(), state, filter)
	if err != nil {
		t.Fatal(err)
	}
	firstContext, firstCancel := context.WithCancel(context.Background())
	defer firstCancel()
	firstCounting := &cancelAtErrCheck{Context: firstContext, cancel: firstCancel}
	if _, present, err := warm.NextOccurrence(firstCounting); err != nil || !present || warm.HashCandidates.Position != 1 {
		warm.Close()
		t.Fatal("residual calibration did not consume one candidate", present, err)
	}
	firstPolls := firstCounting.checks.Load()
	warm.Close()
	consuming, err := exec.OpenNode(context.Background(), state, filter)
	if err != nil {
		t.Fatal(err)
	}
	candidates := consuming.HashCandidates
	if candidates == nil || candidates.Root != root {
		t.Fatal("consumer fell back to scan")
	}
	consumeContext, consumeCancel := context.WithCancel(context.Background())
	defer consumeCancel()
	duringConsumption := &cancelAtErrCheck{Context: consumeContext, cancel: consumeCancel, at: firstPolls + 1}
	if err := consuming.Drain(duringConsumption); !errors.Is(err, context.Canceled) || candidates.Position != 1 || !errors.Is(consuming.Terminal, context.Canceled) {
		consuming.Close()
		t.Fatal("residual cancellation exposed a successful finite result", err, candidates.Position)
	}
	if err := consuming.Drain(context.Background()); !errors.Is(err, context.Canceled) {
		consuming.Close()
		t.Fatal("fresh context revived a canceled stage", err)
	}
	consuming.Close()
	unchanged()
	fresh := hashFilterStage(t, query, source, nil)
	defer fresh.Close()
	if fresh.HashCandidates == nil || fresh.HashCandidates.Root != root || len(fresh.HashCandidates.Occurrences) != 3 {
		t.Fatal("fresh lookup did not reuse retained authority")
	}
	if err := fresh.Drain(context.Background()); err != nil || fresh.Count() != 3 || fresh.HashCandidates.Position != 3 {
		t.Fatal("fresh lookup did not recover", err)
	}
}

// Reuse the repository's phase-aware stack observation and cancelAtErrCheck
// seam. Only Err polls from the actual hash merge count; admission and final
// owner checks cannot accidentally stand in for mid-merge cancellation.
type hashMergeCancellationContext struct{ *cancelAtErrCheck }

func (c *hashMergeCancellationContext) Err() error {
	var pcs [32]uintptr
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, ".MergeHashIndexRoot") {
			return c.cancelAtErrCheck.Err()
		}
		if !more {
			break
		}
	}
	return c.Context.Err()
}

func TestHashIndexMergeCancellation(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(1),(2),(2),(3),(NULL)", "CREATE INDEX hn ON t USING hash(n)"} {
		run(t, e, q)
	}
	idx, old := hashRoot(t, e, "hn")
	parent := e.Catalog.State.MustRelation(idx.SQLIndex.Parent)
	store := e.Storage.Relations[parent.Address()]
	writer := sessionStart(t, e, nil)
	ref := transactionBegin(t, writer)
	active := true
	t.Cleanup(func() {
		if active {
			transactionEnd(t, writer, ref, false)
		}
	})
	changes := []string{"UPDATE t SET n=4 WHERE n=2", "DELETE FROM t WHERE n=1", "INSERT INTO t VALUES(5)"}
	for _, q := range changes {
		sessionSQL(t, writer, q)
	}
	writes, deletes := writer.Transactions.Writes, writer.Transactions.Deletes
	if len(writes[parent.Address()]) != 3 || deletes[parent.Address()] == nil {
		t.Fatal("fixture did not stage a genuine multirow change")
	}
	before := indexMaintenanceCapture(e)
	rows, buckets := hashCancellationRows(store.Versions), hashCancellationBuckets(old)
	generation, ends, frontier := store.Generation, store.Ends, e.Visibility.CommitSeq()

	// Dry preparation uses the same real private batch and slot/end owner as
	// publication. Its final pre-return poll follows touched-bucket copying;
	// cancel at the immediately preceding bucket-entry poll in the real owner.
	p := index.RowPublication{Address: parent.Address(), Store: store, Original: store, OriginalRelation: parent, Relation: parent, Root: store.Versions, Writes: writes[parent.Address()], Ends: store.Ends, Deletion: deletes[parent.Address()], Generation: store.Generation, SingleSlot: -1}
	if err := index.PrepareRowPublication(&p, e.Catalog.Domain, ref, true); err != nil {
		t.Fatal(err)
	}
	calibration, stop := context.WithCancel(context.Background())
	defer stop()
	counting := &cancelAtErrCheck{Context: calibration, cancel: stop}
	candidate, err := index.MergeHashIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: counting}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, parent, idx, e.Storage.IndexRoots[idx.Address()], &p, false, true)
	if err != nil || candidate == nil || candidate.Hash == nil || !reflect.DeepEqual(hashKeys(t, candidate.Hash), []string{"3", "4", "4", "5"}) {
		t.Fatal("real merge calibration failed", candidate, err)
	}
	polls := counting.checks.Load()
	if polls <= int32(len(p.Writes))+2 {
		t.Fatal("merge did not reach bucket copying", polls)
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, err := writer.Acquire(base)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	during := &hashMergeCancellationContext{cancelAtErrCheck: &cancelAtErrCheck{Context: op.Context(), cancel: cancel, at: polls - 1}}
	op.Ctx = during
	err = e.publishRows(op, ref, writes, deletes, nil, nil, nil)
	op.Done()
	if !errors.Is(err, context.Canceled) || during.checks.Load() != during.at {
		t.Fatal("owner did not cancel inside real hash merge", err, during.checks.Load(), during.at)
	}
	indexMaintenanceUnchanged(t, e, before)
	if store.Generation != generation || store.Ends != ends || e.Visibility.CommitSeq() != frontier || !reflect.DeepEqual(store.Versions, rows) || !reflect.DeepEqual(old.Buckets, buckets) || !reflect.DeepEqual(hashKeys(t, old), []string{"1", "2", "2", "3"}) {
		t.Fatal("canceled merge published rows/endings or mutated predecessor buckets")
	}
	transactionEnd(t, writer, ref, false)
	active = false

	ref = transactionBegin(t, writer)
	active = true
	for _, q := range changes {
		sessionSQL(t, writer, q)
	}
	if !transactionEnd(t, writer, ref, true).Committed {
		t.Fatal("fresh mutation did not commit")
	}
	active = false
	_, fresh := hashRoot(t, e, "hn")
	if fresh == old || !reflect.DeepEqual(hashKeys(t, fresh), []string{"3", "4", "4", "5"}) || !reflect.DeepEqual(old.Buckets, buckets) || !reflect.DeepEqual(hashKeys(t, old), []string{"1", "2", "2", "3"}) {
		t.Fatal("fresh mutation failed or changed retained predecessor")
	}
}
