package core_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

// Cancel at the actual fallible transport, not a scan callback or a fabricated
// completion seam. This exercises the overlay's entire ordering boundary.
type clusterTransportContext struct {
	context.Context
	boundary string
	entered bool
	onEnter func()
}

func (c *clusterTransportContext) Err() error {
	if err := c.Context.Err(); err!=nil { return err }
	if !c.entered {
		var pcs [64]uintptr
		frames := runtime.CallersFrames(pcs[:runtime.Callers(2,pcs[:])])
		for {
			frame,more := frames.Next()
			if strings.HasSuffix(frame.Function,c.boundary) {
				c.entered = true
				c.onEnter()
				break
			}
			if !more { break }
		}
	}
	return c.Context.Err()
}

func clusterProgressTransportBoundaries(t *testing.T) {
	for _,boundary := range []string{"index.BuildPositions","index.(*AlterIndexBuild).BaselineRoot","index.AttachIndexBuildOutputs"} {
		t.Run(boundary,func(t *testing.T){
			e := engine.New()
			worker := enginetest.StartSession(t,e,nil)
			enginetest.SessionSQL(t,worker,"CREATE TABLE progress_build(k INT)")
			enginetest.SessionSQL(t,worker,"INSERT INTO progress_build VALUES(1)")
			enginetest.SessionSQL(t,worker,"BEGIN")
			enginetest.SessionSQL(t,worker,"INSERT INTO progress_build VALUES(2),(3)")
			b := indexProgressStage(t,e,worker,"CREATE INDEX progress_new ON progress_build(k)")
			close(b.gate.release)
			e.Mu.Lock()
			p := b.scope.BeginClusterProgressLocked(b.parent,"CLUSTER")
			e.Mu.Unlock()
			defer e.EndClusterProgress(b.scope.TxRef,p)
			b.stage.Indexes().Progress = index.ProgressBinding{Observer:p,Targets:map[ids.ObjectAddress]ids.ObjectAddress{b.target:b.parent}}
			probe := &clusterTransportContext{Context:b.op.Context(),boundary:boundary}
			probe.onEnter = func(){
				facts := clusterProgressFacts(t,e,b.observer)
				if len(facts)!=1 || facts[0].Indexes!=0 || facts[0].Phase!="rebuilding index" { t.Fatal("premature successful build",facts) }
				b.op.Cancel(context.Canceled)
			}
			b.op.Ctx = probe
			prepared,err := e.ValidateCatalogStage(b.op.Context(),b.stage)
			if err==nil { _,err = e.PublishCatalogOperation(b.op,b.scope,prepared,core.PublicationToken{Epoch:e.CatalogRead().Epoch()}) }
			if !probe.entered || !errors.Is(err,context.Canceled) { t.Fatal("actual transport cancellation not reached",probe.entered,err) }
			facts := clusterProgressFacts(t,e,b.observer)
			if len(facts)!=1 || facts[0].Indexes!=0 { t.Fatal("failed transport claimed completion",facts) }
			_ = b.scope.Finish(err)
			b.op.Done()
			enginetest.SessionSQL(t,worker,"ROLLBACK")
		})
	}
}
