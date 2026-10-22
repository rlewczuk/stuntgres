package reference_test

import (
 "bytes"
 "context"
 "database/sql"
 "runtime"
 "testing"
 "time"

 "stuntgres/reference/fixture"
)

// Keep PostgreSQL's exact PID/query admission and diagnostics on the existing
// fixture path. Native needs exact worker identity without a deep launcher frame.
func orderedSetActiveCancel(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, operation func(context.Context) fixture.RelationOutcome) {
 t.Helper()
 if b.PG {
  fixture.BinaryConversionActiveCancel(t,p,b,c,query,"sortOrderedAggregate",operation)
  return
 }
 ctx,cancel:=context.WithTimeout(p.Ctx,15*time.Second)
 defer cancel()
 done:=make(chan fixture.RelationOutcome,1)
 identity:=make(chan []byte,1)
 joined:=false
 defer func(){cancel();if !joined {out:=fixture.LockResult(t,done);if out.Rows!=nil {out.Rows.Close()}}}()
 go func(){
  var own [128]byte
  n:=runtime.Stack(own[:],false)
  fields:=bytes.Fields(own[:n])
  identity<-bytes.Clone(fields[1])
  done<-operation(ctx)
 }()
 id:=<-identity
 stacks:=make([]byte,64<<10)
 observed:=false
 poll:=time.NewTimer(time.Hour)
 if !poll.Stop(){<-poll.C}
 defer poll.Stop()
 early:=func(out fixture.RelationOutcome){joined=true;if out.Rows!=nil {out.Rows.Close()};t.Fatal("ordered aggregate completed before real sort admission",out.Err)}
 for !observed && ctx.Err()==nil {
  select {case out:=<-done:early(out);default:}
  n:=runtime.Stack(stacks,true)
  for n==len(stacks) {stacks=make([]byte,2*len(stacks));n=runtime.Stack(stacks,true)}
  observed=orderedSetWorkerSorting(stacks[:n],id,true)
  if !observed {
   // Complete all-worker snapshots stop the world. Reuse storage and allow
   // worker progress after misses instead of continuously sampling it.
   poll.Reset(5*time.Millisecond)
   select {case <-ctx.Done():case out:=<-done:early(out);case <-poll.C:}
  }
 }
 if !observed {t.Fatal("ordered aggregate sort admission deadline",ctx.Err())}
 cancel()
 out:=fixture.LockResult(t,done)
 joined=true
 if out.Rows!=nil {out.Rows.Close();t.Fatal("canceled sort published cursor")}
 if out.Result!=nil {t.Fatal("canceled sort published result")}
 fixture.LexicalOrdinary(t,out.Err,context.Canceled)
}

// complete is false for a full (potentially truncated) runtime.Stack buffer.
// Parse bytes in place and inspect only the exact worker's live frames.
func orderedSetWorkerSorting(sample,id []byte,complete bool) bool {
 if !complete || len(id)==0 {return false}
 for _,c:=range id {if c<'0'||c>'9' {return false}}
 for len(sample)>0 {
  block,rest,found:=bytes.Cut(sample,[]byte("\n\n"))
  header,frames,ok:=bytes.Cut(block,[]byte("\n"))
  if ok {
   h,ok:=bytes.CutPrefix(header,[]byte("goroutine "))
   if ok {
    worker,state,ok:=bytes.Cut(h,[]byte(" "))
    if ok && bytes.Equal(worker,id) && len(state)>2 && state[0]=='[' && bytes.HasSuffix(state,[]byte("]:")) {
     live,_,_:=bytes.Cut(frames,[]byte("\ncreated by "))
     return bytes.Contains(live,[]byte("stuntgres/internal/engine/exec.sortOrderedAggregate"))
    }
   }
  }
  if !found {break};sample=rest
 }
 return false
}

func TestOrderedSetWorkerSorting(t *testing.T) {
 for _,tc:=range []struct{name,sample,id string;complete,want bool}{
  {"exact","goroutine 42 [running]:\nstuntgres/internal/engine/exec.sortOrderedAggregate.func1()\n\tfile.go:1\n","42",true,true},
  {"unrelated","goroutine 43 [running]:\nstuntgres/internal/engine/exec.sortOrderedAggregate()\n\tfile.go:1\n","42",true,false},
  {"created-only","goroutine 42 [running]:\nruntime.park()\n\tfile.go:1\ncreated by stuntgres/internal/engine/exec.sortOrderedAggregate in goroutine 1\n\tfile.go:2\n","42",true,false},
  {"truncated","goroutine 42 [running]:\nstuntgres/internal/engine/exec.sortOrderedAggregate()\n","42",false,false},
  {"empty","","42",true,false},
  {"malformed","goroutine 42 ???\nstuntgres/internal/engine/exec.sortOrderedAggregate()\n","42",true,false},
 } {t.Run(tc.name,func(t *testing.T){if got:=orderedSetWorkerSorting([]byte(tc.sample),[]byte(tc.id),tc.complete);got!=tc.want {t.Fatalf("sorting=%t want %t",got,tc.want)}})}
}
