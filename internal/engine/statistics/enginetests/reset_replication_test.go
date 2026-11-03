package statistics_test

import (
 "reflect"
 "testing"
 "time"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/statistics"
)

func resetSubscriptionVector(start int64) statistics.Fact {
 return statistics.Fact{ApplyErrorCount:start,SyncErrorCount:start+1,ConflInsertExists:start+2,ConflUpdateOriginDiffers:start+3,ConflUpdateExists:start+4,ConflUpdateMissing:start+5,ConflDeleteOriginDiffers:start+6,ConflDeleteMissing:start+7,ConflMultipleUniqueConflicts:start+8}
}
func TestStatsProviderTargetedReset(t *testing.T) {
 t.Run("oid_existing_entry_authority",statsProviderResetSubscriptionOID)
 var p statistics.Provider
 a,b:=statsKeys(); a.Category,b.Category=statistics.Subscription,statistics.Subscription
 slot:=a; slot.Category=statistics.ReplicationSlot
 relation,function,io,wal:=a,a,a,a
 relation.Category,function.Category,io.Category,wal.Category=statistics.Relation,statistics.Function,statistics.IO,statistics.WAL
 t0:=time.Unix(100,0).UTC(); t1,t2,t3,t4,t5:=t0.Add(time.Second),t0.Add(2*time.Second),t0.Add(3*time.Second),t0.Add(4*time.Second),t0.Add(5*time.Second)
 p.Reset(t0)
 p.Command(ids.SessionToken{},ids.CommandIdentity{},"reset activity control",t0,true)
 var activityReader statistics.Reader
 activityBefore:=activityReader.Activities(&p)
 p.EnsureSubscription(a); p.EnsureSubscription(b)
 var seed statistics.Writer
 seed.Add(&p,a,resetSubscriptionVector(1)); seed.Add(&p,b,resetSubscriptionVector(11))
 seed.Add(&p,slot,statistics.Fact{SpillTxns:21,SpillCount:22,SpillBytes:23,StreamTxns:24,StreamCount:25,StreamBytes:26,TotalTxns:27,TotalBytes:28})
 controls:=map[statistics.Key]statistics.Fact{relation:{Inserts:7,Live:7},function:{Calls:5,TotalTime:9*time.Millisecond,SelfTime:4*time.Millisecond},io:{Operations:3,TotalTime:2*time.Millisecond},wal:{Operations:4,TotalTime:6*time.Millisecond}}
 for key,f:=range controls { seed.Add(&p,key,f) }; seed.Flush(&p,t0,true)
 p.ResetEntry(a,t0); p.ResetEntry(b,t1); p.ResetEntry(slot,t2)
 seed.Add(&p,a,resetSubscriptionVector(1)); seed.Add(&p,b,resetSubscriptionVector(11)); seed.Add(&p,slot,statistics.Fact{SpillTxns:21,SpillCount:22,SpillBytes:23,StreamTxns:24,StreamCount:25,StreamBytes:26,TotalTxns:27,TotalBytes:28}); seed.Flush(&p,t2,true)
 beforeA,beforeB,beforeSlot:=p.Read(a),p.Read(b),p.Read(slot)
 for key:=range controls { controls[key]=p.Read(key) }
 var cache,snapshot,none statistics.Reader
 cache.Configure("cache",1); snapshot.Configure("snapshot",1); none.Configure("none",1)
 if cache.Read(&p,a,t2)!=beforeA || snapshot.Read(&p,a,t2)!=beforeA { t.Fatal("seed capture") }
 if !p.ResetEntry(a,t3) { t.Fatal("actual entry absent") }
 zero:=statistics.Fact{ResetAt:t3,Generation:beforeA.Generation}
 if got:=p.Read(a); got!=zero { t.Fatal("target counters/stamp",got,zero) }
 if p.Read(b)!=beforeB || p.Read(slot)!=beforeSlot { t.Fatal("single reset touched peer category/entry") }
 for key,want:=range controls { if got:=p.Read(key); got!=want { t.Fatal("unrelated category changed",key,got,want) } }
 if cache.Read(&p,a,t3)!=beforeA || snapshot.Read(&p,a,t3)!=beforeA || snapshot.Timestamp()!=t2 { t.Fatal("reset invalidated retained readers") }
 if none.Read(&p,a,t3)!=zero { t.Fatal("none did not see immediate reset") }
 missing:=b; missing.Object.Address.ObjectID++
 if p.ResetEntry(missing,t3) { t.Fatal("reset allocated missing entry") }
 var fresh statistics.Reader; fresh.Configure("snapshot",1)
 if got:=fresh.Read(&p,missing,t3); !got.ResetAt.IsZero() || got.Generation!=beforeA.Generation { t.Fatal("absent inherited database reset",got) }
 if reflect.ValueOf(&fresh).Elem().FieldByName("cache").MapIndex(reflect.ValueOf(missing)).IsValid() { t.Fatal("missing zero record allocated") }
 missingAfter:=missing; missingAfter.Object.Address.ObjectID++
 if got:=fresh.Read(&p,missingAfter,t3); !got.ResetAt.IsZero() || got.Generation!=beforeA.Generation { t.Fatal("retained full snapshot missing key inherited global reset",got) }
 p.ResetCategory(statistics.Subscription,t4)
 for _,key:=range []statistics.Key{a,b} { if got:=p.Read(key); got!=(statistics.Fact{ResetAt:t4,Generation:beforeA.Generation}) { t.Fatal("category reset",key,got) } }
 if p.Read(slot)!=beforeSlot { t.Fatal("category reset changed slot") }
 p.ResetCategory(statistics.ReplicationSlot,t5)
 if got:=p.Read(slot); got!=(statistics.Fact{ResetAt:t5,Generation:beforeA.Generation}) { t.Fatal("slot reset",got) }
 for key,want:=range controls { if p.Read(key)!=want { t.Fatal("global generation/reset changed",key) } }
 var activityAfter statistics.Reader
 if !reflect.DeepEqual(activityBefore,activityAfter.Activities(&p)) { t.Fatal("targeted reset changed activity") }
 // Pending counters survive unchanged, including the throttle frontier. A
 // non-forced early flush remains throttled; the later normal publication adds
 // exactly the pending vector and retains the shared reset timestamp.
 var pending statistics.Writer
 pending.Add(&p,a,resetSubscriptionVector(2)); pending.Flush(&p,t5,true)
 pending.Add(&p,a,resetSubscriptionVector(2)); pending.Add(&p,relation,statistics.Fact{Inserts:2,Live:2})
 pendingBefore:=reflect.ValueOf(&pending).Elem().FieldByName("pending").Len()
 p.ResetEntry(a,t5)
 if !pending.Pending() || reflect.ValueOf(&pending).Elem().FieldByName("pending").Len()!=pendingBefore || pending.Flush(&p,t5.Add(time.Millisecond),false) { t.Fatal("reset changed pending/throttle") }
 if !pending.Flush(&p,t5.Add(time.Second),false) { t.Fatal("normal pending publication failed") }
 want:=resetSubscriptionVector(2); want.ResetAt=t5; want.Generation=beforeA.Generation
 if got:=p.Read(a); got!=want || p.Read(relation).Inserts!=9 { t.Fatal("pending counts or reset stamp lost",got,want,p.Read(relation)) }
 if cache.Read(&p,a,t5)!=beforeA { t.Fatal("publication changed retained cache") }; cache.Clear()
 if cache.Read(&p,a,t5)!=want { t.Fatal("clear retained old authority") }
 pending.Add(&p,a,resetSubscriptionVector(30)); p.Retire(a); p.ResetEntry(a,t5); p.ResetCategory(statistics.Subscription,t5); p.EnsureSubscription(a)
 pending.Flush(&p,t5.Add(2*time.Second),true); pending.Add(&p,a,resetSubscriptionVector(40)); pending.Flush(&p,t5.Add(3*time.Second),true)
 fresh.Clear(); fresh.Read(&p,a,t5)
 if reflect.ValueOf(&fresh).Elem().FieldByName("cache").MapIndex(reflect.ValueOf(a)).IsValid() || p.Read(a)!=(statistics.Fact{Generation:beforeA.Generation}) { t.Fatal("reset resurrected tombstoned record",p.Read(a)) }
 t.Run("concurrent_atomicity",func(t *testing.T){
  var shared statistics.Provider
  key,_:=statsKeys(); key.Category=statistics.Subscription
  shared.EnsureSubscription(key)
  started,done:=make(chan struct{}),make(chan struct{})
  go func(){
   defer close(done); <-started
   var writer statistics.Writer
   for range 100 {
    writer.Add(&shared,key,resetSubscriptionVector(1)); writer.Flush(&shared,t0,true)
    shared.ResetEntry(key,t1)
   }
  }()
  close(started)
  var invalid statistics.Fact
  for range 100 {
   f:=shared.Read(key); counts:=f; counts.Generation=0; counts.ResetAt=time.Time{}
   if counts!=(statistics.Fact{}) && counts!=resetSubscriptionVector(1) { invalid=f; break }
  }
  <-done
  if invalid!=(statistics.Fact{}) { t.Fatal("partial shared vector observed",invalid) }
 })
}

func statsProviderResetSubscriptionOID(t *testing.T) {
 owner,caller:=&ids.Domain{},&ids.Domain{}
 address:=ids.ObjectAddress{ClassID:ids.SubscriptionClassID,ObjectID:900001}
 target:=statistics.Key{Object:owner.Ref(7,address),Category:statistics.Subscription}
 projected:=statistics.Key{Object:caller.Ref(11,address),Category:statistics.Subscription}
 other:=target; other.Object.Address.ObjectID++
 subobject:=target; subobject.Object.Address.SubID=1
 wrongClass:=target; wrongClass.Object.Address.ClassID=ids.RelationClassID
 wrongCategory:=target; wrongCategory.Category=statistics.Function
 relation:=wrongClass; relation.Category=statistics.Relation
 t0,t1:=time.Unix(100,0).UTC(),time.Unix(200,0).UTC()
 targetFact:=resetSubscriptionVector(1); targetFact.ResetAt=t0; targetFact.Inserts=7; targetFact.Calls=5
 var p statistics.Provider
 p.Replace(t0,map[statistics.Key]statistics.Fact{
  target:targetFact,other:resetSubscriptionVector(11),subobject:resetSubscriptionVector(21),
  wrongClass:resetSubscriptionVector(31),wrongCategory:{Calls:41},relation:{Inserts:7,Live:7},
 })
 controls:=make(map[statistics.Key]statistics.Fact)
 for _,key:=range []statistics.Key{other,subobject,wrongClass,wrongCategory,relation} { controls[key]=p.Read(key) }
 before:=p.Read(target)
 var pending statistics.Writer
 pending.Add(&p,target,resetSubscriptionVector(2)); pending.Add(&p,relation,statistics.Fact{Inserts:2,Live:2})
 if !p.ResetSubscriptionOID(address.ObjectID,t1) { t.Fatal("actual shared subscription OID not found") }
 want:=statistics.Fact{Inserts:7,Calls:5,ResetAt:t1,Generation:before.Generation}
 if got:=p.Read(target); got!=want { t.Fatal("OID reset lost stored key/fields/generation",got,want) }
 for key,fact:=range controls { if p.Read(key)!=fact { t.Fatal("OID lookup ignored full address/category",key) } }
 var snapshot statistics.Reader; snapshot.Configure("snapshot",1); snapshot.Read(&p,target,t1)
 if !reflect.ValueOf(&snapshot).Elem().FieldByName("cache").MapIndex(reflect.ValueOf(target)).IsValid() || reflect.ValueOf(&snapshot).Elem().FieldByName("cache").MapIndex(reflect.ValueOf(projected)).IsValid() { t.Fatal("OID reset synthesized caller-domain key") }
 if p.ResetSubscriptionOID(4294967294,t1) { t.Fatal("absent OID allocated/found") }
 if !pending.Pending() { t.Fatal("OID reset discarded pending batch") }
 pending.Flush(&p,t1,true)
 want=resetSubscriptionVector(2); want.Inserts=7; want.Calls=5; want.ResetAt=t1; want.Generation=before.Generation
 if p.Read(target)!=want || p.Read(relation).Inserts!=9 { t.Fatal("OID reset lost pending delta/stamp",p.Read(target),want) }
 pending.Add(&p,target,resetSubscriptionVector(51)); p.Retire(target)
 if p.ResetSubscriptionOID(address.ObjectID,t1) { t.Fatal("named reset found retired identity") }
 pending.Flush(&p,t1,true); pending.Add(&p,target,resetSubscriptionVector(61)); pending.Flush(&p,t1,true)
 snapshot.Clear(); snapshot.Read(&p,target,t1)
 if reflect.ValueOf(&snapshot).Elem().FieldByName("cache").MapIndex(reflect.ValueOf(target)).IsValid() { t.Fatal("named reset/late writer resurrected retired key") }
 var empty statistics.Provider
 if empty.ResetSubscriptionOID(address.ObjectID,t1) || !reflect.ValueOf(&empty).Elem().FieldByName("facts").IsNil() { t.Fatal("missing OID allocated provider storage") }
}
