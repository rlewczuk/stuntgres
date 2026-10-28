package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

func indexProgressMethodCallbacks(t *testing.T) {
	for _, tc := range []struct {
		name, sql  string
		tupleTotal int64
		sort       bool
	}{
		{"hash", "CREATE INDEX progress_new ON progress_build USING hash(k)", 86, true},
		{"brin", "CREATE INDEX progress_new ON progress_build USING brin(k)", 0, false},
		{"expression", "CREATE INDEX progress_new ON progress_build((k+1))", 129, true},
		{"partial", "CREATE INDEX progress_new ON progress_build(k) WHERE k IS NOT NULL", 86, true},
	} {
		for _, explicit := range []bool{false, true} {
			name := tc.name + "/publication"
			if explicit {
				name = tc.name + "/overlay"
			}
			t.Run(name, func(t *testing.T) {
				e := engine.New()
				b := indexProgressPrepareSQL(t, e, sessionStart(t, e, nil), 129, explicit, tc.sql)
				done := b.run()
				joined := false
				defer func() {
					close(b.gate.release)
					if !joined {
						b.op.Cancel(context.Canceled)
						<-done
					}
				}()
				scans, sorts, visits := 0, 0, 0
				for {
					select {
					case step := <-b.gate.steps:
						facts := indexProgressFacts(t, e, b.observer)
						if len(facts) != 1 {
							t.Fatal(facts)
						}
						f := facts[0]
						if f.Command != "CREATE INDEX" || f.Index != 0 || f.Relation != b.parent.ObjectID || f.TuplesDone != 0 {
							t.Fatal(f)
						}
						generic := tc.name == "hash" || tc.name == "brin"
						if generic && f.Phase != "building index" {
							t.Fatal(f)
						}
						switch step.kind {
						case "scan":
							scans++
							visits = 0
							if f.BlocksTotal != 2 || f.BlocksDone != 0 || f.TuplesTotal != 0 {
								t.Fatal(f)
							}
						case "visited":
							visits++
							if visits != step.n || f.TuplesTotal != 0 {
								t.Fatal(step, f)
							}
						case "sort":
							sorts++
							if step.n != int(tc.tupleTotal) || f.TuplesTotal != tc.tupleTotal {
								t.Fatal(step, f)
							}
							if tc.name == "hash" && (f.BlocksTotal != 2 || f.BlocksDone != 2) {
								t.Fatal("hash scan blocks lost", f)
							}
						}
						close(step.ack)
					case err := <-done:
						joined = true
						if err != nil {
							t.Fatal(err)
						}
						if scans == 0 || visits != 129 || tc.sort && sorts == 0 || !tc.sort && sorts != 0 {
							t.Fatal(scans, sorts, visits)
						}
						if explicit {
							sessionSQL(t, b.worker, "COMMIT")
						}
						return
					}
				}
			})
		}
	}
}

func indexProgressCrossDatabase(t *testing.T) {
	e := engine.New()
	admin := sessionStart(t, e, nil)
	sessionSQL(t, admin, "CREATE DATABASE progress_other_database")
	other, err := e.SelectDatabase(context.Background(), "progress_other_database")
	if err != nil {
		t.Fatal(err)
	}
	first := indexProgressPrepareFor(t, e, admin, 1, false)
	second := indexProgressPrepareFor(t, other, sessionStart(t, other, nil), 1, false)
	identities := make(map[int64]ids.OID)
	for _, b := range []*indexProgressBuild{first, second} {
		parent := b.e.CatalogRead().MustRelation(b.parent)
		idx := b.stage.Get(b.target).(*catalog.Relation)
		b.e.Mu.Lock()
		store := *b.e.Storage.Relations[b.parent]
		b.e.Mu.Unlock()
		if _, err := index.BuildIndexRoot(indexProgressEvaluation(b), parent, idx.SQLIndex, &store, &index.BuildCapture{Progress: b.gate.BuildProgress}); err != nil {
			t.Fatal(err)
		}
		identities[int64(b.worker.Token().ID())] = b.e.Catalog.DatabaseAddress().ObjectID
	}
	facts := indexProgressFacts(t, e, first.observer)
	if len(facts) != 2 {
		t.Fatal(facts)
	}
	for _, f := range facts {
		if identities[f.PID] != f.Database || !f.DatabaseExists || f.TuplesTotal != 1 || f.Phase != "building index: sorting live tuples" {
			t.Fatal("cross-database identity/capture", f)
		}
		delete(identities, f.PID)
	}
	if len(identities) != 0 {
		t.Fatal("missing unordered worker", identities)
	}
}

// Test the per-target observer reset with an existing installed index and
// repeated real root construction. Public tests exercise the REINDEX dispatcher.
func indexProgressReindexTargets(t *testing.T) {
	b := indexProgressPrepare(t, 1, false)
	close(b.gate.release)
	if err := <-b.run(); err != nil {
		t.Fatal(err)
	}
	op := transactionOperation(t, b.worker)
	scope, err := b.e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	defer op.Done()
	defer scope.Finish(context.Canceled)
	b.e.Mu.Lock()
	binding := scope.BeginCreateIndexProgressLocked(b.parent, b.target)
	b.e.Mu.Unlock()
	p := core.ScopeIndexProgressEntry(scope)
	scope.IndexProgressCommand("REINDEX", b.target.ObjectID)
	b.scope, b.op = scope, op
	binding.Targets = map[ids.ObjectAddress]ids.ObjectAddress{b.target: b.parent}
	parent := b.e.CatalogRead().MustRelation(b.parent)
	idx := b.stage.Get(b.target).(*catalog.Relation)
	b.e.Mu.Lock()
	store := *b.e.Storage.Relations[b.parent]
	b.e.Mu.Unlock()
	for range 2 {
		observer := binding.ObserverFor(b.target)
		f := core.IndexProgressSnapshot(p)
		if f.Command != "REINDEX" || f.Index != b.target.ObjectID || f.Relation != b.parent.ObjectID || f.Phase != "initializing" || f.TuplesTotal != 0 || f.BlocksTotal != 0 || f.PartitionsTotal != 0 {
			t.Fatal(f)
		}
		if _, err := index.BuildIndexRoot(indexProgressEvaluation(b), parent, idx.SQLIndex, &store, &index.BuildCapture{Progress: observer}); err != nil {
			t.Fatal(err)
		}
		f = core.IndexProgressSnapshot(p)
		if f.TuplesTotal != 1 || f.TuplesDone != 0 {
			t.Fatal(f)
		}
	}
}
