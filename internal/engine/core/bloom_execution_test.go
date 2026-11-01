package core_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
)

func TestBloomCollisionRecheck(t *testing.T) {
	e:=engine.New()
	enginetest.Run(t,e,"CREATE EXTENSION bloom")
	enginetest.Run(t,e,"CREATE TABLE bloom_collision(i integer)")
	var values []string
	for i:=range 17 { values=append(values,fmt.Sprintf("(%d)",i)) }
	enginetest.Run(t,e,"INSERT INTO bloom_collision VALUES"+strings.Join(values,","))
	enginetest.Run(t,e,"CREATE INDEX bloom_collision_idx ON bloom_collision USING bloom(i) WITH(length=1,col1=1)")
	_,built:=bloomRoot(t,e,"bloom_collision_idx")
	seen:=map[uint16]int{}
	target,collision:=-1,-1
	for i,signed:=range built.Bloom.Entries {
		if len(signed.Signature)!=1 || signed.Signature[0]==0 { t.Fatal("not a real one-bit signature",signed) }
		if prior,ok:=seen[signed.Signature[0]];ok { target,collision=prior,i;break }
		seen[signed.Signature[0]]=i
	}
	if target<0 { t.Fatal("17 real values did not collide in 16 bits") }
	text,err:=built.Entries[target].Keys[0].Type().Output(context.Background(),built.Entries[target].Keys[0]);if err!=nil{t.Fatal(err)}
	enginetest.Run(t,e,"INSERT INTO bloom_collision VALUES("+text+"),(NULL)")
	_,root:=bloomRoot(t,e,"bloom_collision_idx")
	query,source,release:=bloomCapture(t,e,"SELECT i FROM bloom_collision WHERE i="+text)
	defer release()
	stage:=hashFilterStage(t,query,source,nil)
	defer stage.Close()
	selection:=stage.HashCandidates
	if source.ScanDecision.Method!=plan.BloomCandidates || selection==nil || selection.Bloom!=root.Bloom || selection.Root!=nil || len(selection.Occurrences)<3 { t.Fatal("real lossy candidate path was not admitted",source.ScanDecision,selection) }
	falseCandidate:=false
	for _,ordinal:=range selection.Occurrences {
		row:=source.Committed[source.RawIndex(ordinal)]
		if row.Row==built.Entries[collision].Row && row.Version==built.Entries[collision].Version { falseCandidate=true }
	}
	if !falseCandidate {t.Fatal("production collision erased before original predicate")}
	if err:=stage.Drain(context.Background());err!=nil || stage.Count()!=2 || selection.Position!=len(selection.Occurrences){t.Fatal("collision was not consumed and rechecked with duplicate preservation",stage.Count(),selection.Position,err)}
}

// Scope interruption to work in the actual owner, using existing context Err
// boundaries. No sleeps, fake signatures, provider mocks or production hooks.
type bloomPhaseContext struct {
	*cancelAtErrCheck
	phase string
}
func (c *bloomPhaseContext) Err()error{
	var pcs [64]uintptr
	frames:=runtime.CallersFrames(pcs[:runtime.Callers(2,pcs[:])])
	for {
		frame,more:=frames.Next()
		if strings.HasSuffix(frame.Function,"."+c.phase){return c.cancelAtErrCheck.Err()}
		if !more{break}
	}
	return c.Context.Err()
}

func TestBloomCancellation(t *testing.T){
	e:=bloomSetup(t)
	enginetest.Run(t,e,"ALTER INDEX bloom_rows_idx SET(col1=4095)")
	d,root:=bloomRoot(t,e,"bloom_rows_idx")
	parent:=e.CatalogRead().MustRelation(d.SQLIndex.Parent)
	store:=e.Storage.Relations[parent.Address()]
	before,rows:=bloomSignatures(root),hashCancellationRows(store.Versions)
	generation,ends,frontier:=store.Generation,store.Ends,e.Visibility.CommitSeq()
	unchanged:=func(){
		if e.Storage.IndexRoots[d.Address()]!=root || store.Generation!=generation || store.Ends!=ends || e.Visibility.CommitSeq()!=frontier || !reflect.DeepEqual(rows,store.Versions) || !reflect.DeepEqual(before,bloomSignatures(root)){t.Fatal("cancellation changed source/root/publication authority")}
	}
	base,cancel:=context.WithCancel(context.Background())
	during:=&bloomPhaseContext{cancelAtErrCheck:&cancelAtErrCheck{Context:base,cancel:cancel,at:5},phase:"AddBloomValue"}
	built,err:=index.BuildBloomIndexRoot(&exec.Context{EvalStatement:exec.EvalStatement{Context:during,Catalog:e.CatalogRead()},EvalFrame:exec.EvalFrame{Scope:1,Frame:1}},parent,d,store,true,nil)
	cancel()
	if built!=nil || !errors.Is(err,context.Canceled) || during.checks.Load()!=5{t.Fatal("did not cancel actual signature draws",built,err,during.checks.Load())}
	unchanged()
	query,source,release:=bloomCapture(t,e,"SELECT i,s FROM bloom_rows WHERE i=7")
	filter:=exec.HashQueryFilter(query.Root)
	exec.AdmitScan(query.Root,source)
	state:=&exec.State{Source:source,HashFilter:filter,Frame:query.Root.(*plan.ProjectNode).Frame}
	if scope:=core.ScopeOf(source.LiveHost());scope!=nil{state.Input,state.Output=scope.Input,scope.Output}
	calibration,stop:=context.WithCancel(context.Background())
	counting:=&bloomPhaseContext{cancelAtErrCheck:&cancelAtErrCheck{Context:calibration,cancel:stop},phase:"bloomFilterCandidates"}
	opened,err:=exec.OpenNode(counting,state,filter)
	if err!=nil || opened.HashCandidates==nil || opened.HashCandidates.Bloom!=root.Bloom{t.Fatal("probe calibration did not use real root",err)}
	polls:=counting.checks.Load();opened.Close();stop()
	if polls<10{t.Fatal("no signature/probe work",polls)}
	probeContext,probeCancel:=context.WithCancel(context.Background())
	probe:=&bloomPhaseContext{cancelAtErrCheck:&cancelAtErrCheck{Context:probeContext,cancel:probeCancel,at:polls-1},phase:"bloomFilterCandidates"}
	failed,err:=exec.OpenNode(probe,state,filter);probeCancel()
	if !errors.Is(err,context.Canceled) || failed.Source!=nil || failed.Produce!=nil || failed.HashCandidates!=nil{failed.Close();t.Fatal("probe returned successful partial stage",err)}
	unchanged()
	warm,err:=exec.OpenNode(context.Background(),state,filter);if err!=nil{t.Fatal(err)}
	firstContext,firstCancel:=context.WithCancel(context.Background())
	first:=&cancelAtErrCheck{Context:firstContext,cancel:firstCancel}
	if _,present,err:=warm.NextOccurrence(first);err!=nil || !present{t.Fatal("consumer calibration",present,err)}
	firstPolls:=first.checks.Load();warm.Close();firstCancel()
	consuming,err:=exec.OpenNode(context.Background(),state,filter);if err!=nil{t.Fatal(err)}
	consumeContext,consumeCancel:=context.WithCancel(context.Background())
	consume:=&cancelAtErrCheck{Context:consumeContext,cancel:consumeCancel,at:firstPolls+1}
	if err:=consuming.Drain(consume);!errors.Is(err,context.Canceled) || !errors.Is(consuming.Terminal,context.Canceled){t.Fatal("consumer published partial success",err)}
	if err:=consuming.Drain(context.Background());!errors.Is(err,context.Canceled){t.Fatal("canceled stage revived",err)}
	consuming.Close();consumeCancel();unchanged()
	fresh:=hashFilterStage(t,query,source,nil)
	if err:=fresh.Drain(context.Background());err!=nil || fresh.Count()!=3{t.Fatal("fresh probe did not recover",fresh.Count(),err)}
	fresh.Close();release()

	// Cancel the actual terminal publication owner over a genuine staged batch.
	writer:=enginetest.StartSession(t,e,nil)
	ref:=enginetest.TransactionBegin(t,writer)
	enginetest.SessionSQL(t,writer,"UPDATE bloom_rows SET i=9 WHERE i=7")
	enginetest.SessionSQL(t,writer,"DELETE FROM bloom_rows WHERE i=8")
	enginetest.SessionSQL(t,writer,"INSERT INTO bloom_rows VALUES(10,'q')")
	pubContext,pubCancel:=context.WithCancel(context.Background())
	op,err:=writer.Acquire(pubContext);if err!=nil{t.Fatal(err)}
	publication:=&bloomPhaseContext{cancelAtErrCheck:&cancelAtErrCheck{Context:op.Context(),cancel:pubCancel,at:2},phase:"MergeBloomIndexRoot"}
	op.Ctx=publication
	err=core.EnginePublishRows(e,op,ref,writer.Transactions.Writes,writer.Transactions.Deletes,nil,nil,nil)
	op.Done();pubCancel()
	if !errors.Is(err,context.Canceled) || publication.checks.Load()!=2{t.Fatal("did not cancel real bloom publication",err,publication.checks.Load())}
	unchanged()
	enginetest.TransactionEnd(t,writer,ref,false)
	ref=enginetest.TransactionBegin(t,writer)
	enginetest.SessionSQL(t,writer,"INSERT INTO bloom_rows VALUES(10,'q')")
	if !enginetest.TransactionEnd(t,writer,ref,true).Committed{t.Fatal("fresh publication failed")}
	bloomAssertRoot(t,e,"bloom_rows_idx",8)
	if !reflect.DeepEqual(before,bloomSignatures(root)){t.Fatal("fresh publication mutated predecessor")}
}
