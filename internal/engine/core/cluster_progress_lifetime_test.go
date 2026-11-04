package core_test

import (
	"bytes"
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

func clusterProgressReplacement(t *testing.T) {
	for _,restore := range []bool{false,true} {
		t.Run(map[bool]string{false:"reset",true:"restore"}[restore],func(t *testing.T){
			b := indexProgressPrepare(t,3,false)
			var dump bytes.Buffer
			if err := engine.Dump(context.Background(),b.e,&dump); err!=nil { t.Fatal(err) }
			b.e.Mu.Lock()
			old := b.scope.BeginClusterProgressLocked(b.parent,"CLUSTER")
			b.e.Mu.Unlock()
			defer b.e.EndClusterProgress(b.scope.TxRef,old)
			retained := clusterProgressFacts(t,b.e,b.observer)
			var err error
			if restore { err = engine.Restore(context.Background(),b.e,bytes.NewReader(dump.Bytes())) } else { err = engine.Reset(context.Background(),b.e) }
			if err!=nil { t.Fatal(err) }
			observer := enginetest.TransactionOperation(t,enginetest.StartSession(t,b.e,nil))
			defer observer.Done()
			old.IndexStart(b.parent,b.target)
			old.IndexComplete(b.target)
			if facts := clusterProgressFacts(t,b.e,observer); len(facts)!=0 { t.Fatal("catalog replacement retained old work",facts) }
			worker := enginetest.StartSession(t,b.e,nil)
			enginetest.SessionSQL(t,worker,"CREATE TABLE cp_replacement(id int)")
			ref := enginetest.CatalogRef(t,b.e,b.e.CatalogRead(),"cp_replacement")
			op := enginetest.TransactionOperation(t,worker)
			defer op.Done()
			scope,err := b.e.StartStatement(op)
			if err!=nil { t.Fatal(err) }
			defer scope.Finish(context.Canceled)
			if err := scope.Gate(enginetest.Statement(t,"CLUSTER")); err!=nil { t.Fatal(err) }
			b.e.Mu.Lock()
			next := scope.BeginClusterProgressLocked(ref.Address,"CLUSTER")
			b.e.Mu.Unlock()
			defer b.e.EndClusterProgress(scope.TxRef,next)
			b.e.EndClusterProgress(b.scope.TxRef,old)
			facts := clusterProgressFacts(t,b.e,observer)
			if len(facts)!=1 || facts[0].PID!=int64(worker.SessionToken.ID()) || facts[0].Indexes!=0 { t.Fatal("stale owner affected new generation",facts) }
			if len(retained)!=1 || retained[0].Phase!="initializing" || retained[0].Indexes!=0 { t.Fatal("replacement mutated detached backing",retained) }
		})
	}
}

type clusterProgressHold struct {
	*core.ClusterProgress
	entered chan struct{}
	release <-chan struct{}
}

func (p *clusterProgressHold) ScanStart(n int,m catalog.IndexMethod) {
	p.ClusterProgress.ScanStart(n,m)
	close(p.entered)
	<-p.release
}

func clusterProgressConcurrentWorkers(t *testing.T) {
	b := indexProgressPrepare(t,3,false)
	second := indexProgressStage(t,b.e,enginetest.StartSession(t,b.e,nil),"CREATE INDEX progress_other ON progress_build(k)")
	release := make(chan struct{})
	released := false
	done := make(chan error,2)
	ctx,stop := context.WithTimeout(t.Context(),enginetest.Liveness)
	defer stop()
	started,joined := 0,0
	defer func(){
		if !released { close(release) }
		for joined<started { <-done; joined++ }
	}()
	for _,build := range []*indexProgressBuild{b,second} {
		b.e.Mu.Lock()
		p := build.scope.BeginClusterProgressLocked(build.parent,"CLUSTER")
		store := *b.e.Storage.Relations[build.parent]
		b.e.Mu.Unlock()
		defer b.e.EndClusterProgress(build.scope.TxRef,p)
		hold := &clusterProgressHold{ClusterProgress:p,entered:make(chan struct{}),release:release}
		binding := index.ProgressBinding{Observer:hold,Targets:map[ids.ObjectAddress]ids.ObjectAddress{build.target:build.parent}}
		parent := b.e.CatalogRead().MustRelation(build.parent)
		idx := build.stage.Get(build.target).(*catalog.Relation)
		started++
		go func(){
			_,err := index.BuildRelationIndexRoot(indexProgressEvaluation(build),parent,idx,idx.SQLIndex.Method,&store,binding.Capture(build.target))
			if err==nil { binding.Complete(build.target) }
			done <- err
		}()
		select {
		case <-hold.entered:
		case <-ctx.Done(): t.Fatal(ctx.Err())
		}
	}
	facts := clusterProgressFacts(t,b.e,b.observer)
	if len(facts)!=2 { t.Fatal("independent worker membership",facts) }
	seen := map[int64]bool{}
	for _,f := range facts {
		if seen[f.PID] || f.Command!="CLUSTER" || f.Phase!="rebuilding index" || f.Indexes!=0 || f.Scanned!=0 { t.Fatal("incoherent concurrent capture",facts) }
		seen[f.PID] = true
	}
	close(release)
	released = true
	// Capture concurrently with both real completion callbacks, before joining:
	// the entry mutex, not a channel join, must make this cut coherent.
	during := clusterProgressFacts(t,b.e,b.observer)
	if len(during)!=2 { t.Fatal(during) }
	for _,f := range during {
		if f.Phase!="rebuilding index" || f.Indexes<0 || f.Indexes>1 || f.Scanned!=0 || f.Written!=0 { t.Fatal("concurrent completed cut",during) }
	}
	for joined<started {
		select {
		case err := <-done:
			joined++
			if err!=nil { t.Fatal(err) }
		case <-ctx.Done(): t.Fatal(ctx.Err())
		}
	}
	facts = clusterProgressFacts(t,b.e,b.observer)
	if len(facts)!=2 { t.Fatal(facts) }
	for _,f := range facts { if f.Indexes!=1 { t.Fatal("independent completion",facts) } }
}
