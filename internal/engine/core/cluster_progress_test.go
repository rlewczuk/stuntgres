package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
)

func clusterProgressFacts(t *testing.T,e *core.Engine,op *session.Operation) []pgcatalog.ClusterProgressFact {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth,err := e.AuthorizationLocked(op)
	if err!=nil { t.Fatal(err) }
	var facts []pgcatalog.ClusterProgressFact
	r := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine:e,Auth:auth})
	if err := r.VisitClusterProgress(op.Context(),func(f pgcatalog.ClusterProgressFact) error { facts = append(facts,f); return nil }); err!=nil { t.Fatal(err) }
	return facts
}

func TestClusterProgressOwnership(t *testing.T) {
	t.Run("replacement",clusterProgressReplacement)
	t.Run("concurrent_workers",clusterProgressConcurrentWorkers)
	t.Run("admitted_targets",clusterProgressAdmittedTargets)
	b := indexProgressPrepare(t,3,false)
	b.e.Mu.Lock()
	old := b.scope.BeginClusterProgressLocked(b.parent,"CLUSTER")
	b.e.Mu.Unlock()
	defer b.e.EndClusterProgress(b.scope.TxRef,old)
	original := clusterProgressFacts(t,b.e,b.observer)
	if len(original)!=1 || original[0].Command!="CLUSTER" || original[0].Phase!="initializing" || original[0].Relation!=b.parent.ObjectID { t.Fatal(original) }
	binding := index.ProgressBinding{Observer:old,Targets:map[ids.ObjectAddress]ids.ObjectAddress{b.target:b.parent}}
	// Callback lock order is entry-only, even when publication owns Engine.Mu.
	b.e.Mu.Lock()
	observer := binding.ObserverFor(b.target)
	observer.ScanStart(3,catalog.BtreeMethod)
	observer.ScanDone(3)
	observer.ScanFinish(3,catalog.BtreeMethod)
	binding.Complete(b.target)
	binding.Complete(b.target) // Idempotent completion for this exact build.
	binding.Complete(b.parent) // An unselected target cannot contribute.
	b.e.Mu.Unlock()
	current := clusterProgressFacts(t,b.e,b.observer)
	if len(current)!=1 || current[0].Indexes!=1 || current[0].Phase!="rebuilding index" || current[0].Scanned!=0 || current[0].Written!=0 || current[0].HeapBlocksTotal!=0 || current[0].HeapBlocksScanned!=0 || current[0].ClusterIndex!=0 { t.Fatal(current) }
	if original[0].Phase!="initializing" || original[0].Indexes!=0 { t.Fatal("borrowed fact backing",original) }
	// Per-target turnover retains neither counts nor the old entry's lifetime.
	b.e.Mu.Lock()
	next := b.scope.BeginClusterProgressLocked(b.target,"CLUSTER")
	b.e.Mu.Unlock()
	defer b.e.EndClusterProgress(b.scope.TxRef,next)
	if next==old { t.Fatal("target owner reused") }
	b.e.EndClusterProgress(b.scope.TxRef,old)
	binding.Complete(b.target)
	current = clusterProgressFacts(t,b.e,b.observer)
	if len(current)!=1 || current[0].Relation!=b.target.ObjectID || current[0].Indexes!=0 { t.Fatal("stale cleanup/callback reached replacement",current) }
	b.e.EndClusterProgress(b.scope.TxRef,next)
	next.IndexStart(b.parent,b.target)
	next.IndexComplete(b.target)
	b.e.EndClusterProgress(b.scope.TxRef,next)
	if facts := clusterProgressFacts(t,b.e,b.observer); len(facts)!=0 { t.Fatal("detached callback reinserted",facts) }
}

func TestClusterProgressCompletedBuilds(t *testing.T) {
	t.Run("transport_boundaries",clusterProgressTransportBoundaries)
	for _,cancel := range []bool{false,true} {
		t.Run(map[bool]string{false:"success",true:"canceled"}[cancel],func(t *testing.T){
			b := indexProgressPrepare(t,3,false)
			close(b.gate.release)
			b.e.Mu.Lock()
			p := b.scope.BeginClusterProgressLocked(b.parent,"CLUSTER")
			// Build two real roots through the ordinary overlay, establishing the
			// final completed count before target removal, without a timer.
			syntax := enginetest.Statement(t,"CREATE INDEX progress_second ON progress_build(k)").(*parser.CreateIndex)
			auth,authErr := b.e.AuthorizationLocked(b.op)
			if authErr!=nil { b.e.Mu.Unlock(); t.Fatal(authErr) }
			host := tablecmds.TableHost{DDLHost:core.DDLHost{E:b.e,Op:b.op,CatalogStage:b.stage}}
			parent := auth.Catalog.MustRelation(b.parent)
			created,buildErr := index.PrepareCreate(host,auth,parent,syntax,b.scope.Input,nil)
			if buildErr==nil { buildErr = index.StageCreate(host,auth,parent,created.Relation,syntax.Only) }
			if buildErr!=nil { b.e.Mu.Unlock(); t.Fatal(buildErr) }
			b.e.Mu.Unlock()
			defer b.e.EndClusterProgress(b.scope.TxRef,p)
			b.stage.Indexes().Progress = index.ProgressBinding{Observer:p,Targets:map[ids.ObjectAddress]ids.ObjectAddress{b.target:b.parent,created.Relation.Address():b.parent}}
			if cancel { b.op.Cancel(context.Canceled) }
			prepared,err := b.e.ValidateCatalogStage(b.op.Context(),b.stage)
			if err==nil { _,err = b.e.PublishCatalogOperation(b.op,b.scope,prepared,core.PublicationToken{Epoch:b.e.CatalogRead().Epoch()}) }
			if (err!=nil)!=cancel { t.Fatal(err) }
			facts := clusterProgressFacts(t,b.e,b.observer)
			want := int64(2)
			if cancel { want=0 }
			if len(facts)!=1 || facts[0].Indexes!=want || facts[0].Scanned!=0 || facts[0].Written!=0 { t.Fatal("successful actual build accounting",facts) }
			b.e.EndClusterProgress(b.scope.TxRef,p)
			_ = b.scope.Finish(err)
			b.op.Done()
		})
	}
}
