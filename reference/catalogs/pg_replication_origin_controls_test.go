package reference_test

import (
 "bytes"
 "context"
 "errors"
 "reflect"
 "strings"
 "stuntgres"
 "stuntgres/reference/fixture"
 "testing"
)

func originControlScenario(t *testing.T,name string,smoke bool) {
 t.Helper()
 databases:=[]string{"origin_control_peer"}; fixture.DatabaseReferenceCleanup(t,&databases)
 p:=subscriptionScenario(t,name,"origin_control_pending","origin_control_stale")
 for _,b:=range p.Backends() { t.Run(b.Name,func(t *testing.T) {
  subscriptionRun(t,p,b,b.DB,originEmpty(b.PG,"SELECT * FROM pg_replication_origin WHERE FALSE"))
  originCreate(t,p,b,b.DB,name,false)
  id,canonical:=originIdentity(t,p,b.DB,name)
  originCheck(t,p,b,b.DB,name,id,canonical)
  actor,observer:=fixture.LockConnections(t,p.Ctx,b.DB)
  statement,err:=observer.PrepareContext(p.Ctx,originQuery(name)); if err!=nil { t.Fatal(err) }; defer statement.Close()
  held:=fixture.HeldStatement{Executor:observer,Statement:statement,SQL:originQuery(name)}
  originCheck(t,p,b,held,name,id,canonical)
  subscriptionExec(t,p,b,actor,"BEGIN",false)
  subscriptionExec(t,p,b,actor,"DROP SUBSCRIPTION "+name,false)
  originCheck(t,p,b,actor,name,0,"")
  subscriptionExec(t,p,b,actor,"ROLLBACK",false)
  originCheck(t,p,b,held,name,id,canonical)
  subscriptionExec(t,p,b,actor,"BEGIN",false)
  originCreate(t,p,b,actor,"origin_control_stale",false)
  subscriptionExec(t,p,b,actor,"ROLLBACK",false)
  originCheck(t,p,b,observer,"origin_control_stale",0,"")
  if !b.PG {
   var first,second bytes.Buffer
   if err:=stuntgres.Dump(p.Ctx,p.Native,&first);err!=nil { t.Fatal(err) }
   if err:=stuntgres.Dump(p.Ctx,p.Native,&second);err!=nil { t.Fatal(err) }
   if !bytes.Equal(first.Bytes(),second.Bytes()) || strings.Contains(first.String(),"pg_replication_origin_create") || strings.Contains(first.String(),"pg_replication_origin_drop") { t.Fatal("nonportable or nondeterministic origin dump") }
   before:=originBag(t,p,b.DB)
   failure:=errors.New("origin control I/O")
   if err:=stuntgres.Dump(p.Ctx,p.Native,fixture.CatalogControlFailingWriter{Err:failure});!errors.Is(err,failure) { t.Fatal(err) }
   if err:=stuntgres.Restore(p.Ctx,p.Native,fixture.CatalogControlFailingReader{Err:failure});!errors.Is(err,failure) { t.Fatal(err) }
   canceled,cancel:=context.WithCancel(p.Ctx); cancel()
   if err:=stuntgres.Reset(canceled,p.Native);!errors.Is(err,context.Canceled) { t.Fatal(err) }
   if err:=stuntgres.Restore(canceled,p.Native,bytes.NewReader(first.Bytes()));!errors.Is(err,context.Canceled) { t.Fatal(err) }
   if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(first.String()+"CREATE SUBSCRIPTION late_origin CONNECTION 'testconn' PUBLICATION p WITH (connect=false);"));err==nil { t.Fatal("late invalid restore published") }
   if !reflect.DeepEqual(before,originBag(t,p,b.DB)) { t.Fatal("failed replacement changed origins") }
   old,err:=b.DB.QueryContext(p.Ctx,originQuery(name));if err!=nil { t.Fatal(err) }; defer old.Close()
   if !smoke {
    subscriptionExec(t,p,b,b.DB,"CREATE DATABASE origin_control_peer",false)
    peer,_,recorder:=fixture.DatabaseScenarioPool(t,p,false,"origin_control_peer","")
    pb:=b;pb.DB=peer;pb.Recorder=recorder
    originCreate(t,p,pb,peer,"origin_control_peer_sub",false)
    peerID,peerName:=originIdentity(t,p,peer,"origin_control_peer_sub")
    // Dump's donor ID collides with destination's first registration; retained
    // peers and unpublished peer candidates must both survive remint.
    tx,err:=peer.BeginTx(p.Ctx,nil);if err!=nil { t.Fatal(err) };defer tx.Rollback()
    originCreate(t,p,pb,tx,"origin_control_pending",false)
    pendingID,pendingName:=originIdentity(t,p,tx,"origin_control_pending")
    if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil { t.Fatal(err) }
    originCheck(t,p,b,b.DB,name,0,"")
    originCheck(t,p,pb,peer,name,0,"")
    originCheck(t,p,pb,peer,"origin_control_peer_sub",peerID,peerName)
    originCheck(t,p,pb,tx,"origin_control_pending",pendingID,pendingName)
    if err:=subscriptionRestoreDuringRead(t,p,first.Bytes(),func(context.CancelFunc) {
     originCreate(t,p,pb,peer,"origin_control_during_read",false)
    });err!=nil { t.Fatal(err) }
    fresh,freshName:=originIdentity(t,p,b.DB,name)
    if fresh==peerID || fresh==pendingID || freshName==canonical { t.Fatal("replacement registration collision",fresh,freshName) }
    if err:=tx.Commit();err!=nil { t.Fatal(err) }
    originCheck(t,p,pb,peer,"origin_control_peer_sub",peerID,peerName)
    originCheck(t,p,b,b.DB,"origin_control_pending",pendingID,pendingName)
    duringID,duringName:=originIdentity(t,p,peer,"origin_control_during_read")
    originCheck(t,p,b,b.DB,"origin_control_during_read",duringID,duringName)
    if duringID==fresh || duringID==pendingID || duringID==peerID { t.Fatal("gated peer registration collision") }
    if !reflect.DeepEqual(originBag(t,p,b.DB),originBag(t,p,peer)) { t.Fatal("replacement origins not shared") }
    subscriptionExec(t,p,pb,peer,"DROP SUBSCRIPTION origin_control_pending",false)
    subscriptionExec(t,p,pb,peer,"DROP SUBSCRIPTION origin_control_peer_sub",false)
    subscriptionExec(t,p,pb,peer,"DROP SUBSCRIPTION origin_control_during_read",false)
   } else {
    if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil { t.Fatal(err) }
    originCheck(t,p,b,held,name,0,"")
    if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(first.Bytes()));err!=nil { t.Fatal(err) }
    var redump bytes.Buffer
    if err:=stuntgres.Dump(p.Ctx,p.Native,&redump);err!=nil { t.Fatal(err) }
    if !bytes.Equal(first.Bytes(),redump.Bytes()) { t.Fatal("origin remint changed portable SQL dump") }
   }
   fresh,freshName:=originIdentity(t,p,b.DB,name)
   if freshName==canonical { t.Fatal("restore retained donor subscription address") }
   originCheck(t,p,b,held,name,fresh,freshName)
   var oldID int64;var oldName string
   if !old.Next() { t.Fatal("old origin disappeared",old.Err()) };if err:=old.Scan(&oldID,&oldName);err!=nil { t.Fatal(err) }
   if oldID!=id || oldName!=canonical || old.Next() || old.Err()!=nil { t.Fatal("replacement mutated acquired origin",oldID,oldName,old.Err()) };old.Close()
  }
  subscriptionExec(t,p,b,actor,"DROP SUBSCRIPTION "+name,false)
  originCheck(t,p,b,held,name,0,"")
 }) }
}
func TestReferenceReplicationOriginControls(t *testing.T) { originControlScenario(t,"origin_controls",false) }
func TestReferenceReplicationOriginSmoke(t *testing.T) { originControlScenario(t,"origin_smoke",true) }

// Native reader cancellation and late owner-authority veto preserve the
// selected registration bag rather than publishing a partially replayed donor.
func TestReferenceReplicationOriginReplacementBarrier(t *testing.T) {
	for _,change:=range []string{"cancel","owner_veto"} { t.Run(change,func(t *testing.T) {
		fixture.RoleReferenceCleanup(t,[]string{"origin_barrier_owner","origin_barrier_renamed"})
		p:=subscriptionScenario(t,"origin_barrier")
		for _,b:=range p.Backends() { t.Run(b.Name,func(t *testing.T) {
			subscriptionExec(t,p,b,b.DB,"CREATE ROLE origin_barrier_owner",false)
			originCreate(t,p,b,b.DB,"origin_barrier",false)
			subscriptionExec(t,p,b,b.DB,"ALTER SUBSCRIPTION origin_barrier OWNER TO origin_barrier_owner",false)
			id,name:=originIdentity(t,p,b.DB,"origin_barrier")
			owner:="origin_barrier_owner"
			if !b.PG {
				var dump bytes.Buffer
				if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil { t.Fatal(err) }
				before:=originBag(t,p,b.DB)
				err:=subscriptionRestoreDuringRead(t,p,dump.Bytes(),func(cancel context.CancelFunc) {
					if change=="cancel" { cancel() } else {
						subscriptionExec(t,p,b,b.DB,"ALTER ROLE origin_barrier_owner RENAME TO origin_barrier_renamed",false)
						owner="origin_barrier_renamed"
					}
				})
				if change=="cancel" {
					if !errors.Is(err,context.Canceled) { t.Fatal(err) }
				} else if err==nil || err.Error()!=`stuntgres: replacement principal "origin_barrier_owner" does not exist` { t.Fatal("owner veto did not reject publication",err) }
				if !reflect.DeepEqual(before,originBag(t,p,b.DB)) { t.Fatal("failed native boundary changed registration") }
			}
			originCheck(t,p,b,b.DB,"origin_barrier",id,name)
			subscriptionExec(t,p,b,b.DB,"DROP SUBSCRIPTION origin_barrier",false)
			subscriptionExec(t,p,b,b.DB,"DROP ROLE "+owner,false)
		}) }
	}) }
}
