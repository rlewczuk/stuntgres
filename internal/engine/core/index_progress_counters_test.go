package core_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/storage"
)

type indexProgressRecorder struct {
	index.BuildProgress
	observe func(string, int)
}

func (p indexProgressRecorder) ScanStart(n int, m catalog.IndexMethod) {
	p.BuildProgress.ScanStart(n, m)
	p.observe("scan", n)
}
func (p indexProgressRecorder) ScanDone(n int) { p.BuildProgress.ScanDone(n); p.observe("visited", n) }
func (p indexProgressRecorder) ScanFinish(n int, m catalog.IndexMethod) {
	p.BuildProgress.ScanFinish(n, m)
	p.observe("sort", n)
}

func indexProgressEvaluation(b *indexProgressBuild) *exec.Context {
	return &exec.Context{EvalStatement: exec.EvalStatement{Context: b.op.Context(), Catalog: b.e.StageRead(b.stage), Host: core.HostOf(b.scope), Input: b.scope.Input, Output: b.scope.Output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
}

func TestCreateIndexProgressPhaseCounters(t *testing.T) {
	t.Run("reindex_targets", indexProgressReindexTargets)
	for _, n := range []int{0, 1, 127, 128, 129} {
		for _, dead := range []bool{false, true} {
			t.Run(fmt.Sprintf("versions=%d/dead=%v", n, dead), func(t *testing.T) {
				b := indexProgressPrepare(t, n, false)
				parent := b.e.CatalogRead().MustRelation(b.parent)
				idx := b.stage.Get(b.target).(*catalog.Relation)
				b.e.Mu.Lock()
				store := *b.e.Storage.Relations[b.parent]
				b.e.Mu.Unlock()
				wantTuples := n
				if dead && n > 0 {
					last := store.Versions[n-1]
					store.Ends = &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{last.Row: {Version: last.Version, End: 1}}}
					wantTuples--
				}
				p := core.ScopeIndexProgressEntry(b.scope)
				initial := core.IndexProgressSnapshot(p)
				if initial.Phase != "initializing" || initial.BlocksTotal != 0 || initial.BlocksDone != 0 || initial.TuplesTotal != 0 || initial.TuplesDone != 0 {
					t.Fatal(initial)
				}
				visits, sorts := 0, 0
				recorder := indexProgressRecorder{BuildProgress: b.gate.BuildProgress, observe: func(kind string, visited int) {
					f := core.IndexProgressSnapshot(p)
					switch kind {
					case "scan":
						total := int64(n / 128)
						if n%128 != 0 {
							total++
						}
						if f.Phase != "building index: scanning table" || f.BlocksTotal != total || f.BlocksDone != 0 || f.TuplesTotal != 0 || f.TuplesDone != 0 {
							t.Fatal(f)
						}
					case "visited":
						visits++
						want := int64(visited / 128)
						if visited == n && n%128 != 0 {
							want++
						}
						if visited != visits || f.BlocksDone != want || f.TuplesTotal != 0 || f.TuplesDone != 0 {
							t.Fatal(visited, f)
						}
					case "sort":
						sorts++
						if visited != wantTuples || f.Phase != "building index: sorting live tuples" || f.TuplesTotal != int64(wantTuples) || f.TuplesDone != 0 || f.BlocksTotal != 0 || f.BlocksDone != 0 {
							t.Fatal(visited, f)
						}
					}
				}}
				root, err := index.BuildIndexRoot(indexProgressEvaluation(b), parent, idx.SQLIndex, &store, &index.BuildCapture{Progress: recorder})
				if err != nil || len(root.Entries) != wantTuples || visits != n || sorts != 1 {
					t.Fatal(root, err, visits, sorts)
				}
				// NULLs and duplicates are entries, not omitted work or loading claims.
				f := core.IndexProgressSnapshot(p)
				if f.TuplesTotal != int64(wantTuples) || f.TuplesDone != 0 {
					t.Fatal(f)
				}
			})
		}
	}
	t.Run("actual_key_failure", func(t *testing.T) {
		b := indexProgressPrepare(t, 129, false)
		parent := b.e.CatalogRead().MustRelation(b.parent)
		idx := b.stage.Get(b.target).(*catalog.Relation)
		def := *idx.SQLIndex
		def.Keys = append([]catalog.IndexKey(nil), def.Keys...)
		calls := 0
		failure := errors.New("real key provider failure")
		key := partialEvaluationWitness{Info: expr.Info{Usage: parent.Columns[0].Usage}, calls: &calls, failure: failure}
		def.Keys[0].Derived = &catalog.DerivedIndexKey{Definition: key, Execution: key}
		b.e.Mu.Lock()
		store := *b.e.Storage.Relations[b.parent]
		b.e.Mu.Unlock()
		p := core.ScopeIndexProgressEntry(b.scope)
		_, err := index.BuildIndexRoot(indexProgressEvaluation(b), parent, &def, &store, &index.BuildCapture{Progress: b.gate.BuildProgress})
		if !errors.Is(err, failure) || calls != 1 {
			t.Fatal(err, calls)
		}
		before := core.IndexProgressSnapshot(p)
		if before.BlocksDone != 0 || before.TuplesTotal != 0 {
			t.Fatal(before)
		}
		if err := b.scope.Finish(err); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		b.op.Done()
		if got := core.IndexProgressSnapshot(p); got != before {
			t.Fatal("error fabricated work", got, before)
		}
		if facts := indexProgressFacts(t, b.e, b.observer); len(facts) != 0 {
			t.Fatal(facts)
		}
	})
}

func TestCreateIndexProgressCaptureRetention(t *testing.T) {
	t.Run("cross_database", indexProgressCrossDatabase)
	b := indexProgressPrepare(t, 129, false)
	original := indexProgressFacts(t, b.e, b.observer)
	if len(original) != 1 {
		t.Fatal(original)
	}
	p := core.ScopeIndexProgressEntry(b.scope)
	var scanning, sorting []pgcatalog.CreateIndexProgressFact
	recorder := indexProgressRecorder{BuildProgress: b.gate.BuildProgress, observe: func(kind string, n int) {
		if kind == "scan" {
			scanning = indexProgressFacts(t, b.e, b.observer)
		}
		if kind == "sort" {
			sorting = indexProgressFacts(t, b.e, b.observer)
		}
	}}
	b.e.Mu.Lock()
	store := *b.e.Storage.Relations[b.parent]
	b.e.Mu.Unlock()
	parent := b.e.CatalogRead().MustRelation(b.parent)
	idx := b.stage.Get(b.target).(*catalog.Relation)
	if _, err := index.BuildIndexRoot(indexProgressEvaluation(b), parent, idx.SQLIndex, &store, &index.BuildCapture{Progress: recorder}); err != nil {
		t.Fatal(err)
	}
	if original[0].Phase != "initializing" || scanning[0].Phase != "building index: scanning table" || scanning[0].BlocksDone != 0 || scanning[0].BlocksTotal != 2 || sorting[0].TuplesTotal != 129 || sorting[0].BlocksTotal != 0 {
		t.Fatal(original, scanning, sorting)
	}
	_ = b.scope.Finish(context.Canceled)
	b.op.Done()
	if facts := indexProgressFacts(t, b.e, b.observer); len(facts) != 0 {
		t.Fatal(facts)
	}
	if original[0].Phase != "initializing" || scanning[0].BlocksDone != 0 {
		t.Fatal("borrowed snapshots")
	}
	core.ScopeEndIndexProgressEntry(b.scope, p)
	// A stale terminal owner cannot remove a subsequent command's entry.
	op := transactionOperation(t, b.worker)
	scope, err := b.e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	defer op.Done()
	defer scope.Finish(context.Canceled)
	b.e.Mu.Lock()
	scope.BeginCreateIndexProgressLocked(b.parent, b.target)
	b.e.Mu.Unlock()
	core.ScopeEndIndexProgressEntry(b.scope, p)
	if facts := indexProgressFacts(t, b.e, b.observer); len(facts) != 1 {
		t.Fatal("stale cleanup erased newer work", facts)
	}
}
