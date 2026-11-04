package core_test

import (
 "context"
 "errors"
 "fmt"
 "reflect"
 "stuntgres/internal/engine/exec"
 "testing"
 "time"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/analyze"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/engine/statistics"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)

func resetStatsKey(t *testing.T,e *core.Engine,s *session.Session,name string,journals ...*session.Journal) statistics.Key {
 t.Helper()
 for _,journal:=range journals { if journal!=nil { for address,descriptor:=range journal.Descriptors { if d,ok:=descriptor.(*catalog.Subscription); ok && d.Name()==name && d.Database==e.Catalog.DatabaseAddress() { return statistics.Key{Object:e.Catalog.Domain.Ref(s.Transactions.Ref.Epoch(),address),Category:statistics.Subscription} } } } }
 read:=e.CatalogRead()
 for address,d:=range read.Subscriptions() { if d.Name()==name && d.Database==e.Catalog.DatabaseAddress() { return statistics.Key{Object:e.Catalog.Domain.Ref(read.Epoch(),address),Category:statistics.Subscription} } }
 t.Fatal("missing genuine owning subscription",name); return statistics.Key{}
}
func resetStatsCreate(t *testing.T,e *core.Engine,s *session.Session,name string) statistics.Key {
 t.Helper(); statsFunctionExec(t,s,"CREATE SUBSCRIPTION "+name+" CONNECTION '' PUBLICATION p WITH (connect=false,slot_name=NONE)")
 key:=resetStatsKey(t,e,s,name,s.Transactions.Catalog)
 var snapshot statistics.Reader; snapshot.Configure("snapshot",1)
 if got:=snapshot.Read(&e.Statistics,key,time.Now()); got!=(statistics.Fact{Generation:got.Generation}) || !statsLifetimeSnapshotContains(&snapshot,key) { t.Fatal("actual creation did not establish zero/NULL record",got) }
 return key
}
func resetStatsVector(start int64) statistics.Fact { return statistics.Fact{ApplyErrorCount:start,SyncErrorCount:start+1,ConflInsertExists:start+2,ConflUpdateOriginDiffers:start+3,ConflUpdateExists:start+4,ConflUpdateMissing:start+5,ConflDeleteOriginDiffers:start+6,ConflDeleteMissing:start+7,ConflMultipleUniqueConflicts:start+8} }
func resetStatsSeed(e *core.Engine,key statistics.Key,start int64) statistics.Fact {
 var writer statistics.Writer; writer.Add(&e.Statistics,key,resetStatsVector(start)); writer.Flush(&e.Statistics,time.Now(),true); return e.Statistics.Read(key)
}
func resetStatsAbsent(t *testing.T,e *core.Engine,key statistics.Key) {
 t.Helper(); var reader statistics.Reader; reader.Configure("snapshot",1)
 if got:=reader.Read(&e.Statistics,key,time.Now()); got!=(statistics.Fact{Generation:got.Generation}) || statsLifetimeSnapshotContains(&reader,key) { t.Fatal("retired/missing key retained shared authority",key,got) }
}
func resetStatsZero(t *testing.T,e *core.Engine,key statistics.Key) time.Time {
 t.Helper(); got:=e.Statistics.Read(key)
 if got.ResetAt.IsZero() || got.ResetAt.Nanosecond()%1000!=0 || got!=(statistics.Fact{Generation:got.Generation,ResetAt:got.ResetAt}) { t.Fatal("target not zeroed/stamped",key,got) }; return got.ResetAt
}
// Consume the real typed cursor: the generic test exporter intentionally does
// not admit VOID. Direct and prepared reset effects share these assertions.
func resetStatsConsumeVoid(ctx context.Context, result core.Result) (err error) {
 if result.Rows==nil { return fmt.Errorf("reset returned no scalar VOID cursor") }
 defer func(){ if closeErr:=result.Rows.Close(); err==nil { err=closeErr } }()
 columns:=result.Rows.Schema()
 if len(columns)!=1 || columns[0].Usage.Type==nil || columns[0].Usage.Type.ID()!=sqltype.Void {
  return fmt.Errorf("reset returned an invalid scalar VOID schema: %v",columns)
 }
 var values [1]sqltype.Value
 present,err:=result.Rows.Next(ctx,values[:])
 if err!=nil { return err }
 if !present || values[0].IsNull() || values[0].Type()!=columns[0].Usage.Type {
  return fmt.Errorf("reset did not return one non-NULL typed VOID datum")
 }
 text,err:=values[0].Type().Output(ctx,values[0])
 if err!=nil { return err }
 if text!="" { return fmt.Errorf("reset returned nonempty VOID output %q",text) }
 present,err=result.Rows.Next(ctx,values[:])
 if err!=nil { return err }
 if present { return fmt.Errorf("reset returned more than one scalar VOID row") }
 return nil
}
func resetStatsSQL(t *testing.T,s *session.Session,query string) error {
 t.Helper()
 op,err:=s.Acquire(context.Background()); if err!=nil { return err }; defer op.Done()
 statement:=enginetest.Statement(t,query)
 result,err:=core.EngineOf(s).Execute(op,statement,0,nil)
 if err!=nil { if result.Rows!=nil { result.Rows.Close() }; return err }
 if _,selectStatement:=statement.(*parser.Select); selectStatement || result.Rows!=nil {
  return resetStatsConsumeVoid(op.Context(),result)
 }
 return nil
}
func resetStatsExec(t *testing.T,s *session.Session,queries ...string) {
 t.Helper()
 for _,query:=range queries { if err:=resetStatsSQL(t,s,query); err!=nil { t.Fatalf("%s: %v",query,err) } }
}

func resetStatsPrepared(e *core.Engine,s *session.Session,ast parser.Statement,d *core.Description,argument *string) error {
 op,err:=s.Acquire(context.Background()); if err!=nil { return err }; defer op.Done()
 scope,err:=e.StartStatement(op); if err!=nil { return err }; scope.Expected=d
 result,err:=scope.Execute(ast,1,func(ctx context.Context,info analyze.ParameterInfo,frame []sqltype.Value,input sqltype.InputEnvironment) error {
  if argument==nil { frame[0]=sqltype.Null(info.Types[0].Type); return nil }
  value,err:=info.Types[0].Type.InputWithEnvironment(ctx,*argument,-1,input); frame[0]=value; return err
 })
 err=scope.Finish(err)
 if err!=nil { if result.Rows!=nil { result.Rows.Close() }; return err }
 err=resetStatsConsumeVoid(op.Context(),result)
 return err
}
func resetStatsFixture(t *testing.T) (*core.Engine,*session.Session,*core.Engine,*session.Session,[]statistics.Key) {
 t.Helper(); e:=engine.New(); s:=enginetest.StartSession(t,e,nil)
 a,b:=resetStatsCreate(t,e,s,"reset_target_a"),resetStatsCreate(t,e,s,"reset_target_b")
 statsFunctionExec(t,s,"CREATE DATABASE reset_target_peer")
 peer,err:=e.SelectDatabase(context.Background(),"reset_target_peer"); if err!=nil { t.Fatal(err) }
 ps:=enginetest.StartSession(t,peer,nil); c:=resetStatsCreate(t,peer,ps,"reset_target_c")
 return e,s,peer,ps,[]statistics.Key{a,b,c}
}
func TestStatsResetReplicationTargeting(t *testing.T) {
 e,s,peer,ps,keys:=resetStatsFixture(t); a,b,c:=keys[0],keys[1],keys[2]
 controls:=resetStatsControls(t,e,s)
 beforeA,beforeB,beforeC:=resetStatsSeed(e,a,1),resetStatsSeed(e,b,11),resetStatsSeed(peer,c,21)
 independent:=engine.New(); is:=enginetest.StartSession(t,independent,nil); ik:=resetStatsCreate(t,independent,is,"reset_independent"); independentFact:=resetStatsSeed(independent,ik,31)
 descriptorA,descriptorC:=e.CatalogRead().Get(a.Object.Address),peer.CatalogRead().Get(c.Object.Address)
 projected:=statistics.Key{Object:e.Catalog.Domain.Ref(e.CatalogRead().Epoch(),c.Object.Address),Category:statistics.Subscription}; resetStatsAbsent(t,e,projected)
 query:="SELECT pg_stat_reset_subscription_stats($1::oid)"
 ast,d:=enginetest.PreparedDescription(t,e,s,query)
 if e.Statistics.Read(a)!=beforeA || e.Statistics.Read(b)!=beforeB || peer.Statistics.Read(c)!=beforeC { t.Fatal("Describe reset shared records") }
 argument:=fmt.Sprint(a.Object.Address.ObjectID)
 if err:=resetStatsPrepared(e,s,ast,&d,&argument); err!=nil { t.Fatal(err) }; resetStatsZero(t,e,a)
 if e.Statistics.Read(b)!=beforeB || peer.Statistics.Read(c)!=beforeC { t.Fatal("single reset changed peer") }
 argument=fmt.Sprint(c.Object.Address.ObjectID)
 if err:=resetStatsPrepared(e,s,ast,&d,&argument); err!=nil { t.Fatal(err) }; resetStatsZero(t,peer,c)
 if e.Statistics.Read(b)!=beforeB { t.Fatal("foreign reset changed local record") }
 statsFunctionExec(t,ps,"BEGIN")
 private:=resetStatsCreate(t,peer,ps,"reset_target_private"); resetStatsSeed(peer,private,41)
 resetStatsExec(t,ps,fmt.Sprintf("SELECT pg_stat_reset_subscription_stats(%d::oid)",private.Object.Address.ObjectID))
 resetStatsZero(t,peer,private); resetStatsSeed(peer,private,41)
 if err:=resetStatsPrepared(e,s,ast,&d,nil); err!=nil { t.Fatal(err) }
 stamp:=resetStatsZero(t,e,a)
 for _,entry:=range []struct{e *core.Engine;k statistics.Key}{{e,b},{peer,c},{peer,private}} { if !resetStatsZero(t,entry.e,entry.k).Equal(stamp) { t.Fatal("NULL-all did not use one timestamp") } }
 if independent.Statistics.Read(ik)!=independentFact { t.Fatal("reset crossed independent cluster") }
 if e.CatalogRead().Get(a.Object.Address)!=descriptorA || peer.CatalogRead().Get(c.Object.Address)!=descriptorC { t.Fatal("reset mutated catalog") }
 statsFunctionExec(t,ps,"ROLLBACK"); resetStatsAbsent(t,peer,private)
 missing:=a; missing.Object.Address.ObjectID=4294967294
 argument="4294967294"; if err:=resetStatsPrepared(e,s,ast,&d,&argument); err!=nil { t.Fatal(err) }; resetStatsAbsent(t,e,missing)
 before:=e.Statistics.Read(a)
 for _,q:=range []string{"SELECT pg_stat_reset_subscription_stats(0::oid)","SELECT pg_stat_reset_subscription_stats(true)","SELECT pg_stat_reset_replication_slot('missing')"} { if err:=resetStatsSQL(t,s,q); err==nil { t.Fatal("invalid reset admitted",q) }; if e.Statistics.Read(a)!=before { t.Fatal("rejected reset changed record",q) } }
 resetStatsExec(t,s,"SELECT pg_stat_reset_replication_slot(NULL)")
 if e.Statistics.Read(a)!=before { t.Fatal("slot NULL changed subscription") }
 beforeB=resetStatsSeed(e,b,51)
 resetStatsExec(t,s,"BEGIN","SAVEPOINT reset_mark",fmt.Sprintf("SELECT pg_stat_reset_subscription_stats(%d::oid)",b.Object.Address.ObjectID))
 resetStamp:=resetStatsZero(t,e,b)
 statsFunctionExec(t,s,"ROLLBACK TO reset_mark","ROLLBACK")
 if got:=e.Statistics.Read(b); got.ResetAt!=resetStamp || got.Generation!=beforeB.Generation || got.ApplyErrorCount!=0 { t.Fatal("rollback undid immediate reset",got) }
 ctx,cancel:=context.WithCancel(context.Background()); cancel()
 if _,err:=enginetest.Execute(e,ctx,enginetest.Statement(t,"SELECT pg_stat_reset_subscription_stats(NULL)"),nil); !errors.Is(err,context.Canceled) { t.Fatal(err) }
 if e.Statistics.Read(a)!=before { t.Fatal("pre-canceled reset changed record") }
 expired,expire:=context.WithDeadline(context.Background(),time.Unix(1,0)); defer expire()
 if _,err:=enginetest.Execute(e,expired,enginetest.Statement(t,"SELECT pg_stat_reset_subscription_stats(NULL)"),nil); !errors.Is(err,context.DeadlineExceeded) { t.Fatal(err) }
 if e.Statistics.Read(a)!=before { t.Fatal("expired reset changed record") }
 resetStatsWantControls(t,e,controls)
}

func TestStatsResetReplicationReaders(t *testing.T) {
 e,s,peer,_,keys:=resetStatsFixture(t); a,b,c:=keys[0],keys[1],keys[2]
 beforeA,beforeB,beforeC:=resetStatsSeed(e,a,1),resetStatsSeed(e,b,11),resetStatsSeed(peer,c,21)
 for _,mode:=range []string{"cache","snapshot"} { t.Run(mode,func(t *testing.T) {
  var reader,other,none statistics.Reader; reader.Configure(mode,1); other.Configure(mode,1); none.Configure("none",1)
  captured:=time.Unix(100,0).UTC()
  reader.Read(&e.Statistics,a,captured); reader.Read(&e.Statistics,b,captured); other.Read(&peer.Statistics,c,captured)
  var pending statistics.Writer; pending.Add(&e.Statistics,a,resetStatsVector(2))
  resetStatsExec(t,s,"SELECT pg_stat_reset_subscription_stats(NULL)")
  stamp:=resetStatsZero(t,e,a); if !resetStatsZero(t,peer,c).Equal(stamp) { t.Fatal("cross-provider timestamp") }
  if reader.Read(&e.Statistics,a,time.Now())!=beforeA || reader.Read(&e.Statistics,b,time.Now())!=beforeB || other.Read(&peer.Statistics,c,time.Now())!=beforeC { t.Fatal("retained readers invalidated") }
  if mode=="snapshot" && reader.Timestamp()!=captured { t.Fatal("snapshot timestamp changed") }
  if none.Read(&e.Statistics,a,time.Now()).ResetAt!=stamp || !pending.Pending() { t.Fatal("immediate shared/pending separation") }
  pending.Flush(&e.Statistics,time.Now(),true)
  want:=resetStatsVector(2); want.ResetAt=stamp; want.Generation=beforeA.Generation
  if got:=e.Statistics.Read(a); got!=want || reader.Read(&e.Statistics,a,time.Now())!=beforeA { t.Fatal("pending merge/cache retention",got,want) }
  reader.Clear(); if got:=reader.Read(&e.Statistics,a,time.Now()); got!=want { t.Fatal("explicit clear",got,want) }
  beforeA=resetStatsSeed(e,a,1); beforeB=resetStatsSeed(e,b,11); beforeC=resetStatsSeed(peer,c,21)
 }) }
 // Exercise the resetting physical session's real transaction-owned full
 // snapshot, not merely independent Reader values.
 statsFunctionExec(t,s,"CREATE TABLE reset_reader_control(v integer)","SET stats_fetch_consistency=snapshot","BEGIN")
 table:=enginetest.RoleTableRef(t,e,"reset_reader_control")
 op,err:=s.Acquire(context.Background()); if err!=nil { t.Fatal(err) }
 scope,err:=e.StartStatement(op); if err!=nil { op.Done(); t.Fatal(err) }
 defer func(){ if op!=nil { scope.Finish(context.Canceled); op.Done() } }()
 e.Mu.Lock(); auth,err:=e.AuthorizationLocked(op); e.Mu.Unlock(); if err!=nil { t.Fatal(err) }
 runtime:=core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine:e,Auth:auth,Settings:s.Settings})
 runtime.TableStatistics(table.Address)
 ctx:=&exec.Context{EvalStatement:exec.EvalStatement{Context:context.Background(),Host:core.HostOf(scope)}}
 timestamp,err:=ctx.StatsSnapshotValue(false,enginetest.Usage(e,sqltype.Timestamptz).Type); if err!=nil || timestamp.IsNull() { t.Fatal("missing real session snapshot",err) }
 if err:=scope.Finish(nil); err!=nil { t.Fatal(err) }; op.Done(); op=nil
 owned:=reflect.ValueOf(core.PayloadOf(s)).Elem().FieldByName("statsReader")
 capture:=owned.FieldByName("cache").MapIndex(reflect.ValueOf(a))
 if !capture.IsValid() || !capture.FieldByName("exists").Bool() || capture.FieldByName("fact").FieldByName("ApplyErrorCount").Int()!=beforeA.ApplyErrorCount { t.Fatal("session snapshot did not capture actual subscription") }
 resetStatsExec(t,s,"SELECT pg_stat_reset_subscription_stats(NULL)")
 retained:=owned.FieldByName("cache").MapIndex(reflect.ValueOf(a))
 if !retained.IsValid() || !retained.FieldByName("exists").Bool() || retained.FieldByName("fact").FieldByName("ApplyErrorCount").Int()!=beforeA.ApplyErrorCount { t.Fatal("reset invalidated resetting session reader") }
 resetStatsExec(t,s,"SELECT pg_stat_clear_snapshot()")
 if owned.FieldByName("cache").Len()!=0 { t.Fatal("explicit clear retained session cache") }
 statsFunctionExec(t,s,"ROLLBACK")
 if owned.FieldByName("cache").Len()!=0 || !owned.FieldByName("stamp").IsZero() { t.Fatal("terminal transaction retained reader") }
}

func TestStatsResetReplicationStateSmoke(t *testing.T) {
 e,s,peer,_,keys:=resetStatsFixture(t); a,b,c:=keys[0],keys[1],keys[2]
 controls:=resetStatsControls(t,e,s)
 beforeA,beforeB,beforeC:=resetStatsSeed(e,a,1),resetStatsSeed(e,b,11),resetStatsSeed(peer,c,21)
 var snapshot statistics.Reader; snapshot.Configure("snapshot",1); snapshot.Read(&e.Statistics,a,time.Now())
 var pending statistics.Writer; pending.Add(&e.Statistics,a,resetStatsVector(2))
 resetStatsExec(t,s,fmt.Sprintf("SELECT pg_stat_reset_subscription_stats(%d::oid)",a.Object.Address.ObjectID)); resetStatsZero(t,e,a)
 if e.Statistics.Read(b)!=beforeB || peer.Statistics.Read(c)!=beforeC || snapshot.Read(&e.Statistics,a,time.Now())!=beforeA { t.Fatal("named target/cache effect") }
 ast,d:=enginetest.PreparedDescription(t,e,s,"SELECT pg_stat_reset_subscription_stats($1::oid)")
 if err:=resetStatsPrepared(e,s,ast,&d,nil); err!=nil { t.Fatal(err) }; stamp:=resetStatsZero(t,e,a)
 if !resetStatsZero(t,e,b).Equal(stamp) || !resetStatsZero(t,peer,c).Equal(stamp) { t.Fatal("all-target effect") }
 pending.Flush(&e.Statistics,time.Now(),true)
 want:=resetStatsVector(2); want.ResetAt=stamp; want.Generation=beforeA.Generation
 if e.Statistics.Read(a)!=want { t.Fatal("pending publication",e.Statistics.Read(a),want) }
 resetStatsWantControls(t,e,controls)
 pending.Add(&e.Statistics,a,resetStatsVector(30)); statsFunctionExec(t,s,"DROP SUBSCRIPTION reset_target_a"); pending.Flush(&e.Statistics,time.Now(),true); resetStatsAbsent(t,e,a)
 control:=resetStatsSeed(peer,c,71)
 if err:=engine.Reset(context.Background(),e); err!=nil { t.Fatal(err) }
 if peer.Statistics.Read(c)!=control { t.Fatal("selected reset changed peer database") }
}


func resetStatsControls(t *testing.T,e *core.Engine,s *session.Session) map[statistics.Key]statistics.Fact {
 t.Helper()
 statsFunctionExec(t,s,"CREATE TABLE reset_unrelated_control(v integer)","CREATE FUNCTION reset_unrelated_function() RETURNS integer LANGUAGE SQL AS 'SELECT 7'")
 relation:=statistics.Key{Object:enginetest.RoleTableRef(t,e,"reset_unrelated_control"),Category:statistics.Relation}
 function:=statsFunctionKey(t,e,"reset_unrelated_function")
 io,wal:=relation,relation; io.Category,wal.Category=statistics.IO,statistics.WAL
 facts:=map[statistics.Key]statistics.Fact{relation:{Inserts:7,Live:7},function:{Calls:5,TotalTime:9*time.Millisecond,SelfTime:4*time.Millisecond},io:{Operations:3,TotalTime:2*time.Millisecond},wal:{Operations:4,TotalTime:6*time.Millisecond}}
 var writer statistics.Writer
 for key,fact:=range facts { writer.Add(&e.Statistics,key,fact) }; writer.Flush(&e.Statistics,time.Now(),true)
 for key:=range facts { facts[key]=e.Statistics.Read(key) }; return facts
}
func resetStatsWantControls(t *testing.T,e *core.Engine,facts map[statistics.Key]statistics.Fact) {
 t.Helper(); for key,want:=range facts { if got:=e.Statistics.Read(key); got!=want { t.Fatal("reset changed unrelated category",key,got,want) } }
}
