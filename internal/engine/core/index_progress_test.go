package core_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
)

func indexProgressFacts(t *testing.T, e *core.Engine, op *session.Operation) []pgcatalog.CreateIndexProgressFact {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	var facts []pgcatalog.CreateIndexProgressFact
	runtime := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth})
	if err := runtime.VisitCreateIndexProgress(op.Context(), func(f pgcatalog.CreateIndexProgressFact) error { facts = append(facts, f); return nil }); err != nil {
		t.Fatal(err)
	}
	return facts
}

type indexProgressStep struct {
	kind string
	n    int
	ack  chan struct{}
}
type indexProgressGate struct {
	index.BuildProgress
	steps   chan indexProgressStep
	release chan struct{}
}

func (g *indexProgressGate) step(kind string, n int) {
	step := indexProgressStep{kind: kind, n: n, ack: make(chan struct{})}
	select {
	case g.steps <- step:
	case <-g.release:
		return
	}
	select {
	case <-step.ack:
	case <-g.release:
	}
}
func (g *indexProgressGate) ScanStart(n int, m catalog.IndexMethod) {
	g.BuildProgress.ScanStart(n, m)
	g.step("scan", n)
}
func (g *indexProgressGate) ScanDone(n int) { g.BuildProgress.ScanDone(n); g.step("visited", n) }
func (g *indexProgressGate) ScanFinish(n int, m catalog.IndexMethod) {
	g.BuildProgress.ScanFinish(n, m)
	g.step("sort", n)
}

type indexProgressBuild struct {
	e              *core.Engine
	worker         *session.Session
	op, observer   *session.Operation
	scope          *core.StatementScope
	stage          *core.CatalogStage
	gate           *indexProgressGate
	parent, target ids.ObjectAddress
}

// This fixture uses the command's admitted PrepareCreate/StageCreate and real
// publication paths. Only the observer is wrapped, never the work or counters.
func indexProgressPrepare(t *testing.T, n int, explicit bool) *indexProgressBuild {
	t.Helper()
	e := engine.New()
	worker := sessionStart(t, e, nil)
	return indexProgressPrepareFor(t, e, worker, n, explicit)
}

func indexProgressPrepareFor(t *testing.T, e *core.Engine, worker *session.Session, n int, explicit bool) *indexProgressBuild {
	t.Helper()
	return indexProgressPrepareSQL(t, e, worker, n, explicit, "CREATE INDEX progress_new ON progress_build(k)")
}

func indexProgressPrepareSQL(t *testing.T, e *core.Engine, worker *session.Session, n int, explicit bool, createSQL string) *indexProgressBuild {
	t.Helper()
	sessionSQL(t, worker, "CREATE TABLE progress_build(k INT)")
	if n > 0 {
		var q strings.Builder
		q.WriteString("INSERT INTO progress_build VALUES")
		for i := range n {
			if i > 0 {
				q.WriteByte(',')
			}
			if i%3 == 0 {
				q.WriteString("(NULL)")
			} else {
				fmt.Fprintf(&q, "(%d)", i%7)
			}
		}
		sessionSQL(t, worker, q.String())
	}
	if explicit {
		sessionSQL(t, worker, "BEGIN")
	}
	return indexProgressStage(t, e, worker, createSQL)
}

func indexProgressStage(t *testing.T, e *core.Engine, worker *session.Session, createSQL string) *indexProgressBuild {
	t.Helper()
	op := transactionOperation(t, worker)
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	var stage *core.CatalogStage
	setupComplete := false
	defer func() {
		if !setupComplete {
			_ = scope.Finish(context.Canceled)
			op.Done()
			if stage != nil {
				e.DiscardCatalogStage(stage)
			}
		}
	}()
	syntax := enginetest.Statement(t, createSQL).(*parser.CreateIndex)
	if err := scope.Gate(syntax); err != nil {
		t.Fatal(err)
	}
	// Gate establishes transaction ownership, not the relation target. Match
	// Execute's real relation admission before reading LockedRef or staging DDL.
	if err := scope.AcquireRelation(syntax, true); err != nil {
		t.Fatal(err)
	}
	var parentAddress, targetAddress ids.ObjectAddress
	var gate *indexProgressGate
	func() {
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, err := e.StatementAuthorizationLocked(op, syntax)
		if err != nil {
			t.Fatal(err)
		}
		if err := scope.CaptureIdentityLocked(auth); err != nil {
			t.Fatal(err)
		}
		parent := auth.Catalog.MustRelation(scope.LockedRef.Address)
		stage = e.BeginCatalogStage(auth.Catalog)
		stage.AuthorizeLocked(auth, op.Session)
		stage.Indexes().Output = scope.Output
		host := tablecmds.TableHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}
		created, err := index.PrepareCreate(host, auth, parent, syntax, scope.Input, nil)
		if err == nil {
			err = index.StageCreate(host, parent, created.Relation, syntax.Only)
		}
		if err != nil {
			t.Fatal(err)
		}
		parentAddress, targetAddress = parent.Address(), created.Relation.Address()
		binding := scope.BeginCreateIndexProgressLocked(parentAddress, targetAddress)
		if parent.Partition != nil {
			binding.PartitionTree(host.StageRead(), host.StageBase(), targetAddress)
		}
		gate = &indexProgressGate{BuildProgress: binding.Observer, steps: make(chan indexProgressStep), release: make(chan struct{})}
		binding.Observer = gate
		stage.Indexes().Progress = binding
	}()
	observer := transactionOperation(t, sessionStart(t, e, nil))
	build := &indexProgressBuild{e: e, worker: worker, op: op, observer: observer, scope: scope, stage: stage, gate: gate, parent: parentAddress, target: targetAddress}
	t.Cleanup(func() {
		if !scope.Finished {
			_ = scope.Finish(context.Canceled)
			op.Done()
		}
		observer.Done()
		e.DiscardCatalogStage(stage)
	})
	setupComplete = true
	return build
}

func (b *indexProgressBuild) run() <-chan error {
	done := make(chan error, 1)
	go func() {
		prepared, err := b.e.ValidateCatalogStage(b.op.Context(), b.stage)
		if err == nil {
			_, err = b.e.PublishCatalogOperation(b.op, b.scope, prepared, core.PublicationToken{Epoch: b.e.CatalogRead().Epoch()})
		}
		if err == nil {
			b.scope.CompleteUtility()
		}
		err = b.scope.Finish(err)
		b.op.Done()
		done <- err
	}()
	return done
}

func TestCreateIndexProgressBuildCallbacks(t *testing.T) {
	t.Run("initial_admission", indexProgressAdmission)
	t.Run("methods", indexProgressMethodCallbacks)
	t.Run("generic_methods", indexProgressGenericMethods)
	t.Run("partitions", indexProgressPartitionCallbacks)
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			b := indexProgressPrepare(t, 129, explicit)
			done := b.run()
			joined := false
			defer func() {
				close(b.gate.release)
				if !joined {
					b.op.Cancel(context.Canceled)
					<-done
				}
			}()
			scans, sorts := 0, 0
			for {
				select {
				case step := <-b.gate.steps:
					facts := indexProgressFacts(t, b.e, b.observer)
					if len(facts) != 1 {
						t.Fatal(facts)
					}
					f := facts[0]
					if f.PID != int64(b.op.Session.Token().ID()) || f.Relation != b.parent.ObjectID || f.Database != b.e.Catalog.DatabaseAddress().ObjectID || f.Index != 0 || !f.DatabaseExists || !f.Visible || f.Command != "CREATE INDEX" {
						t.Fatalf("actual identity: %+v", f)
					}
					if f.TuplesDone != 0 || f.LockersTotal != 0 || f.LockersDone != 0 || f.CurrentLockerPID != 0 || f.PartitionsTotal != 0 || f.PartitionsDone != 0 {
						t.Fatal(f)
					}
					switch step.kind {
					case "scan":
						scans++
						if core.IndexProgressAttempt(core.ScopeIndexProgressEntry(b.scope)) != uint64(scans) {
							t.Fatal("repeated build did not start a distinct attempt")
						}
						if step.n != 129 || f.Phase != "building index: scanning table" || f.BlocksTotal != 2 || f.BlocksDone != 0 || f.TuplesTotal != 0 {
							t.Fatal(step, f)
						}
					case "visited":
						want := int64(step.n / 128)
						if step.n == 129 {
							want = 2
						}
						if f.BlocksDone != want || f.TuplesTotal != 0 {
							t.Fatal(step, f)
						}
					case "sort":
						sorts++
						if step.n != 129 || f.Phase != "building index: sorting live tuples" || f.TuplesTotal != 129 || f.BlocksTotal != 0 || f.BlocksDone != 0 {
							t.Fatal(step, f)
						}
					}
					close(step.ack)
				case err := <-done:
					joined = true
					if err != nil {
						t.Fatal(err)
					}
					if scans == 0 || sorts == 0 {
						t.Fatalf("actual build callbacks scan=%d sort=%d", scans, sorts)
					}
					if facts := indexProgressFacts(t, b.e, b.observer); len(facts) != 0 {
						t.Fatal("completed row retained", facts)
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

func TestCreateIndexProgressLifetime(t *testing.T) {
	t.Run("panic", indexProgressPanicCleanup)
	t.Run("early_finish", indexProgressEarlyFinish)
	t.Run("publication_veto", indexProgressPublicationVeto)
	for _, phase := range []string{"visited", "sort"} {
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%v", phase, explicit), func(t *testing.T) {
				b := indexProgressPrepare(t, 129, explicit)
				p := core.ScopeIndexProgressEntry(b.scope)
				done := b.run()
				joined := false
				defer func() {
					close(b.gate.release)
					if !joined {
						b.op.Cancel(context.Canceled)
						<-done
					}
				}()
				cancelled := false
				var before pgcatalog.CreateIndexProgressFact
				for {
					select {
					case step := <-b.gate.steps:
						if !cancelled && step.kind == phase && (phase != "visited" || step.n == 128) {
							before = core.IndexProgressSnapshot(p)
							b.op.Cancel(context.Canceled)
							cancelled = true
						}
						close(step.ack)
					case err := <-done:
						joined = true
						if !cancelled || !errors.Is(err, context.Canceled) {
							t.Fatalf("cancel=%v err=%v", cancelled, err)
						}
						after := core.IndexProgressSnapshot(p)
						if before != after {
							t.Fatalf("cancellation fabricated completion: before=%+v after=%+v", before, after)
						}
						if facts := indexProgressFacts(t, b.e, b.observer); len(facts) != 0 {
							t.Fatal(facts)
						}
						if _, ok := b.e.CatalogRead().Relation(b.target); ok {
							t.Fatal("cancel published index")
						}
						if explicit {
							sessionSQL(t, b.worker, "ROLLBACK")
						}
						return
					}
				}
			})
		}
	}
}
