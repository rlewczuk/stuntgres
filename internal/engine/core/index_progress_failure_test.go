package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/sqltype"
)

type indexProgressPanicKey struct {
	expr.Info
	marker any
}

func (k indexProgressPanicKey) Eval(expr.Context) (sqltype.Value, error) { panic(k.marker) }

func indexProgressPanicCleanup(t *testing.T) {
	b := indexProgressPrepare(t, 1, false)
	parent := b.e.CatalogRead().MustRelation(b.parent)
	idx := b.stage.Get(b.target).(*catalog.Relation)
	def := *idx.SQLIndex
	def.Keys = append([]catalog.IndexKey(nil), def.Keys...)
	marker := &struct{}{}
	key := indexProgressPanicKey{Info: expr.Info{Usage: parent.Columns[0].Usage}, marker: marker}
	def.Keys[0].Derived = &catalog.DerivedIndexKey{Definition: key, Execution: key}
	b.e.Mu.Lock()
	store := *b.e.Storage.Relations[b.parent]
	b.e.Mu.Unlock()
	var recovered any
	p := core.ScopeIndexProgressEntry(b.scope)
	func() {
		defer func() { recovered = recover() }()
		defer b.scope.Finish(context.Canceled)
		_, _ = index.BuildIndexRoot(indexProgressEvaluation(b), parent, &def, &store, &index.BuildCapture{Progress: b.gate.BuildProgress})
	}()
	b.op.Done()
	if recovered != marker {
		t.Fatal("provider panic not propagated", recovered)
	}
	if facts := indexProgressFacts(t, b.e, b.observer); len(facts) != 0 {
		t.Fatal("panic retained work", facts)
	}
	f := core.IndexProgressSnapshot(p)
	if f.Phase != "building index: scanning table" || f.BlocksDone != 0 || f.TuplesTotal != 0 || f.TuplesDone != 0 {
		t.Fatal("panic fabricated completed work", f)
	}
	if _, ok := b.e.CatalogRead().Relation(b.target); ok {
		t.Fatal("panic published index")
	}
}

func indexProgressPublicationVeto(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		b := indexProgressPrepare(t, 129, explicit)
		close(b.gate.release)
		parent := b.e.CatalogRead().MustRelation(b.parent)
		idx := b.stage.Get(b.target).(*catalog.Relation)
		b.e.Mu.Lock()
		live := b.e.Storage.Relations[b.parent]
		snapshot := *live
		b.e.Mu.Unlock()
		root, err := index.BuildIndexRoot(indexProgressEvaluation(b), parent, idx.SQLIndex, &snapshot, &index.BuildCapture{Progress: b.gate.BuildProgress})
		if err != nil {
			t.Fatal(err)
		}
		b.stage.Indexes().Candidates = map[ids.ObjectAddress]index.RootCandidate{b.target: {Root: root, Index: idx, Storage: live, Generation: snapshot.Generation, Ends: snapshot.Ends, Length: len(snapshot.Versions)}}
		prepared, err := b.e.ValidateCatalogStage(b.op.Context(), b.stage)
		if err != nil {
			t.Fatal(err)
		}
		p := core.ScopeIndexProgressEntry(b.scope)
		before := core.IndexProgressSnapshot(p)
		if before.TuplesTotal != 129 || before.TuplesDone != 0 {
			t.Fatal(before)
		}
		b.op.Cancel(context.Canceled)
		_, err = b.e.PublishCatalogOperation(b.op, b.scope, prepared, core.PublicationToken{Epoch: b.e.CatalogRead().Epoch()})
		if err == nil {
			t.Fatal("cancelled publication installed candidate")
		}
		_ = b.scope.Finish(err)
		b.op.Done()
		if facts := indexProgressFacts(t, b.e, b.observer); len(facts) != 0 {
			t.Fatal(facts)
		}
		if _, ok := b.e.CatalogRead().Relation(b.target); ok {
			t.Fatal("publication veto installed index")
		}
		if core.IndexProgressSnapshot(p).TuplesDone != 0 {
			t.Fatal("veto fabricated loading")
		}
		if explicit {
			sessionSQL(t, b.worker, "ROLLBACK")
		}
	}
}

func indexProgressEarlyFinish(t *testing.T) {
	b := indexProgressPrepare(t, 1, false)
	failure := errors.New("early admitted CREATE failure")
	if err := b.scope.Finish(failure); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	b.op.Done()
	if facts := indexProgressFacts(t, b.e, b.observer); len(facts) != 0 {
		t.Fatal(facts)
	}
	if err := b.scope.Finish(nil); !errors.Is(err, failure) {
		t.Fatal("repeated Finish changed result", err)
	}
}
