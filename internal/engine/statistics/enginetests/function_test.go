package statistics_test

import (
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine/statistics"
)

func statsProviderFunctionUsage(t *testing.T) {
	ms:=time.Millisecond
	for _,kind:=range []string{"same","distinct","mutual","prefix","failed_outer","reset","retire","peer_retire"} {
		t.Run(kind,func(t *testing.T){
			var p statistics.Provider
			var w,peer statistics.Writer
			f,g:=statsKeys();f.Category,g.Category=statistics.Function,statistics.Function
			begin:=func(k statistics.Key)statistics.FunctionUsage{u,ok:=w.BeginFunction(&p,k);if !ok{t.Fatal("live key rejected")};return u}
			if kind=="prefix" {u:=begin(f);w.EndFunction(&p,u,4*ms)}
			outer:=begin(f)
			innerKey:=f;if kind=="distinct"||kind=="mutual" {innerKey=g}
			inner:=begin(innerKey)
			if kind=="mutual" {u:=begin(f);w.EndFunction(&p,u,3*ms)}
			elapsed:=5*ms;if kind=="mutual" {elapsed=8*ms}
			w.EndFunction(&p,inner,elapsed)
			switch kind {
			case "reset":p.Reset(time.Now())
			case "retire":peer.Retire(&p,f)
			case "peer_retire":
				// Shared retirement/publication is safe across separately owned
				// writers, without pretending a session permits concurrent calls.
				var joined sync.WaitGroup;joined.Add(1)
				go func(){defer joined.Done();peer.Retire(&p,f)}();joined.Wait()
			}
			if kind!="failed_outer" {elapsed=10*ms;if kind=="mutual"{elapsed=12*ms};w.EndFunction(&p,outer,elapsed)}
			w.Flush(&p,time.Now(),true)
			wantCalls,wantTotal,wantSelf:=int64(2),10*ms,10*ms
			switch kind {
			case "prefix":wantCalls,wantTotal,wantSelf=3,14*ms,14*ms
			case "distinct":wantCalls,wantSelf=1,5*ms
			case "mutual":wantTotal,wantSelf=12*ms,7*ms
			case "failed_outer":wantCalls,wantTotal,wantSelf=1,5*ms,5*ms
			case "reset","retire","peer_retire":wantCalls,wantTotal,wantSelf=0,0,0
			}
			if got:=p.Read(f);got.Calls!=wantCalls||got.TotalTime!=wantTotal||got.SelfTime!=wantSelf{t.Fatal(kind,got,wantCalls,wantTotal,wantSelf)}
			if kind=="distinct"||kind=="mutual" {total,self:=5*ms,5*ms;if kind=="mutual"{total=8*ms};if got:=p.Read(g);got.Calls!=1||got.TotalTime!=total||got.SelfTime!=self{t.Fatal("distinct inclusive total",got)}}
			if kind=="retire"||kind=="peer_retire" {if _,ok:=w.BeginFunction(&p,f);ok {t.Fatal("retired key admitted")}}
		})
	}
}
