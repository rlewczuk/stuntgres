package core_test

import (
 "context"
 "errors"
 "math"
 "sync"
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/exec"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/sqlstate"
 "stuntgres/internal/sqltype"
)
func TestServerSignalLiveSessions(t *testing.T){
 e:=engine.New();caller:=enginetest.StartSession(t,e,nil)
 op,_,err:=caller.AcquireCommand(context.Background());if err!=nil{t.Fatal(err)};defer op.Done()
 scope,err:=e.StartStatement(op);if err!=nil{t.Fatal(err)};defer scope.Finish(nil)
 evaluation:=&exec.Context{EvalStatement:exec.EvalStatement{Context:context.Background(),Host:core.HostOf(scope)}}
 result:=enginetest.Usage(e,sqltype.Bool).Type
 classify:=func(pid int64)(bool,error){v,err:=evaluation.ServerSignalValue(functions.SignalMemory,pid,result);if err!=nil{var d *sqlstate.Error;if errors.As(err,&d)&&d.Code=="0A000"&&d.Message=="pg_log_backend_memory_contexts backend memory-context logging is unsupported in embedded mode"{return true,nil};return false,err};b,err:=v.Boolean();if err==nil&&b{return false,errors.New("fabricated success")};return false,err}
 want:=func(pid int64,live bool){t.Helper();got,err:=classify(pid);if err!=nil||got!=live{t.Fatal(pid,got,live,err)}}
 want(int64(caller.Token().ID()),true)
 for _,pid:=range []int64{-1,0,math.MaxInt32}{want(pid,false)}
 high,err:=enginetest.OpenSession(context.Background(),e,session.SessionStart{ID:ids.SessionID(uint64(1)<<32|1900000001)});if err!=nil{t.Fatal(err)}
 want(1900000001,false);high.RequestEnd(session.CleanClose);if err:=high.WaitEnd(context.Background());err!=nil{t.Fatal(err)}
 other:=enginetest.StartSession(t,engine.New(),nil);want(int64(other.Token().ID()),false)
 failed,err:=enginetest.OpenSession(context.Background(),e,session.SessionStart{ID:1900000000,Defaults:&defaultProvider{err:errors.New("signal initialization failure")}})
 if err==nil||failed!=nil{t.Fatal("initialization unexpectedly succeeded",failed,err)}
 want(1900000000,false)
 peer:=enginetest.StartSession(t,e,nil);pid:=int64(peer.Token().ID());want(pid,true)
 lease,_,err:=peer.AcquireCommand(context.Background());if err!=nil{t.Fatal(err)}
 peer.RequestEnd(session.CleanClose)
 // The held operation prevents unregister: terminal-but-registered is unknown.
 want(pid,false);lease.Done();if err:=peer.WaitEnd(context.Background());err!=nil{t.Fatal(err)};want(pid,false)
 // Rendezvous live publication with lookup, then race request-end against lookup.
 // Each worker is joined; terminal cleanup is joined independently of lookup.
 for i:=range 24{
  published:=make(chan *session.Session);release:=make(chan struct{});done:=make(chan error,1)
  go func(){s,err:=enginetest.OpenSession(context.Background(),e,session.SessionStart{ID:ids.SessionID(2000000000+i)});if err!=nil{done<-err;close(published);return};published<-s;<-release;s.RequestEnd(session.CleanClose);done<-s.WaitEnd(context.Background())}()
  var starting sync.WaitGroup;starting.Add(1);var startupLookupErr error
  go func(){defer starting.Done();for range 16{if _,err:=classify(int64(2000000000+i));err!=nil{startupLookupErr=err;return}}}()
  s:=<-published;starting.Wait();if startupLookupErr!=nil{t.Fatal(startupLookupErr)};if s==nil{t.Fatal(<-done)};id:=int64(s.Token().ID());want(id,true)
  var wg sync.WaitGroup;wg.Add(1);var lookupErr error
  go func(){defer wg.Done();for range 16{if _,err:=classify(id);err!=nil{lookupErr=err;return}}}()
  close(release);wg.Wait();if err:=<-done;err!=nil{t.Fatal(err)};if lookupErr!=nil{t.Fatal(lookupErr)};want(id,false)
 }
}
