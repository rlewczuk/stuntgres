package engine

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"

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
	if len(source.hashIndexes) != 1 || source.hashIndexes[0].root != root {
		t.Fatal("cancellation fixture did not retain the real root")
	}
	filter := hashQueryFilter(query.Root)
	if filter == nil {
		t.Fatal("missing real filter")
	}
	state := &queryState{source: source, hashFilter: filter, frame: query.Root.(*projectNode).frame}
	if scope := source.liveScope(); scope != nil {
		state.input, state.output = scope.input, scope.output
	}
	view, data := source.view, source.view.read()
	rows, visible := hashCancellationRows(source.committed), append([]int(nil), source.visible...)
	first := &source.committed[0]
	buckets := hashCancellationBuckets(root)
	before := indexMaintenanceCapture(e)
	unchanged := func() {
		indexMaintenanceUnchanged(t, e, before)
		if source.view != view || source.view.read() != data || &source.committed[0] != first || !reflect.DeepEqual(source.committed, rows) || !reflect.DeepEqual(source.visible, visible) || len(source.hashIndexes) != 1 || source.hashIndexes[0].root != root || !reflect.DeepEqual(root.Buckets, buckets) || e.storage.IndexRoots[index.Address()].Hash != root {
			t.Fatal("canceled lookup changed retained source/root authority")
		}
	}

	// Calibrate the actual filter opening, including hashing and every duplicate
	// equality check. The penultimate poll is inside that probe, not precancel.
	calibration, stop := context.WithCancel(context.Background())
	defer stop()
	counting := &cancelAtErrCheck{Context: calibration, cancel: stop}
	opened, err := openQueryNode(counting, state, filter)
	if err != nil {
		t.Fatal(err)
	}
	if opened.hashCandidates == nil || opened.hashCandidates.root != root || len(opened.hashCandidates.occurrences) != 3 {
		t.Fatal("calibration did not execute the duplicate bucket", opened.hashCandidates)
	}
	polls := counting.checks.Load()
	opened.Close()
	if polls <= 2 {
		t.Fatal("probe did not perform cancellable work", polls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	duringProbe := &cancelAtErrCheck{Context: ctx, cancel: cancel, at: polls - 1}
	failed, err := openQueryNode(duringProbe, state, filter)
	if !errors.Is(err, context.Canceled) || duringProbe.checks.Load() != duringProbe.at || failed.source != nil || failed.produce != nil || failed.hashCandidates != nil {
		failed.Close()
		t.Fatal("canceled probe returned a successful/partial stage", err, duringProbe.checks.Load(), failed)
	}
	unchanged()

	// Also cancel the real residual consumer after one admitted occurrence. Its
	// terminal error must survive a later drain with a fresh context.
	warm, err := openQueryNode(context.Background(), state, filter)
	if err != nil {
		t.Fatal(err)
	}
	firstContext, firstCancel := context.WithCancel(context.Background())
	defer firstCancel()
	firstCounting := &cancelAtErrCheck{Context: firstContext, cancel: firstCancel}
	if _, present, err := warm.nextOccurrence(firstCounting); err != nil || !present || warm.hashCandidates.position != 1 {
		warm.Close()
		t.Fatal("residual calibration did not consume one candidate", present, err)
	}
	firstPolls := firstCounting.checks.Load()
	warm.Close()
	consuming, err := openQueryNode(context.Background(), state, filter)
	if err != nil {
		t.Fatal(err)
	}
	candidates := consuming.hashCandidates
	if candidates == nil || candidates.root != root {
		t.Fatal("consumer fell back to scan")
	}
	consumeContext, consumeCancel := context.WithCancel(context.Background())
	defer consumeCancel()
	duringConsumption := &cancelAtErrCheck{Context: consumeContext, cancel: consumeCancel, at: firstPolls + 1}
	if err := consuming.drain(duringConsumption); !errors.Is(err, context.Canceled) || candidates.position != 1 || !errors.Is(consuming.terminal, context.Canceled) {
		consuming.Close()
		t.Fatal("residual cancellation exposed a successful finite result", err, candidates.position)
	}
	if err := consuming.drain(context.Background()); !errors.Is(err, context.Canceled) {
		consuming.Close()
		t.Fatal("fresh context revived a canceled stage", err)
	}
	consuming.Close()
	unchanged()
	fresh := hashFilterStage(t, query, source, nil)
	defer fresh.Close()
	if fresh.hashCandidates == nil || fresh.hashCandidates.root != root || len(fresh.hashCandidates.occurrences) != 3 {
		t.Fatal("fresh lookup did not reuse retained authority")
	}
	if err := fresh.drain(context.Background()); err != nil || fresh.count() != 3 || fresh.hashCandidates.position != 3 {
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
		if strings.HasSuffix(frame.Function, ".mergeHashIndexRoot") {
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
	index, old := hashRoot(t, e, "hn")
	parent := e.catalog.state.MustRelation(index.SQLIndex.Parent)
	store := e.storage.Relations[parent.Address()]
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
	writes, deletes := writer.transactions.writes, writer.transactions.deletes
	if len(writes[parent.Address()]) != 3 || deletes[parent.Address()] == nil {
		t.Fatal("fixture did not stage a genuine multirow change")
	}
	before := indexMaintenanceCapture(e)
	rows, buckets := hashCancellationRows(store.Versions), hashCancellationBuckets(old)
	generation, ends, frontier := store.Generation, store.Ends, e.commitSeq

	// Dry preparation uses the same real private batch and slot/end owner as
	// publication. Its final pre-return poll follows touched-bucket copying;
	// cancel at the immediately preceding bucket-entry poll in the real owner.
	p := rowPublication{address: parent.Address(), store: store, original: store, originalRelation: parent, relation: parent, root: store.Versions, writes: writes[parent.Address()], ends: store.Ends, deletion: deletes[parent.Address()], generation: store.Generation, singleSlot: -1}
	if err := e.prepareRowPublication(&p, ref, true); err != nil {
		t.Fatal(err)
	}
	calibration, stop := context.WithCancel(context.Background())
	defer stop()
	counting := &cancelAtErrCheck{Context: calibration, cancel: stop}
	candidate, err := mergeHashIndexRoot(&EvalContext{Context: counting, Scope: 1, Frame: 1}, parent, index, e.storage.IndexRoots[index.Address()], &p, false, true)
	if err != nil || candidate == nil || candidate.Hash == nil || !reflect.DeepEqual(hashKeys(t, candidate.Hash), []string{"3", "4", "4", "5"}) {
		t.Fatal("real merge calibration failed", candidate, err)
	}
	polls := counting.checks.Load()
	if polls <= int32(len(p.writes))+2 {
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
	op.ctx = during
	err = e.publishRows(op, ref, writes, deletes, nil, nil, nil)
	op.Done()
	if !errors.Is(err, context.Canceled) || during.checks.Load() != during.at {
		t.Fatal("owner did not cancel inside real hash merge", err, during.checks.Load(), during.at)
	}
	indexMaintenanceUnchanged(t, e, before)
	if store.Generation != generation || store.Ends != ends || e.commitSeq != frontier || !reflect.DeepEqual(store.Versions, rows) || !reflect.DeepEqual(old.Buckets, buckets) || !reflect.DeepEqual(hashKeys(t, old), []string{"1", "2", "2", "3"}) {
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
